package runtime

import (
	"fmt"
	"strings"
)

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

	// Thread posts use task.OwnerEmployeeID to pick Slack bot token — publish only after ownership transfers.
	if err := publisher.PublishThreadNotice(task, receiverTakingNotice(req)); err != nil {
		return task, err
	}
	if err := publisher.PublishUpdate(task, fmt.Sprintf("Executing capability as %s…", req.ToEmployeeID)); err != nil {
		return task, err
	}
	return task, nil
}

func receiverTakingNotice(req HandoffRequest) string {
	skill := strings.TrimSpace(req.RequiredSkillID)
	if skill == "" {
		return "On it."
	}
	return fmt.Sprintf("On it — running `%s`.", skill)
}
