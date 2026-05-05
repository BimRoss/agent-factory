package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/orchestratorevent"
	"github.com/slack-go/slack"
)

// ThreadContextFunc optionally supplies Slack thread history (e.g. from
// conversations.replies) to conversation mode. When nil, only the memory bank
// and the single user turn are used.
type ThreadContextFunc func(ctx context.Context, task Task) string

type Engine struct {
	publisher     StatusPublisher
	handoff       HandoffBus
	tasks         TaskStore
	traces        TraceStore
	registry      Registry
	toolSpecs     map[string]ToolSpec
	provider      ProviderConfig
	memory        *MemoryBank
	threadContext ThreadContextFunc
	remote        RemoteHandoffForwarder
	// slackForEmployee resolves the posting bot's Slack API client (per EMPLOYEE_ID) for write tools like create-company.
	slackForEmployee func(employeeID string) *slack.Client
}

func NewEngine(publisher StatusPublisher, handoff HandoffBus, tasks TaskStore, traces TraceStore, registry Registry, toolSpecs map[string]ToolSpec, provider ProviderConfig, memory *MemoryBank, threadContext ThreadContextFunc, remote RemoteHandoffForwarder, slackForEmployee func(string) *slack.Client) *Engine {
	if toolSpecs == nil {
		toolSpecs = map[string]ToolSpec{}
	}
	return &Engine{
		publisher:        publisher,
		handoff:          handoff,
		tasks:            tasks,
		traces:           traces,
		registry:         registry,
		toolSpecs:        toolSpecs,
		provider:         provider,
		memory:           memory,
		threadContext:    threadContext,
		remote:           remote,
		slackForEmployee: slackForEmployee,
	}
}

func (e *Engine) StartTask(task Task, ownerEmployeeID string) (Task, error) {
	now := time.Now().UTC()
	task.OwnerEmployeeID = ownerEmployeeID
	task.LastState = StatePlanning
	task.CreatedAt = now
	task.UpdatedAt = now
	if err := e.tasks.SaveTask(task); err != nil {
		return task, err
	}
	if err := e.publisher.PublishStatus(LifecycleEvent{
		TaskID:       task.ID,
		ThreadAnchor: task.ThreadAnchor,
		TraceID:      task.TraceID,
		EmployeeID:   ownerEmployeeID,
		StateFrom:    StateReceived,
		StateTo:      StatePlanning,
		Timestamp:    now,
	}); err != nil {
		return task, err
	}
	return task, nil
}

func (e *Engine) DelegateIfMissingSkill(task Task, toEmployeeID, missingSkillID string) (Task, error) {
	requestedAt := time.Now().UTC()
	requestEntry := TraceEntry{
		Sequence:       len(e.traces.ListTrace(task.ID)) + 1,
		EmployeeID:     task.OwnerEmployeeID,
		SkillID:        missingSkillID,
		Status:         "handoff_requested",
		Note:           fmt.Sprintf("requested handoff to %s", toEmployeeID),
		RenderOutputID: "",
		Timestamp:      requestedAt,
	}
	if err := e.traces.AppendTrace(task.ID, requestEntry); err != nil {
		return task, err
	}

	req := HandoffRequest{
		Task:            task,
		FromEmployeeID:  task.OwnerEmployeeID,
		ToEmployeeID:    toEmployeeID,
		Reason:          fmt.Sprintf("owner missing skill %s", missingSkillID),
		RequiredSkillID: missingSkillID,
	}
	updatedTask, err := TransferOwnership(task, req, e.handoff, e.publisher)
	if err != nil {
		return task, err
	}
	updatedTask.LastState = StateHandoffAccepted
	updatedTask.UpdatedAt = time.Now().UTC()
	acceptedEntry := TraceEntry{
		Sequence:       len(e.traces.ListTrace(updatedTask.ID)) + 1,
		EmployeeID:     updatedTask.OwnerEmployeeID,
		SkillID:        missingSkillID,
		Status:         "handoff_accepted",
		Note:           fmt.Sprintf("ownership transferred from %s", req.FromEmployeeID),
		RenderOutputID: "",
		Timestamp:      updatedTask.UpdatedAt,
	}
	if err := e.traces.AppendTrace(updatedTask.ID, acceptedEntry); err != nil {
		return task, err
	}
	if err := e.tasks.SaveTask(updatedTask); err != nil {
		return task, err
	}
	if err := e.publisher.PublishStatus(LifecycleEvent{
		TaskID:           updatedTask.ID,
		ThreadAnchor:     updatedTask.ThreadAnchor,
		TraceID:          updatedTask.TraceID,
		EmployeeID:       updatedTask.OwnerEmployeeID,
		StateFrom:        StateWaitingHandoff,
		StateTo:          StateHandoffAccepted,
		TransitionReason: fmt.Sprintf("handoff accepted for capability %s", missingSkillID),
		Timestamp:        updatedTask.UpdatedAt,
	}); err != nil {
		return task, err
	}
	return updatedTask, nil
}

func (e *Engine) ExecuteCapability(ctx context.Context, task Task, capabilityID string, source *orchestratorevent.EventV1) (Task, error) {
	capabilityID = normalizeID(capabilityID)
	if capabilityID == "" {
		return task, fmt.Errorf("capability id is required")
	}
	if task.ID == "" {
		return task, fmt.Errorf("task id is required")
	}

	if !e.registry.EmployeeHasCapability(task.OwnerEmployeeID, capabilityID) {
		toEmployeeID, ok := e.registry.FindEmployeeForCapability(capabilityID, task.OwnerEmployeeID)
		if !ok || toEmployeeID == "" {
			return task, fmt.Errorf("no employee available for capability %s", capabilityID)
		}
		// True multi-pod handoff: Redis + JetStream to slack.work.<to>.events (no local execution on this process).
		if e.remote != nil && source != nil {
			_ = e.publisher.PublishThreadNotice(task, fmt.Sprintf("Routing `%s` to %s — their worker is taking it from here.", capabilityID, displayEmployeeName(toEmployeeID)))
			if err := e.remote.ForwardRemoteHandoff(ctx, task, task.OwnerEmployeeID, toEmployeeID, capabilityID, source); err != nil {
				return task, err
			}
			fwdAt := time.Now().UTC()
			prevState := task.LastState
			task.LastState = StateForwarded
			task.UpdatedAt = fwdAt
			fwdEntry := TraceEntry{
				Sequence:       len(e.traces.ListTrace(task.ID)) + 1,
				EmployeeID:     task.OwnerEmployeeID,
				SkillID:        capabilityID,
				Status:         "forwarded_remote",
				Note:           fmt.Sprintf("dispatched to %s via NATS+Redis (no local execution)", toEmployeeID),
				RenderOutputID: "",
				Timestamp:      fwdAt,
			}
			if err := e.traces.AppendTrace(task.ID, fwdEntry); err != nil {
				return task, err
			}
			if err := e.tasks.SaveTask(task); err != nil {
				return task, err
			}
			if err := e.publisher.PublishStatus(LifecycleEvent{
				TaskID:           task.ID,
				ThreadAnchor:     task.ThreadAnchor,
				TraceID:          task.TraceID,
				EmployeeID:       task.OwnerEmployeeID,
				StateFrom:        prevState,
				StateTo:          StateForwarded,
				TransitionReason: fmt.Sprintf("remote handoff to %s for %s", toEmployeeID, capabilityID),
				Timestamp:        fwdAt,
			}); err != nil {
				return task, err
			}
			return task, ErrHandoffDispatched
		}
		prevState := task.LastState
		task.LastState = StateWaitingHandoff
		if err := e.tasks.SaveTask(task); err != nil {
			return task, err
		}
		if err := e.publisher.PublishStatus(LifecycleEvent{
			TaskID:           task.ID,
			ThreadAnchor:     task.ThreadAnchor,
			TraceID:          task.TraceID,
			EmployeeID:       task.OwnerEmployeeID,
			StateFrom:        prevState,
			StateTo:          StateWaitingHandoff,
			TransitionReason: fmt.Sprintf("owner missing capability %s", capabilityID),
			Timestamp:        time.Now().UTC(),
		}); err != nil {
			return task, err
		}
		var err error
		task, err = e.DelegateIfMissingSkill(task, toEmployeeID, capabilityID)
		if err != nil {
			return task, err
		}
	}

	startedAt := time.Now().UTC()
	prevState := task.LastState
	task.LastState = StateRunning
	task.UpdatedAt = startedAt
	if err := e.tasks.SaveTask(task); err != nil {
		return task, err
	}
	if err := e.publisher.PublishStatus(LifecycleEvent{
		TaskID:           task.ID,
		ThreadAnchor:     task.ThreadAnchor,
		TraceID:          task.TraceID,
		EmployeeID:       task.OwnerEmployeeID,
		StateFrom:        prevState,
		StateTo:          StateRunning,
		TransitionReason: fmt.Sprintf("executing capability %s", capabilityID),
		Timestamp:        startedAt,
	}); err != nil {
		return task, err
	}

	startEntry := TraceEntry{
		Sequence:       len(e.traces.ListTrace(task.ID)) + 1,
		EmployeeID:     task.OwnerEmployeeID,
		SkillID:        capabilityID,
		Status:         "started",
		Note:           "execution started",
		RenderOutputID: "",
		Timestamp:      startedAt,
	}
	if err := e.traces.AppendTrace(task.ID, startEntry); err != nil {
		return task, err
	}
	plan := capabilityExecutionPlan(task, capabilityID, e.toolSpecs)
	if isCreateIssueCapability(capabilityID) {
		// PublishUpdate before the long path so the status publisher can add the waiting
		// reaction (e.g. hourglass) to the trigger message while GitHub + model work run.
		if err := e.publisher.PublishUpdate(task, "Gathering thread context and drafting the GitHub issue…"); err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		p, err := e.runCreateIssue(task)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		plan.FinalPayload = p
	} else if isCreateGitHubRepoCapability(capabilityID) {
		if err := e.publisher.PublishUpdate(task, "Creating GitHub repository…"); err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		p, err := e.runCreateGitHubRepo(ctx, task)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		plan.FinalPayload = p
	} else if capabilityID == "read-web" {
		query := strings.TrimSpace(task.RequestText)
		if query == "" {
			query = "Latest relevant updates"
		}
		if err := e.publisher.PublishUpdate(task, "Running Gemini research query..."); err != nil {
			return task, err
		}
		research, err := runGeminiResearch(ctx, e.provider, query)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		summary := FormatResearchResultForSlack(research)
		if strings.TrimSpace(summary) == "" {
			_ = e.publisher.ClearInboundReaction(task)
			return task, fmt.Errorf("read-web: model returned no summary")
		}
		plan.FinalPayload = RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-web", task.ID),
			FallbackText: summary,
			FinalSummary: "read-web research completed",
			Transport:    "slack",
		}
	} else if isReadGitHubCapability(capabilityID) {
		if err := e.publisher.PublishUpdate(task, "Querying GitHub repos/code/files..."); err != nil {
			return task, err
		}
		p, err := e.runReadGitHubCapability(ctx, task, capabilityID)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		plan.FinalPayload = p
	} else if capabilityID == "create-google-doc" {
		if err := e.publisher.PublishUpdate(task, "Preparing create-google-doc request..."); err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		p, err := e.runCreateDoc(ctx, task)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		plan.FinalPayload = p
	} else if capabilityID == "create-email" {
		if err := e.publisher.PublishUpdate(task, "Drafting email preview…"); err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		p, err := e.runCreateEmail(ctx, task)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		plan.FinalPayload = p
		plan.ProgressUpdates = nil
	} else if capabilityID == "create-company" {
		if err := e.publisher.PublishUpdate(task, "Creating company channel..."); err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		p, err := e.runCreateCompany(ctx, task)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		plan.FinalPayload = p
	} else if capabilityID == "update-terms" {
		if err := e.publisher.PublishUpdate(task, "Handling terms…"); err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		p, err := e.runUpdateTerms(ctx, task)
		if err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
		plan.FinalPayload = p
		plan.ProgressUpdates = nil
	}
	for _, update := range plan.ProgressUpdates {
		if err := e.publisher.PublishUpdate(task, update); err != nil {
			_ = e.publisher.ClearInboundReaction(task)
			return task, err
		}
	}

	finishedAt := time.Now().UTC()
	finalPayload := plan.FinalPayload
	if finalPayload.OutputID == "" {
		finalPayload.OutputID = fmt.Sprintf("%s-%s", task.ID, capabilityID)
	}
	if finalPayload.FallbackText == "" {
		finalPayload.FallbackText = fmt.Sprintf("Completed %s via %s.", capabilityID, task.OwnerEmployeeID)
	}
	if finalPayload.FinalSummary == "" {
		finalPayload.FinalSummary = fmt.Sprintf("Capability %s completed", capabilityID)
	}
	if finalPayload.Transport == "" {
		finalPayload.Transport = "slack"
	}
	completedEntry := TraceEntry{
		Sequence:       len(e.traces.ListTrace(task.ID)) + 1,
		EmployeeID:     task.OwnerEmployeeID,
		SkillID:        capabilityID,
		Status:         "completed",
		Note:           finalPayload.FinalSummary,
		RenderOutputID: finalPayload.OutputID,
		Timestamp:      finishedAt,
	}
	if err := e.traces.AppendTrace(task.ID, completedEntry); err != nil {
		_ = e.publisher.ClearInboundReaction(task)
		return task, err
	}

	task.LastState = StateCompleted
	task.UpdatedAt = finishedAt
	if err := e.tasks.SaveTask(task); err != nil {
		_ = e.publisher.ClearInboundReaction(task)
		return task, err
	}
	if err := e.publisher.PublishStatus(LifecycleEvent{
		TaskID:           task.ID,
		ThreadAnchor:     task.ThreadAnchor,
		TraceID:          task.TraceID,
		EmployeeID:       task.OwnerEmployeeID,
		StateFrom:        StateRunning,
		StateTo:          StateCompleted,
		TransitionReason: fmt.Sprintf("completed capability %s", capabilityID),
		Timestamp:        finishedAt,
	}); err != nil {
		_ = e.publisher.ClearInboundReaction(task)
		return task, err
	}
	if err := e.publisher.PublishFinal(task, finalPayload); err != nil {
		return task, err
	}
	return task, nil
}

func displayEmployeeName(employeeID string) string {
	id := strings.ToLower(strings.TrimSpace(employeeID))
	if id == "" {
		return "teammate"
	}
	if len(id) == 1 {
		return strings.ToUpper(id)
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

func isCreateIssueCapability(capabilityID string) bool {
	switch normalizeID(capabilityID) {
	case "create-issue", "create-github-issue":
		return true
	default:
		return false
	}
}

func isCreateGitHubRepoCapability(capabilityID string) bool {
	switch normalizeID(capabilityID) {
	case "create-github-repo", "create-repo":
		return true
	default:
		return false
	}
}
