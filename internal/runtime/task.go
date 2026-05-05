package runtime

import (
	"time"

	"github.com/slack-go/slack"
)

type Task struct {
	ID              string
	ThreadAnchor    string
	TraceID         string
	RequestText     string
	ChannelID       string
	ThreadTS        string
	MessageTS       string
	HumanUserID     string
	Mode            string
	OwnerEmployeeID string
	LastState       TaskState
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type StatusPublisher interface {
	PublishStatus(event LifecycleEvent) error
	PublishUpdate(task Task, message string) error
	// PublishThreadNotice posts visible text in the Slack thread (e.g. handoff). Does not clear the waiting reaction.
	PublishThreadNotice(task Task, text string) error
	PublishFinal(task Task, payload RenderPayload) error
	// ClearInboundReaction removes the "working" reaction from the triggering message (best-effort).
	ClearInboundReaction(task Task) error
}

type RenderPayload struct {
	OutputID     string
	FallbackText string
	FinalSummary string
	Transport    string
	// BlockKit, when non-empty, is posted as Slack blocks (with FallbackText as notification text)
	// instead of slackrender.AgentReplyBlocks(FallbackText).
	BlockKit []slack.Block
	// TermsSkillPending, when set, enables employee-factory–style Redis skill_confirmation for terms_accept
	// once the Slack final message posts successfully (see PublishFinal).
	TermsSkillPending *TermsSkillPendingAnchor
}

type TaskStore interface {
	SaveTask(task Task) error
	GetTask(taskID string) (Task, bool)
}

type TraceStore interface {
	AppendTrace(taskID string, entry TraceEntry) error
	ListTrace(taskID string) []TraceEntry
}
