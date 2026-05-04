package runtime

import "fmt"

type createIssueAdapter struct{}

func (createIssueAdapter) CapabilityID() string {
	return "create-issue"
}

func (createIssueAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		// First progress + hourglass reaction is sent in engine.go before runCreateIssue.
		ProgressUpdates: nil,
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-issue", task.ID),
			FallbackText: "",
			FinalSummary: "create-issue",
			Transport:    "slack",
		},
	}
}
