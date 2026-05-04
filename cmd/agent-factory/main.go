package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bimross/agent-factory/internal/natsbus"
	"github.com/bimross/agent-factory/internal/orchestratorevent"
	"github.com/bimross/agent-factory/internal/runtime"
	"github.com/bimross/agent-factory/internal/slackrender"
	"github.com/bimross/agent-factory/internal/slackthread"
	"github.com/slack-go/slack"
)

func main() {
	appConfig, err := runtime.LoadAppConfigFromEnv()
	if err != nil {
		log.Fatalf("load app config: %v", err)
	}
	sharedContractsDir := firstNonEmpty(os.Getenv("SHARED_CONTRACTS_DIR"), "../shared-contracts")
	employeeInstancesPath := firstNonEmpty(
		os.Getenv("EMPLOYEE_INSTANCES_FILE"),
		filepath.Join(sharedContractsDir, "employees.instances.v1.json"),
	)
	skillInstancesPath := firstNonEmpty(
		os.Getenv("SKILL_INSTANCES_FILE"),
		filepath.Join(sharedContractsDir, "skills.instances.v1.json"),
	)
	skillFactoryDir := firstNonEmpty(os.Getenv("SKILL_FACTORY_DIR"), "../skill-factory")
	toolSpecsDir := firstNonEmpty(os.Getenv("SKILL_TOOL_SPECS_DIR"), filepath.Join(skillFactoryDir, "tools", "v1"))
	registry, err := runtime.LoadRegistryFromContractFiles(employeeInstancesPath, skillInstancesPath)
	if err != nil {
		log.Fatalf("load registry from shared-contracts: %v", err)
	}
	providerConfig, err := runtime.LoadProviderConfigFromEnv()
	if err != nil {
		log.Fatalf("load provider config: %v", err)
	}
	webSearch := "off"
	if providerConfig.EnableWebResearch {
		webSearch = "on"
	}
	log.Printf("inference provider=%s model=%s key_source=%s gemini_google_search=%s", providerConfig.Provider, providerConfig.Model, providerConfig.KeySource, webSearch)
	toolSpecs, err := runtime.LoadToolSpecsFromDir(toolSpecsDir)
	if err != nil {
		log.Fatalf("load tool specs from skill-factory: %v", err)
	}
	memoryBank, err := runtime.LoadMemoryBank(appConfig.MemoryBankFile)
	if err != nil {
		log.Fatalf("load memory bank: %v", err)
	}
	publisher := newStatusPublisherFromEnv()

	store := runtime.NewMemoryStore()
	engine := runtime.NewEngine(
		publisher,
		acceptAllHandoffBus{},
		store,
		store,
		registry,
		toolSpecs,
		providerConfig,
		memoryBank,
		threadContextFuncFromEnv(),
	)

	switch appConfig.Mode {
	case "demo":
		runDemo(engine, store)
	case "serve":
		if err := runServe(appConfig, engine); err != nil {
			log.Fatalf("serve: %v", err)
		}
	default:
		log.Fatalf("unknown AGENT_FACTORY_MODE=%q (supported: serve, demo)", appConfig.Mode)
	}
}

func runDemo(engine *runtime.Engine, store *runtime.MemoryStore) {

	// Scenario 1: @joanne asks for create-issue -> internal handoff to ross.
	taskOne := runtime.Task{
		ID:           "bootstrap-task-1",
		ThreadAnchor: "C123456:1746380100.000100",
		TraceID:      "trace-bootstrap-1",
	}

	ownedTask, err := engine.StartTask(taskOne, "joanne")
	if err != nil {
		log.Fatalf("start task: %v", err)
	}
	ownedTask, err = engine.ExecuteCapability(ownedTask, "create-issue")
	if err != nil {
		log.Fatalf("execute create-issue: %v", err)
	}

	fmt.Printf("scenario1 owner after completion: %s\n", ownedTask.OwnerEmployeeID)
	for _, entry := range store.ListTrace(ownedTask.ID) {
		fmt.Printf("trace1 seq=%d owner=%s capability=%s status=%s\n", entry.Sequence, entry.EmployeeID, entry.SkillID, entry.Status)
	}

	// Scenario 2: @joanne create-company stays on joanne.
	taskTwo := runtime.Task{
		ID:           "bootstrap-task-2",
		ThreadAnchor: "C123456:1746380200.000200",
		TraceID:      "trace-bootstrap-2",
	}
	ownedTaskTwo, err := engine.StartTask(taskTwo, "joanne")
	if err != nil {
		log.Fatalf("start task 2: %v", err)
	}
	ownedTaskTwo, err = engine.ExecuteCapability(ownedTaskTwo, "create-company")
	if err != nil {
		log.Fatalf("execute create-company: %v", err)
	}
	fmt.Printf("scenario2 owner after completion: %s\n", ownedTaskTwo.OwnerEmployeeID)
	for _, entry := range store.ListTrace(ownedTaskTwo.ID) {
		fmt.Printf("trace2 seq=%d owner=%s capability=%s status=%s\n", entry.Sequence, entry.EmployeeID, entry.SkillID, entry.Status)
	}
}

func runServe(cfg runtime.AppConfig, engine *runtime.Engine) error {
	log.Printf("agent-factory serve mode employee=%s nats=%s stream=%s",
		cfg.EmployeeID,
		cfg.NatsURL,
		cfg.NatsStream,
	)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	handler := func(ctx context.Context, payload []byte) error {
		return handleOrchestratorPayload(ctx, cfg.EmployeeID, engine, payload)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- natsbus.RunOrchestratorConsumer(ctx, natsbus.ConsumerConfig{
			EmployeeID:      cfg.EmployeeID,
			NatsURL:         cfg.NatsURL,
			NatsStream:      cfg.NatsStream,
			NatsDurableName: cfg.NatsDurableName,
			FetchBatch:      cfg.NatsFetchBatch,
			FetchMaxWaitMS:  cfg.NatsFetchWaitMS,
			Workers:         cfg.NatsWorkers,
		}, handler)
	}()

	select {
	case <-ctx.Done():
		log.Printf("agent-factory shutting down employee=%s", cfg.EmployeeID)
		return nil
	case err := <-errCh:
		if err == nil || err == context.Canceled {
			return nil
		}
		return err
	}
}

func handleOrchestratorPayload(ctx context.Context, employeeID string, engine *runtime.Engine, payload []byte) error {
	var event orchestratorevent.EventV1
	if err := json.Unmarshal(payload, &event); err != nil {
		log.Printf("orchestrator payload parse failed: %v", err)
		return nil
	}
	target := normalizeID(event.TargetEmployee)
	if target != normalizeID(employeeID) {
		log.Printf("orchestrator payload target mismatch expected=%s got=%s", employeeID, target)
		return nil
	}

	capabilityID := strings.TrimSpace(event.Decision.ToolID)
	kind := normalizeID(event.Decision.Kind)

	taskID := deriveTaskID(event)
	traceID := firstNonEmpty(event.EffectiveTraceID(), taskID)
	anchorTS := strings.TrimSpace(firstNonEmpty(event.Message.ThreadTS, event.Message.MessageTS))
	threadAnchor := strings.TrimSpace(event.Message.ChannelID)
	if threadAnchor != "" && anchorTS != "" {
		threadAnchor += ":" + anchorTS
	}

	task := runtime.Task{
		ID:           taskID,
		TraceID:      traceID,
		ThreadAnchor: threadAnchor,
		RequestText:  strings.TrimSpace(event.Message.Text),
		ChannelID:    strings.TrimSpace(event.Message.ChannelID),
		ThreadTS:     strings.TrimSpace(event.Message.ThreadTS),
		MessageTS:    strings.TrimSpace(event.Message.MessageTS),
		HumanUserID:  strings.TrimSpace(event.Message.UserID),
		Mode:         firstNonEmpty(modeFromDecision(event), "conversation"),
	}
	ownedTask, err := engine.StartTask(task, employeeID)
	if err != nil {
		return fmt.Errorf("start task %s: %w", taskID, err)
	}
	if capabilityID != "" {
		if _, err := engine.ExecuteCapability(ownedTask, capabilityID); err != nil {
			return fmt.Errorf("execute capability %s for task %s: %w", capabilityID, taskID, err)
		}
		log.Printf("processed orchestrator event employee=%s task=%s tool=%s trace=%s kind=%s",
			employeeID,
			taskID,
			capabilityID,
			traceID,
			kind,
		)
		_ = ctx
		return nil
	}
	if _, err := engine.ExecuteConversation(ownedTask); err != nil {
		return fmt.Errorf("execute conversation for task %s: %w", taskID, err)
	}
	log.Printf("processed orchestrator conversation employee=%s task=%s trace=%s kind=%s",
		employeeID, taskID, traceID, kind)
	_ = ctx
	return nil
}

func deriveTaskID(event orchestratorevent.EventV1) string {
	if v := strings.TrimSpace(event.RunID); v != "" {
		return v + ":" + normalizeID(event.TargetEmployee)
	}
	if v := strings.TrimSpace(event.TraceID); v != "" {
		return v + ":" + normalizeID(event.TargetEmployee)
	}
	if v := strings.TrimSpace(event.SlackEventID); v != "" {
		return "ev:" + v + ":" + normalizeID(event.TargetEmployee)
	}
	channel := strings.TrimSpace(event.Message.ChannelID)
	messageTS := strings.TrimSpace(firstNonEmpty(event.Message.MessageTS, event.Message.ThreadTS))
	if channel != "" && messageTS != "" {
		return "msg:" + channel + ":" + messageTS + ":" + normalizeID(event.TargetEmployee)
	}
	return fmt.Sprintf("task-%d:%s", time.Now().UTC().UnixNano(), normalizeID(event.TargetEmployee))
}

func modeFromDecision(event orchestratorevent.EventV1) string {
	if strings.TrimSpace(event.Decision.ToolID) != "" {
		return "task"
	}
	if normalizeID(event.Decision.Kind) == "conversation" {
		return "conversation"
	}
	return ""
}

func normalizeID(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type statusPublisher struct {
	slackClient *slack.Client
	waitEmoji   string
	reacted     sync.Map // task ID -> struct{}; we added a waiting reaction for this task
}

func newStatusPublisherFromEnv() runtime.StatusPublisher {
	botToken := strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN"))
	if botToken == "" {
		log.Printf("status publisher: SLACK_BOT_TOKEN missing, Slack posting disabled")
		return &statusPublisher{}
	}
	emoji := normalizeWaitingReactionName(os.Getenv("SLACK_WAITING_REACTION"))
	if emoji == "" {
		emoji = "hourglass_flowing_sand"
	}
	return &statusPublisher{
		slackClient: slack.New(botToken),
		waitEmoji:   emoji,
	}
}

func normalizeWaitingReactionName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.EqualFold(s, "none") || s == "-" {
		return "-"
	}
	s = strings.TrimPrefix(s, ":")
	s = strings.TrimSuffix(s, ":")
	return strings.TrimSpace(s)
}

// threadContextFuncFromEnv returns a Slack thread transcript fetcher when
// SLACK_BOT_TOKEN is set. Uses SLACK_BOT_USER_ID if set; otherwise auth.test.
func threadContextFuncFromEnv() runtime.ThreadContextFunc {
	token := strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN"))
	if token == "" {
		return nil
	}
	api := slack.New(token)
	botUID := strings.TrimSpace(os.Getenv("SLACK_BOT_USER_ID"))
	if botUID == "" {
		auth, err := api.AuthTest()
		if err != nil {
			log.Printf("slack thread context: auth.test failed (thread history disabled): %v", err)
			return nil
		}
		botUID = auth.UserID
	}
	maxRunes := 12000
	if v := strings.TrimSpace(os.Getenv("SLACK_THREAD_CONTEXT_MAX_RUNES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxRunes = n
		}
	}
	return func(ctx context.Context, task runtime.Task) string {
		return slackthread.Transcript(ctx, api, botUID, task.ChannelID, task.ThreadTS, task.MessageTS, maxRunes)
	}
}

func (s *statusPublisher) PublishStatus(event runtime.LifecycleEvent) error {
	log.Printf("status task=%s trace=%s employee=%s from=%s to=%s reason=%s ts=%s",
		event.TaskID,
		event.TraceID,
		event.EmployeeID,
		event.StateFrom,
		event.StateTo,
		event.TransitionReason,
		event.Timestamp.Format(time.RFC3339),
	)
	return nil
}

func (s *statusPublisher) PublishUpdate(task runtime.Task, message string) error {
	log.Printf("update task=%s owner=%s message=%s", task.ID, task.OwnerEmployeeID, message)
	s.ensureWaitingReaction(task)
	return nil
}

func (s *statusPublisher) ensureWaitingReaction(task runtime.Task) {
	if s.slackClient == nil || s.waitEmoji == "" || s.waitEmoji == "-" {
		return
	}
	ch := strings.TrimSpace(task.ChannelID)
	ts := strings.TrimSpace(task.MessageTS)
	if ch == "" || ts == "" {
		return
	}
	if _, loaded := s.reacted.LoadOrStore(task.ID, struct{}{}); loaded {
		return
	}
	ref := slack.NewRefToMessage(ch, ts)
	ctx := context.Background()
	if err := s.slackClient.AddReactionContext(ctx, s.waitEmoji, ref); err != nil {
		log.Printf("waiting reaction add emoji=%s task=%s: %v", s.waitEmoji, task.ID, err)
		s.reacted.Delete(task.ID)
	}
}

func (s *statusPublisher) clearWaitingReaction(task runtime.Task) {
	if s.slackClient == nil || s.waitEmoji == "" || s.waitEmoji == "-" {
		return
	}
	if _, ok := s.reacted.LoadAndDelete(task.ID); !ok {
		return
	}
	ch := strings.TrimSpace(task.ChannelID)
	ts := strings.TrimSpace(task.MessageTS)
	if ch == "" || ts == "" {
		return
	}
	ref := slack.NewRefToMessage(ch, ts)
	ctx := context.Background()
	if err := s.slackClient.RemoveReactionContext(ctx, s.waitEmoji, ref); err != nil {
		log.Printf("waiting reaction remove emoji=%s task=%s: %v", s.waitEmoji, task.ID, err)
	}
}

func (s *statusPublisher) ClearInboundReaction(task runtime.Task) error {
	s.clearWaitingReaction(task)
	return nil
}

func (s *statusPublisher) PublishFinal(task runtime.Task, payload runtime.RenderPayload) error {
	log.Printf("final task=%s owner=%s text=%s", task.ID, task.OwnerEmployeeID, payload.FallbackText)
	s.clearWaitingReaction(task)
	if s.slackClient == nil {
		return nil
	}
	text := strings.TrimSpace(payload.FallbackText)
	if text == "" {
		return nil
	}
	channelID := strings.TrimSpace(task.ChannelID)
	if channelID == "" {
		log.Printf("status publisher: skip Slack post (missing channel) task=%s", task.ID)
		return nil
	}
	threadTS := strings.TrimSpace(task.ThreadTS)
	if threadTS == "" {
		threadTS = strings.TrimSpace(task.MessageTS)
	}

	blocks, fallback := slackrender.AgentReplyBlocks(text)
	opts := []slack.MsgOption{
		slack.MsgOptionText(fallback, false),
	}
	if len(blocks) > 0 {
		opts = append(opts, slack.MsgOptionBlocks(blocks...))
	}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	if _, _, err := s.slackClient.PostMessage(channelID, opts...); err != nil {
		return fmt.Errorf("post Slack final message: %w", err)
	}
	return nil
}

type acceptAllHandoffBus struct{}

func (acceptAllHandoffBus) RequestHandoff(req runtime.HandoffRequest) (runtime.HandoffResult, error) {
	return runtime.HandoffResult{
		Accepted: true,
		Reason:   "accepted",
	}, nil
}
