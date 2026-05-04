package runtime

import "time"

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
	PublishFinal(task Task, payload RenderPayload) error
	// ClearInboundReaction removes the "working" reaction from the triggering message (best-effort).
	ClearInboundReaction(task Task) error
}

type RenderPayload struct {
	OutputID     string
	FallbackText string
	FinalSummary string
	Transport    string
}

type TaskStore interface {
	SaveTask(task Task) error
	GetTask(taskID string) (Task, bool)
}

type TraceStore interface {
	AppendTrace(taskID string, entry TraceEntry) error
	ListTrace(taskID string) []TraceEntry
}
