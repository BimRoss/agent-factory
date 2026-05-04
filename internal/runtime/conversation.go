package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

func (e *Engine) ExecuteConversation(task Task) (Task, error) {
	if task.ID == "" {
		return task, fmt.Errorf("task id is required")
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
		TransitionReason: "executing conversation",
		Timestamp:        startedAt,
	}); err != nil {
		return task, err
	}

	startEntry := TraceEntry{
		Sequence:       len(e.traces.ListTrace(task.ID)) + 1,
		EmployeeID:     task.OwnerEmployeeID,
		SkillID:        "conversation",
		Status:         "started",
		Note:           "conversation execution started",
		RenderOutputID: "",
		Timestamp:      startedAt,
	}
	if err := e.traces.AppendTrace(task.ID, startEntry); err != nil {
		return task, err
	}

	if err := e.publisher.PublishUpdate(task, "Drafting response..."); err != nil {
		return task, err
	}
	memoryContext := e.memory.BuildPromptContext(task)
	if e.threadContext != nil {
		ctxFetch := context.Background()
		if tc := strings.TrimSpace(e.threadContext(ctxFetch, task)); tc != "" {
			block := "Thread transcript (Slack; oldest first):\n" + tc
			if memoryContext != "" {
				memoryContext = block + "\n\n" + memoryContext
			} else {
				memoryContext = block
			}
		}
	}
	res, err := runGeminiConversation(context.Background(), e.provider, task.OwnerEmployeeID, task.RequestText, task.Mode, memoryContext)
	replyText := strings.TrimSpace(res.Text)
	if err != nil {
		log.Printf("conversation: model failed task=%s employee=%s err=%v", task.ID, task.OwnerEmployeeID, err)
		replyText = defaultConversationFallback(task.OwnerEmployeeID, task.RequestText, task.Mode)
	}
	if replyText == "" {
		log.Printf("conversation: empty reply after success task=%s employee=%s", task.ID, task.OwnerEmployeeID)
		replyText = defaultConversationFallback(task.OwnerEmployeeID, task.RequestText, task.Mode)
	}
	if err == nil && len(res.Citations) > 0 && replyText != "" {
		replyText = replyText + "\n\nSources:\n- " + strings.Join(res.Citations, "\n- ")
	}

	finishedAt := time.Now().UTC()
	finalSummary := "conversation response completed"
	if err == nil && len(res.Citations) > 0 {
		finalSummary = "conversation response completed (with web grounding)"
	}
	finalPayload := RenderPayload{
		OutputID:     fmt.Sprintf("%s-conversation", task.ID),
		FallbackText: replyText,
		FinalSummary: finalSummary,
		Transport:    "slack",
	}
	completedEntry := TraceEntry{
		Sequence:       len(e.traces.ListTrace(task.ID)) + 1,
		EmployeeID:     task.OwnerEmployeeID,
		SkillID:        "conversation",
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
		TransitionReason: "completed conversation",
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
