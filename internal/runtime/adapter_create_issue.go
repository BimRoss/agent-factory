package runtime

import "fmt"

type createIssueAdapter struct{}

func (createIssueAdapter) CapabilityID() string {
	return "create-issue"
}

func (createIssueAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		ProgressUpdates: []string{
			"Collecting issue details and validating required fields...",
			"Preparing issue draft payload for confirmation flow...",
		},
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-issue", task.ID),
			FallbackText: "Issue draft prepared and ready for confirm/cancel flow.",
			FinalSummary: "create-issue prepared",
			Transport:    "slack",
		},
	}
}
