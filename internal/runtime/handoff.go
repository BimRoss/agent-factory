package runtime

import "fmt"

type HandoffRequest struct {
	Task            Task
	FromEmployeeID  string
	ToEmployeeID    string
	Reason          string
	RequiredSkillID string
}

type HandoffResult struct {
	Accepted bool
	Reason   string
}

type HandoffBus interface {
	RequestHandoff(req HandoffRequest) (HandoffResult, error)
}

func TransferOwnership(task Task, req HandoffRequest, bus HandoffBus, publisher StatusPublisher) (Task, error) {
	if req.FromEmployeeID == "" || req.ToEmployeeID == "" {
		return task, fmt.Errorf("handoff requires from/to employee ids")
	}
	if req.FromEmployeeID == req.ToEmployeeID {
		return task, fmt.Errorf("handoff source and destination must differ")
	}

	if err := publisher.PublishUpdate(task, fmt.Sprintf("Transferring to %s...", req.ToEmployeeID)); err != nil {
		return task, err
	}

	result, err := bus.RequestHandoff(req)
	if err != nil {
		_ = publisher.ClearInboundReaction(task)
		return task, err
	}
	if !result.Accepted {
		_ = publisher.ClearInboundReaction(task)
		return task, fmt.Errorf("handoff rejected: %s", result.Reason)
	}

	_ = publisher.ClearInboundReaction(task)
	task.OwnerEmployeeID = req.ToEmployeeID
	return task, nil
}
