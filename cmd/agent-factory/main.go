package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bimross/agent-factory/internal/handoffremote"
	"github.com/bimross/agent-factory/internal/natsbus"
	"github.com/bimross/agent-factory/internal/orchestratorevent"
	"github.com/bimross/agent-factory/internal/runtime"
	"github.com/bimross/agent-factory/internal/slackrender"
	"github.com/bimross/agent-factory/internal/slackthread"
	"github.com/slack-go/slack"
)

var (
	// Strong signals: URLs/names, PR language, owner/repo paths, gh-style keys.
	reGitHubHostOrWord = regexp.MustCompile(`(?i)github\.com|\bgithub\b`)
	reGitHubPRTalk     = regexp.MustCompile(`(?i)\bpull\s+requests?\b|\bprs\b`)
	// Two path segments with a slash — matches bimross/agent-factory, not grant-llc.
	reGitHubOwnerRepoSlash = regexp.MustCompile(`\b[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?/[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?\b`)
	reGitHubCLIKey         = regexp.MustCompile(`(?i)\b(?:org|user):`)
	// Weaker: hyphenated project slug only counts with SCM-flavored verbs (avoids #grant-llc intros).
	reGitHubWorkCue     = regexp.MustCompile(`(?i)\b(?:commits?|pull\s+requests?|prs|branches?|repositories?|\brepos?\b)\b`)
	reHyphenatedNameCue = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9._-]*-[A-Za-z0-9._-]+\b`)
	githubThreadHints   = newThreadCapabilityHints(defaultGitHubFollowupStickyTTL())
)

func main() {
	appConfig, err := runtime.LoadAppConfigFromEnv()
	if err != nil {
		log.Fatalf("load app config: %v", err)
	}
	logGitHubStartupProbe(appConfig.EmployeeID)
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
	remoteForwarder, handoffStore, cleanupHandoff := newRemoteHandoffMaybe(appConfig)
	if cleanupHandoff != nil {
		defer cleanupHandoff()
	}
	slackClients := slackBotClientsFromEnv()
	processEmp := normalizeID(os.Getenv("EMPLOYEE_ID"))
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
		remoteForwarder,
		func(emp string) *slack.Client {
			id := normalizeID(emp)
			if id != "" {
				if c := slackClients[id]; c != nil {
					return c
				}
			}
			if processEmp != "" {
				if c := slackClients[processEmp]; c != nil {
					return c
				}
			}
			for _, c := range slackClients {
				if c != nil {
					return c
				}
			}
			return nil
		},
	)

	switch appConfig.Mode {
	case "demo":
		runDemo(engine, store)
	case "serve":
		if err := runServe(appConfig, engine, handoffStore, publisher, slackClients); err != nil {
			log.Fatalf("serve: %v", err)
		}
	default:
		log.Fatalf("unknown AGENT_FACTORY_MODE=%q (supported: serve, demo)", appConfig.Mode)
	}
}

func logGitHubStartupProbe(employeeID string) {
	probe := runtime.ProbeGitHubAccess(context.Background(), employeeID)
	if !probe.TokenConfigured {
		log.Printf("github probe employee=%s token=missing", firstNonEmpty(probe.EmployeeID, normalizeID(employeeID)))
		return
	}
	if probe.ListScopeOK {
		log.Printf(
			"github probe employee=%s owner=%s scope=%s list_scope=ok token_type=%s oauth_scopes=%q",
			firstNonEmpty(probe.EmployeeID, normalizeID(employeeID)),
			probe.Owner,
			probe.Scope,
			strings.TrimSpace(probe.TokenTypeHint),
			strings.TrimSpace(probe.TokenScopes),
		)
		return
	}
	log.Printf(
		"github probe employee=%s owner=%s scope=%s list_scope=failed token_type=%s oauth_scopes=%q warning=%s err=%s",
		firstNonEmpty(probe.EmployeeID, normalizeID(employeeID)),
		probe.Owner,
		probe.Scope,
		strings.TrimSpace(probe.TokenTypeHint),
		strings.TrimSpace(probe.TokenScopes),
		probe.Warning,
		probe.Error,
	)
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
	ownedTask, err = engine.ExecuteCapability(context.Background(), ownedTask, "create-issue", nil)
	if err != nil {
		log.Fatalf("execute create-issue: %v", err)
	}

	fmt.Printf("scenario1 owner after completion: %s\n", ownedTask.OwnerEmployeeID)
	for _, entry := range store.ListTrace(ownedTask.ID) {
		fmt.Printf("trace1 seq=%d owner=%s capability=%s status=%s\n", entry.Sequence, entry.EmployeeID, entry.SkillID, entry.Status)
	}

	// Scenario 2: @joanne create-company stays on joanne (needs Slack bot token + REDIS for full execution).
	taskTwo := runtime.Task{
		ID:           "bootstrap-task-2",
		ThreadAnchor: "C123456:1746380200.000200",
		TraceID:      "trace-bootstrap-2",
		RequestText:  "create a company called legendz",
		HumanUserID:  "U_OPERATOR",
	}
	ownedTaskTwo, err := engine.StartTask(taskTwo, "joanne")
	if err != nil {
		log.Fatalf("start task 2: %v", err)
	}
	ownedTaskTwo, err = engine.ExecuteCapability(context.Background(), ownedTaskTwo, "create-company", nil)
	if err != nil {
		fmt.Printf("scenario2 create-company error (expected without real Slack/Redis in demo): %v\n", err)
	} else {
		fmt.Printf("scenario2 owner after completion: %s\n", ownedTaskTwo.OwnerEmployeeID)
	}
	for _, entry := range store.ListTrace(ownedTaskTwo.ID) {
		fmt.Printf("trace2 seq=%d owner=%s capability=%s status=%s\n", entry.Sequence, entry.EmployeeID, entry.SkillID, entry.Status)
	}
}

// startWorkerStatusHTTPServer binds HTTP_ADDR (default :8080) for Kubernetes /health and /readyz.
// Without it, probes to :8080 get connection refused and pods never become Ready.
func startWorkerStatusHTTPServer(ctx context.Context, natsURL string) {
	addr := strings.TrimSpace(os.Getenv("HTTP_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	ready := func() bool {
		u, err := url.Parse(strings.TrimSpace(natsURL))
		if err != nil || u.Host == "" {
			return false
		}
		d, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
		if err != nil {
			return false
		}
		_ = d.Close()
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("nats_unreachable\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Printf("worker status http listen=%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("worker status http: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()
}

func runServe(cfg runtime.AppConfig, engine *runtime.Engine, handoffStore *handoffremote.Store, publisher runtime.StatusPublisher, slackClients map[string]*slack.Client) error {
	log.Printf("agent-factory serve mode employee=%s nats=%s stream=%s",
		cfg.EmployeeID,
		cfg.NatsURL,
		cfg.NatsStream,
	)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	startWorkerStatusHTTPServer(ctx, cfg.NatsURL)

	startJoanneInteractionSocket(ctx, cfg.EmployeeID)

	handler := func(ctx context.Context, payload []byte) error {
		return handleOrchestratorPayload(ctx, cfg, engine, handoffStore, publisher, slackClients, payload)
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

func handleOrchestratorPayload(ctx context.Context, cfg runtime.AppConfig, engine *runtime.Engine, handoffStore *handoffremote.Store, publisher runtime.StatusPublisher, slackClients map[string]*slack.Client, payload []byte) error {
	var event orchestratorevent.EventV1
	if err := json.Unmarshal(payload, &event); err != nil {
		log.Printf("orchestrator payload parse failed: %v", err)
		return nil
	}
	if stale, age, cutoff := shouldDropStaleOrchestratorEvent(event); stale {
		log.Printf(
			"orchestrator payload drop stale target=%s channel=%s message_ts=%s age=%s cutoff=%s",
			normalizeID(event.TargetEmployee),
			strings.TrimSpace(event.Message.ChannelID),
			strings.TrimSpace(firstNonEmpty(event.Message.MessageTS, event.Message.ThreadTS)),
			age,
			cutoff,
		)
		return nil
	}
	orchestratorevent.EnsureRunAndTraceIDs(&event)
	if skip, _ := runtime.ShouldSkipDuplicateOrchestratorPayload(ctx, event); skip {
		return nil
	}
	employeeID := cfg.EmployeeID
	target := normalizeID(event.TargetEmployee)
	if target != normalizeID(employeeID) {
		log.Printf("orchestrator payload target mismatch expected=%s got=%s", employeeID, target)
		return nil
	}
	if handled := maybeHandleHumansOperatorJoined(ctx, cfg, slackClients, event); handled {
		return nil
	}
	if handled := maybeHandleJoanneInviteOnboardingHook(ctx, cfg, slackClients, event); handled {
		return nil
	}
	if sp, ok := publisher.(*statusPublisher); ok {
		sp.markPipelineChainWaiting(event)
	}

	if event.Continuation != nil && strings.TrimSpace(event.Continuation.HandoffID) != "" {
		if handoffStore == nil {
			log.Printf("handoff continuation received but REDIS_URL is not configured; acking")
			return nil
		}
		return handleHandoffContinuation(ctx, cfg, engine, handoffStore, publisher, event)
	}

	capabilityID := strings.TrimSpace(event.Decision.ToolID)
	kind := normalizeID(event.Decision.Kind)

	if normalizeID(employeeID) == "joanne" && strings.TrimSpace(capabilityID) == "" {
		anchorTS := effectiveThreadTS(event.Message)
		api := slackClients["joanne"]
		if api == nil {
			api = slackClients[normalizeID(employeeID)]
		}
		if runtime.MaybeHandleEmailConfirmationPlaintext(ctx, api,
			strings.TrimSpace(event.Message.ChannelID),
			strings.TrimSpace(event.Message.UserID),
			anchorTS,
			strings.TrimSpace(event.Message.Text),
		) {
			return nil
		}
	}

	taskID := deriveTaskID(event)
	traceID := firstNonEmpty(event.EffectiveTraceID(), taskID)
	anchorTS := effectiveThreadTS(event.Message)
	threadAnchor := strings.TrimSpace(event.Message.ChannelID)
	if threadAnchor != "" && anchorTS != "" {
		threadAnchor += ":" + anchorTS
	}

	task := runtime.Task{
		ID:           taskID,
		TraceID:      traceID,
		ThreadAnchor: threadAnchor,
		RequestText:  buildTaskRequestText(event),
		ChannelID:    strings.TrimSpace(event.Message.ChannelID),
		ThreadTS:     anchorTS,
		MessageTS:    strings.TrimSpace(event.Message.MessageTS),
		HumanUserID:  strings.TrimSpace(event.Message.UserID),
		Mode:         firstNonEmpty(modeFromDecision(event), "conversation"),
	}
	if capabilityID == "" && employeeHandlesGitHubFollowupHints(employeeID) {
		procEmp := normalizeID(employeeID)
		stickyCapability, stickyOK := githubThreadHints.recall(procEmp, task.ThreadAnchor, time.Now().UTC())
		if stickyOK && stickyCapability != "" && isGitHubLikelyFollowUp(task.RequestText) {
			capabilityID = stickyCapability
			log.Printf(
				"github routing sticky-hit employee=%s task=%s trace=%s capability=%s",
				employeeID,
				taskID,
				traceID,
				capabilityID,
			)
		} else if isGitHubLikelyFollowUp(task.RequestText) {
			task.RequestText = appendGitHubNoToolContext(task.RequestText, cfg.EmployeeID)
		}
	}
	ownedTask, err := engine.StartTask(task, employeeID)
	if err != nil {
		return fmt.Errorf("start task %s: %w", taskID, err)
	}
	if capabilityID != "" {
		executedTask, err := engine.ExecuteCapability(ctx, ownedTask, capabilityID, &event)
		if err != nil {
			if errors.Is(err, runtime.ErrHandoffDispatched) {
				log.Printf("remote handoff dispatched task=%s tool=%s trace=%s", taskID, normalizeID(capabilityID), traceID)
				return nil
			}
			if errors.Is(err, runtime.ErrNoEmployeeForCapability) {
				log.Printf("orchestrator tool not in worker registry; falling back to conversation task=%s tool=%s trace=%s err=%v",
					taskID, normalizeID(capabilityID), traceID, err)
				fb := ownedTask
				fb.Mode = "conversation"
				executedTask, convErr := engine.ExecuteConversation(fb)
				if convErr != nil {
					return fmt.Errorf("execute capability %s for task %s: %w; conversation fallback: %v", capabilityID, taskID, err, convErr)
				}
				maybePublishPipelineContinuation(cfg, event, executedTask)
				log.Printf("processed orchestrator conversation employee=%s task=%s trace=%s kind=%s (capability_unavailable_fallback)",
					employeeID, taskID, traceID, kind)
				return nil
			}
			return fmt.Errorf("execute capability %s for task %s: %w", capabilityID, taskID, err)
		}
		maybePublishPipelineContinuation(cfg, event, executedTask)
		if isReadGitHubCapabilityID(capabilityID) && employeeHandlesGitHubFollowupHints(employeeID) {
			githubThreadHints.remember(normalizeID(employeeID), executedTask.ThreadAnchor, capabilityID, time.Now().UTC())
		}
		log.Printf("processed orchestrator event employee=%s task=%s tool=%s trace=%s kind=%s",
			employeeID,
			taskID,
			capabilityID,
			traceID,
			kind,
		)
		return nil
	}
	executedTask, err := engine.ExecuteConversation(ownedTask)
	if err != nil {
		return fmt.Errorf("execute conversation for task %s: %w", taskID, err)
	}
	maybePublishPipelineContinuation(cfg, event, executedTask)
	log.Printf("processed orchestrator conversation employee=%s task=%s trace=%s kind=%s",
		employeeID, taskID, traceID, kind)
	_ = ctx
	return nil
}

func maybeHandleHumansOperatorJoined(ctx context.Context, cfg runtime.AppConfig, slackClients map[string]*slack.Client, event orchestratorevent.EventV1) bool {
	if normalizeID(cfg.EmployeeID) != "joanne" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(event.InnerType), "humans_operator_joined") {
		return false
	}
	api := slackClients["joanne"]
	if api == nil {
		api = slackClients[normalizeID(cfg.EmployeeID)]
	}
	if api == nil {
		log.Printf("humans_operator_joined: skip reason=no_slack_client user=%s", strings.TrimSpace(event.Message.UserID))
		return true
	}
	userID := strings.TrimSpace(event.Message.UserID)
	if err := runtime.PostHumansChannelWelcome(ctx, api, userID); err != nil {
		log.Printf("humans_operator_joined: welcome user=%s err=%v", userID, err)
	} else {
		log.Printf("humans_operator_joined: ok user=%s channel=%s", userID, strings.TrimSpace(event.Message.ChannelID))
	}
	return true
}

func maybeHandleJoanneInviteOnboardingHook(ctx context.Context, cfg runtime.AppConfig, slackClients map[string]*slack.Client, event orchestratorevent.EventV1) bool {
	if normalizeID(cfg.EmployeeID) != "joanne" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(event.InnerType), "member_joined_channel") {
		return false
	}
	channelID := strings.TrimSpace(event.Message.ChannelID)
	if channelID == "" {
		return true
	}
	api := slackClients["joanne"]
	if api == nil {
		api = slackClients[normalizeID(cfg.EmployeeID)]
	}
	if api == nil {
		log.Printf("joanne invite onboarding: skip channel=%s reason=no_slack_client", channelID)
		return true
	}
	inviter := strings.TrimSpace(event.Message.UserID)
	if err := runtime.HandleJoanneInvitedCompanyChannel(ctx, api, channelID, inviter); err != nil {
		log.Printf("joanne invite onboarding: channel=%s inviter=%s err=%v", channelID, inviter, err)
		return true
	}
	log.Printf("joanne invite onboarding: queued channel=%s inviter=%s", channelID, inviter)
	return true
}

func appendGitHubNoToolContext(raw, employeeID string) string {
	trimmed := strings.TrimSpace(raw)
	probe := runtime.ProbeGitHubAccess(context.Background(), employeeID)
	accessSummary := "GitHub access check: token missing."
	if probe.TokenConfigured {
		accessSummary = fmt.Sprintf(
			"GitHub access check: token configured (owner=%s, scope=%s, list_scope_ok=%t).",
			firstNonEmpty(strings.TrimSpace(probe.Owner), "(unresolved)"),
			firstNonEmpty(strings.TrimSpace(probe.Scope), "(unresolved)"),
			probe.ListScopeOK,
		)
	}
	note := fmt.Sprintf(
		"System note: this message looks GitHub-related, but no GitHub tool intent was routed for this turn. %s Reply in plain text, state that this answer is not a live GitHub API read, and ask the user to explicitly request `read-github` (or ask for repo/commits/PRs directly) for live/private GitHub data.",
		accessSummary,
	)
	if trimmed == "" {
		return note
	}
	return trimmed + "\n\n" + note
}

// employeeHandlesGitHubFollowupHints limits sticky read-github routing and the
// "no live GitHub tool" system note to the automation/engineering pod that owns GitHub skills.
func employeeHandlesGitHubFollowupHints(employeeID string) bool {
	return normalizeID(employeeID) == "ross"
}

func isGitHubLikelyFollowUp(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if reGitHubHostOrWord.MatchString(raw) {
		return true
	}
	if reGitHubPRTalk.MatchString(raw) {
		return true
	}
	if reGitHubOwnerRepoSlash.MatchString(raw) {
		return true
	}
	if reGitHubCLIKey.MatchString(raw) {
		return true
	}
	return reGitHubWorkCue.MatchString(raw) && reHyphenatedNameCue.MatchString(raw)
}

func isReadGitHubCapabilityID(capabilityID string) bool {
	switch normalizeID(capabilityID) {
	case "read-github", "read-github-repos", "read-github-repo-meta", "read-github-tree", "read-github-file", "read-github-code-search", "read-github-commits", "read-github-prs", "read-github-branches":
		return true
	default:
		return false
	}
}

type threadCapabilityHint struct {
	capabilityID string
	updatedAt    time.Time
}

type threadCapabilityHints struct {
	mu   sync.RWMutex
	ttl  time.Duration
	byID map[string]threadCapabilityHint
}

func newThreadCapabilityHints(ttl time.Duration) *threadCapabilityHints {
	if ttl <= 0 {
		ttl = 8 * time.Minute
	}
	return &threadCapabilityHints{
		ttl:  ttl,
		byID: map[string]threadCapabilityHint{},
	}
}

func (h *threadCapabilityHints) remember(employeeID, threadAnchor, capabilityID string, now time.Time) {
	key := h.key(employeeID, threadAnchor)
	capabilityID = normalizeID(capabilityID)
	if key == "" || capabilityID == "" {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.byID[key] = threadCapabilityHint{
		capabilityID: capabilityID,
		updatedAt:    now,
	}
}

func (h *threadCapabilityHints) recall(employeeID, threadAnchor string, now time.Time) (string, bool) {
	key := h.key(employeeID, threadAnchor)
	if key == "" {
		return "", false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	h.mu.RLock()
	hint, ok := h.byID[key]
	h.mu.RUnlock()
	if !ok {
		return "", false
	}
	if now.Sub(hint.updatedAt) > h.ttl {
		h.mu.Lock()
		delete(h.byID, key)
		h.mu.Unlock()
		return "", false
	}
	return hint.capabilityID, true
}

func (h *threadCapabilityHints) key(employeeID, threadAnchor string) string {
	employeeID = normalizeID(employeeID)
	threadAnchor = strings.TrimSpace(threadAnchor)
	if employeeID == "" || threadAnchor == "" {
		return ""
	}
	return employeeID + "|" + threadAnchor
}

func defaultGitHubFollowupStickyTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("GITHUB_FOLLOWUP_STICKY_WINDOW_SEC"))
	if raw == "" {
		return 8 * time.Minute
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return 8 * time.Minute
	}
	return time.Duration(secs) * time.Second
}

func newRemoteHandoffMaybe(cfg runtime.AppConfig) (runtime.RemoteHandoffForwarder, *handoffremote.Store, func()) {
	redisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if redisURL == "" {
		return nil, nil, nil
	}
	ttlSec := 86400
	if v := strings.TrimSpace(os.Getenv("HANDOFF_REDIS_TTL_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttlSec = n
		}
	}
	prefix := strings.TrimSpace(os.Getenv("HANDOFF_REDIS_KEY_PREFIX"))
	store, err := handoffremote.NewStore(redisURL, prefix, time.Duration(ttlSec)*time.Second)
	if err != nil {
		log.Printf("remote handoff disabled: redis: %v", err)
		return nil, nil, nil
	}
	fwd, err := handoffremote.NewForwarder(store, cfg.NatsURL, cfg.NatsStream)
	if err != nil {
		_ = store.Close()
		log.Printf("remote handoff disabled: nats: %v", err)
		return nil, nil, nil
	}
	log.Printf("remote handoff enabled (Redis + JetStream to peer subjects)")
	return fwd, store, func() {
		fwd.Close()
		_ = store.Close()
	}
}

func handleHandoffContinuation(ctx context.Context, cfg runtime.AppConfig, engine *runtime.Engine, store *handoffremote.Store, publisher runtime.StatusPublisher, event orchestratorevent.EventV1) error {
	employeeID := cfg.EmployeeID
	hid := strings.TrimSpace(event.Continuation.HandoffID)
	rec, err := store.Get(ctx, hid)
	if err != nil {
		return fmt.Errorf("handoff redis get: %w", err)
	}
	if rec == nil {
		log.Printf("handoff continuation: unknown id=%s (expired or claimed)", hid)
		return nil
	}
	if normalizeID(rec.ToEmployee) != normalizeID(employeeID) {
		log.Printf("handoff continuation: employee mismatch record=%s consumer=%s", rec.ToEmployee, employeeID)
		return nil
	}
	capabilityID := normalizeID(strings.TrimSpace(firstNonEmpty(event.Decision.ToolID, rec.CapabilityID)))
	if capabilityID == "" {
		log.Printf("handoff continuation: missing capability handoff_id=%s", hid)
		return nil
	}
	if capabilityID != normalizeID(rec.CapabilityID) {
		log.Printf("handoff continuation: capability mismatch event=%s record=%s", capabilityID, rec.CapabilityID)
		return nil
	}

	merge := event
	merge.Message = rec.Message
	merge.Decision = rec.Decision
	merge.TraceID = firstNonEmpty(rec.TraceID, event.TraceID)
	merge.RunID = firstNonEmpty(rec.RunID, event.RunID)
	merge.SlackEventID = firstNonEmpty(rec.SlackEventID, event.SlackEventID)
	merge.SchemaVersion = firstNonEmpty(rec.EventSchemaVersion, event.SchemaVersion)
	merge.TargetEmployee = employeeID
	merge.Continuation = nil
	orchestratorevent.EnsureRunAndTraceIDs(&merge)
	if sp, ok := publisher.(*statusPublisher); ok {
		sp.markPipelineChainWaiting(merge)
	}

	taskID := deriveTaskID(merge)
	traceID := firstNonEmpty(merge.EffectiveTraceID(), taskID)
	anchorTS := effectiveThreadTS(merge.Message)
	threadAnchor := strings.TrimSpace(merge.Message.ChannelID)
	if threadAnchor != "" && anchorTS != "" {
		threadAnchor += ":" + anchorTS
	}

	task := runtime.Task{
		ID:           taskID,
		TraceID:      traceID,
		ThreadAnchor: threadAnchor,
		RequestText:  buildTaskRequestText(merge),
		ChannelID:    strings.TrimSpace(merge.Message.ChannelID),
		ThreadTS:     anchorTS,
		MessageTS:    strings.TrimSpace(merge.Message.MessageTS),
		HumanUserID:  strings.TrimSpace(merge.Message.UserID),
		Mode:         firstNonEmpty(modeFromDecision(merge), "conversation"),
	}
	ownedTask, err := engine.StartTask(task, employeeID)
	if err != nil {
		return fmt.Errorf("handoff start task %s: %w", taskID, err)
	}
	executedTask, err := engine.ExecuteCapability(ctx, ownedTask, capabilityID, nil)
	if err != nil {
		return fmt.Errorf("handoff execute %s: %w", capabilityID, err)
	}
	maybePublishPipelineContinuation(cfg, merge, executedTask)
	if err := store.Delete(ctx, hid); err != nil {
		log.Printf("handoff continuation: redis delete %s: %v", hid, err)
	}
	log.Printf("handoff continuation done handoff_id=%s task=%s capability=%s employee=%s", hid, taskID, capabilityID, employeeID)
	return nil
}

func deriveTaskID(event orchestratorevent.EventV1) string {
	if v := strings.TrimSpace(event.RunID); v != "" {
		if strings.EqualFold(strings.TrimSpace(event.Decision.ExecutionMode), orchestratorevent.ExecutionModePipeline) {
			step := event.Decision.PipelineStepIndex
			if step < 0 {
				step = 0
			}
			return fmt.Sprintf("%s:step:%d:%s", v, step, normalizeID(event.TargetEmployee))
		}
		return v + ":" + normalizeID(event.TargetEmployee)
	}
	if v := strings.TrimSpace(event.TraceID); v != "" {
		if strings.EqualFold(strings.TrimSpace(event.Decision.ExecutionMode), orchestratorevent.ExecutionModePipeline) {
			step := event.Decision.PipelineStepIndex
			if step < 0 {
				step = 0
			}
			return fmt.Sprintf("%s:step:%d:%s", v, step, normalizeID(event.TargetEmployee))
		}
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

func maybePublishPipelineContinuation(cfg runtime.AppConfig, current orchestratorevent.EventV1, task runtime.Task) {
	d := current.Decision
	if !strings.EqualFold(strings.TrimSpace(d.ExecutionMode), orchestratorevent.ExecutionModePipeline) {
		return
	}
	steps := d.PipelineSteps
	idx := d.PipelineStepIndex
	if idx < 0 || idx >= len(steps) {
		return
	}
	nextIdx := idx + 1
	if nextIdx >= len(steps) {
		return
	}
	next := steps[nextIdx]
	nextTarget := normalizeID(next.TargetEmployee)
	if nextTarget == "" {
		return
	}

	out := current
	out.SchemaVersion = orchestratorevent.SchemaVersionPipeline
	out.TargetEmployee = nextTarget
	out.Decision.PipelineStepIndex = nextIdx
	out.Decision.Kind = strings.TrimSpace(next.Kind)
	out.Decision.ToolID = strings.TrimSpace(next.ToolID)
	out.Decision.Employees = []string{nextTarget}
	out.Decision.PrimaryEmployee = nextTarget
	if strings.TrimSpace(out.Message.ThreadTS) == "" {
		out.Message.ThreadTS = effectiveThreadTS(current.Message)
	}
	out.Message.Text = strings.TrimSpace(next.StepText)
	if out.Message.Text == "" {
		out.Message.Text = strings.TrimSpace(current.Message.PipelineAnchorText)
	}
	if strings.TrimSpace(out.Message.PipelineAnchorText) == "" {
		out.Message.PipelineAnchorText = strings.TrimSpace(current.Message.PipelineAnchorText)
	}
	orchestratorevent.EnsureRunAndTraceIDs(&out)
	if strings.TrimSpace(out.RunID) == "" {
		// Back-compat: if orchestrator did not populate run_id, anchor to the finished task id.
		out.RunID = strings.TrimSpace(task.TraceID)
		out.TraceID = strings.TrimSpace(task.TraceID)
	}

	log.Printf(
		"pipeline: publish continuation run_id=%s trace_id=%s from=%s step=%d to=%s kind=%s tool=%s",
		strings.TrimSpace(out.RunID),
		strings.TrimSpace(out.TraceID),
		normalizeID(current.TargetEmployee),
		nextIdx,
		nextTarget,
		normalizeID(out.Decision.Kind),
		normalizeID(out.Decision.ToolID),
	)
	if err := natsbus.PublishOrchestratorEvent(cfg.NatsURL, cfg.NatsStream, cfg.EmployeeID, &out); err != nil {
		log.Printf("pipeline: publish continuation failed run_id=%s step=%d err=%v", strings.TrimSpace(out.RunID), nextIdx, err)
	}
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

func shouldDropStaleOrchestratorEvent(event orchestratorevent.EventV1) (bool, time.Duration, time.Duration) {
	cutoff := orchestratorMaxEventAge()
	if cutoff <= 0 {
		return false, 0, cutoff
	}
	tsRaw := strings.TrimSpace(firstNonEmpty(event.Message.MessageTS, event.Message.ThreadTS))
	if tsRaw == "" {
		return false, 0, cutoff
	}
	sec, err := strconv.ParseFloat(tsRaw, 64)
	if err != nil || sec <= 0 {
		return false, 0, cutoff
	}
	evtAt := time.Unix(int64(sec), int64((sec-float64(int64(sec)))*float64(time.Second)))
	age := time.Since(evtAt)
	if age < 0 {
		return false, age, cutoff
	}
	return age > cutoff, age, cutoff
}

func orchestratorMaxEventAge() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ORCHESTRATOR_MAX_EVENT_AGE_SEC"))
	if raw == "" {
		// Defensive default: chat ingress should be near-real-time; older events are likely replay.
		return 6 * time.Hour
	}
	sec, err := strconv.Atoi(raw)
	if err != nil || sec < 0 {
		return 6 * time.Hour
	}
	return time.Duration(sec) * time.Second
}

func effectiveThreadTS(msg orchestratorevent.MessageV1) string {
	return strings.TrimSpace(firstNonEmpty(msg.ThreadTS, msg.MessageTS))
}

func buildTaskRequestText(event orchestratorevent.EventV1) string {
	stepText := strings.TrimSpace(event.Message.Text)
	anchorText := strings.TrimSpace(event.Message.PipelineAnchorText)
	if stepText == "" {
		return anchorText
	}
	if !strings.EqualFold(strings.TrimSpace(event.Decision.ExecutionMode), orchestratorevent.ExecutionModePipeline) {
		return stepText
	}
	if event.Decision.PipelineStepIndex <= 0 || anchorText == "" || strings.EqualFold(anchorText, stepText) {
		return stepText
	}
	return "Current pipeline step request:\n" + stepText + "\n\nOriginal pipeline anchor message:\n" + anchorText
}

type statusPublisher struct {
	// byEmployee maps lowercase employee id → Slack API client. Used for thread posts so the
	// message matches task.OwnerEmployeeID (e.g. Ross speaks after handoff even when NATS landed on Joanne).
	byEmployee map[string]*slack.Client
	// processEmp is EMPLOYEE_ID for this process — used for waiting reactions (same bot must add/remove).
	processEmp string
	waitEmoji  string
	reacted    sync.Map // task ID -> struct{}; we added a waiting reaction for this task
	// pipelineReacted tracks wait reactions this step places on the previous step's message.
	// key: current task ID -> value: "channelID|messageTS".
	pipelineReacted sync.Map
	botUserIDs      map[string]string
}

func newStatusPublisherFromEnv() runtime.StatusPublisher {
	by := slackBotClientsFromEnv()
	emoji := normalizeWaitingReactionName(os.Getenv("SLACK_WAITING_REACTION"))
	if emoji == "" {
		emoji = "hourglass_flowing_sand"
	}
	processEmp := strings.ToLower(strings.TrimSpace(os.Getenv("EMPLOYEE_ID")))
	if len(by) == 0 {
		log.Printf("status publisher: no Slack bot tokens (set SLACK_BOT_TOKEN + EMPLOYEE_ID or <EMPLOYEE>_SLACK_BOT_TOKEN); Slack disabled")
		return &statusPublisher{waitEmoji: emoji}
	}
	return &statusPublisher{
		byEmployee: by,
		processEmp: processEmp,
		waitEmoji:  emoji,
		botUserIDs: parseMultiagentBotUserIDs(os.Getenv("MULTIAGENT_BOT_USER_IDS")),
	}
}

// slackBotClientsFromEnv loads per-employee bot tokens so one runtime can post as another employee after handoff.
func slackBotClientsFromEnv() map[string]*slack.Client {
	m := make(map[string]*slack.Client)
	defTok := strings.TrimSpace(os.Getenv("SLACK_BOT_TOKEN"))
	defEmp := strings.ToLower(strings.TrimSpace(os.Getenv("EMPLOYEE_ID")))
	if defTok != "" && defEmp != "" {
		m[defEmp] = slack.New(defTok)
	}
	for _, emp := range []string{"joanne", "ross", "alex", "tim", "garth", "anna"} {
		key := strings.ToUpper(emp) + "_SLACK_BOT_TOKEN"
		if tok := strings.TrimSpace(os.Getenv(key)); tok != "" {
			m[emp] = slack.New(tok)
		}
	}
	return m
}

func (s *statusPublisher) processClient() *slack.Client {
	if s == nil || s.byEmployee == nil {
		return nil
	}
	if c := s.byEmployee[s.processEmp]; c != nil {
		return c
	}
	for _, c := range s.byEmployee {
		if c != nil {
			return c
		}
	}
	return nil
}

// clientForMessageOwner chooses which bot posts chat — follows task ownership after handoff.
func (s *statusPublisher) clientForMessageOwner(ownerEmployeeID string) *slack.Client {
	if s == nil || s.byEmployee == nil {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(ownerEmployeeID))
	if id != "" {
		if c := s.byEmployee[id]; c != nil {
			return c
		}
	}
	log.Printf("status publisher: no Slack client for owner=%s; falling back to process bot", ownerEmployeeID)
	return s.processClient()
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

func parseMultiagentBotUserIDs(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		emp := normalizeID(pair[0])
		uid := strings.TrimSpace(pair[1])
		if emp == "" || uid == "" {
			continue
		}
		out[emp] = uid
	}
	return out
}

func (s *statusPublisher) markPipelineChainWaiting(event orchestratorevent.EventV1) {
	if s == nil || s.waitEmoji == "" || s.waitEmoji == "-" {
		return
	}
	d := event.Decision
	if !strings.EqualFold(strings.TrimSpace(d.ExecutionMode), orchestratorevent.ExecutionModePipeline) {
		return
	}
	idx := d.PipelineStepIndex
	steps := d.PipelineSteps
	if idx <= 0 || idx >= len(steps) {
		return
	}
	prevEmp := normalizeID(steps[idx-1].TargetEmployee)
	if prevEmp == "" {
		return
	}
	prevUID := strings.TrimSpace(s.botUserIDs[prevEmp])
	if prevUID == "" {
		return
	}
	api := s.processClient()
	if api == nil {
		return
	}
	channelID := strings.TrimSpace(event.Message.ChannelID)
	if channelID == "" {
		return
	}
	threadTS := strings.TrimSpace(firstNonEmpty(event.Message.ThreadTS, event.Message.MessageTS))
	if threadTS == "" {
		return
	}
	hist, _, _, err := api.GetConversationRepliesContext(context.Background(), &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Inclusive: true,
		Limit:     200,
	})
	if err != nil {
		log.Printf("pipeline: wait-reaction fetch thread failed run_id=%s step=%d err=%v", strings.TrimSpace(event.RunID), idx, err)
		return
	}
	targetTS := ""
	for _, msg := range hist {
		if strings.TrimSpace(msg.User) != prevUID {
			continue
		}
		ts := strings.TrimSpace(msg.Timestamp)
		if ts == "" {
			continue
		}
		if ts > targetTS {
			targetTS = ts
		}
	}
	if targetTS == "" {
		return
	}
	ref := slack.NewRefToMessage(channelID, targetTS)
	if err := api.AddReactionContext(context.Background(), s.waitEmoji, ref); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already_reacted") {
			taskID := strings.TrimSpace(deriveTaskID(event))
			if taskID != "" {
				s.pipelineReacted.Store(taskID, channelID+"|"+targetTS)
			}
			return
		}
		log.Printf(
			"pipeline: wait-reaction add failed run_id=%s from=%s to=%s step=%d ts=%s err=%v",
			strings.TrimSpace(event.RunID),
			prevEmp,
			normalizeID(event.TargetEmployee),
			idx,
			targetTS,
			err,
		)
		return
	}
	log.Printf(
		"pipeline: wait-reaction added run_id=%s from=%s to=%s step=%d ts=%s emoji=%s",
		strings.TrimSpace(event.RunID),
		prevEmp,
		normalizeID(event.TargetEmployee),
		idx,
		targetTS,
		s.waitEmoji,
	)
	taskID := strings.TrimSpace(deriveTaskID(event))
	if taskID != "" {
		s.pipelineReacted.Store(taskID, channelID+"|"+targetTS)
	}
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

func (s *statusPublisher) PublishThreadNotice(task runtime.Task, text string) error {
	log.Printf("thread_notice task=%s owner=%s text=%s", task.ID, task.OwnerEmployeeID, text)
	api := s.clientForMessageOwner(task.OwnerEmployeeID)
	if api == nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	channelID := strings.TrimSpace(task.ChannelID)
	if channelID == "" {
		log.Printf("status publisher: skip thread notice (missing channel) task=%s", task.ID)
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
	if _, _, err := api.PostMessage(channelID, opts...); err != nil {
		return fmt.Errorf("post Slack thread notice: %w", err)
	}
	return nil
}

func (s *statusPublisher) ensureWaitingReaction(task runtime.Task) {
	api := s.processClient()
	if api == nil || s.waitEmoji == "" || s.waitEmoji == "-" {
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
	if err := api.AddReactionContext(ctx, s.waitEmoji, ref); err != nil {
		log.Printf("waiting reaction add emoji=%s task=%s: %v", s.waitEmoji, task.ID, err)
		s.reacted.Delete(task.ID)
	}
}

func (s *statusPublisher) clearWaitingReaction(task runtime.Task) {
	api := s.processClient()
	if api == nil || s.waitEmoji == "" || s.waitEmoji == "-" {
		return
	}
	defer s.clearPipelineChainWaiting(task.ID, api)
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
	if err := api.RemoveReactionContext(ctx, s.waitEmoji, ref); err != nil {
		log.Printf("waiting reaction remove emoji=%s task=%s: %v", s.waitEmoji, task.ID, err)
	}
}

func (s *statusPublisher) clearPipelineChainWaiting(taskID string, api *slack.Client) {
	if s == nil || api == nil {
		return
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	raw, ok := s.pipelineReacted.LoadAndDelete(taskID)
	if !ok {
		return
	}
	key, _ := raw.(string)
	parts := strings.SplitN(strings.TrimSpace(key), "|", 2)
	if len(parts) != 2 {
		return
	}
	channelID := strings.TrimSpace(parts[0])
	messageTS := strings.TrimSpace(parts[1])
	if channelID == "" || messageTS == "" {
		return
	}
	ref := slack.NewRefToMessage(channelID, messageTS)
	if err := api.RemoveReactionContext(context.Background(), s.waitEmoji, ref); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no_reaction") {
			return
		}
		log.Printf("pipeline: wait-reaction remove failed task=%s ts=%s err=%v", taskID, messageTS, err)
	}
}

func (s *statusPublisher) ClearInboundReaction(task runtime.Task) error {
	s.clearWaitingReaction(task)
	return nil
}

func (s *statusPublisher) PublishFinal(task runtime.Task, payload runtime.RenderPayload) error {
	log.Printf("final task=%s owner=%s text=%s", task.ID, task.OwnerEmployeeID, payload.FallbackText)
	s.clearWaitingReaction(task)
	api := s.clientForMessageOwner(task.OwnerEmployeeID)
	if api == nil {
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

	var opts []slack.MsgOption
	if len(payload.BlockKit) > 0 {
		opts = []slack.MsgOption{
			slack.MsgOptionText(text, false),
			slack.MsgOptionBlocks(payload.BlockKit...),
		}
	} else {
		blocks, fallback := slackrender.AgentReplyBlocks(text)
		opts = []slack.MsgOption{
			slack.MsgOptionText(fallback, false),
		}
		if len(blocks) > 0 {
			opts = append(opts, slack.MsgOptionBlocks(blocks...))
		}
	}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	if _, _, err := api.PostMessage(channelID, opts...); err != nil {
		return fmt.Errorf("post Slack final message: %w", err)
	}
	if err := runtime.CommitTermsSkillPendingAnchor(context.Background(), payload.TermsSkillPending); err != nil {
		log.Printf("status publisher: terms skill pending redis err=%v task=%s", err, task.ID)
	}
	if err := runtime.CommitEmailSkillPendingAnchor(context.Background(), payload.EmailSkillPending); err != nil {
		log.Printf("status publisher: email skill pending redis err=%v task=%s", err, task.ID)
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
