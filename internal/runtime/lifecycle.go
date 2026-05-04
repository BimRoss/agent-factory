package runtime

import "time"

type TaskState string

const (
	StateReceived        TaskState = "received"
	StatePlanning        TaskState = "planning"
	StateRunning         TaskState = "running"
	StateWaitingHandoff  TaskState = "waiting_handoff"
	StateHandoffAccepted TaskState = "handoff_accepted"
	StateFinalizing      TaskState = "finalizing"
	StateCompleted       TaskState = "completed"
	StateFailed          TaskState = "failed"
	StateCancelled       TaskState = "cancelled"
)

type LifecycleEvent struct {
	TaskID           string
	ThreadAnchor     string
	TraceID          string
	EmployeeID       string
	StateFrom        TaskState
	StateTo          TaskState
	TransitionReason string
	Timestamp        time.Time
}

func (e LifecycleEvent) IsTerminal() bool {
	return e.StateTo == StateCompleted || e.StateTo == StateFailed || e.StateTo == StateCancelled
}
