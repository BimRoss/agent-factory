package runtime

import "fmt"

type createCompanyAdapter struct{}

func (createCompanyAdapter) CapabilityID() string {
	return "create-company"
}

func (createCompanyAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		ProgressUpdates: []string{
			"Collecting company slug and founder context...",
			"Preparing company channel provisioning request...",
		},
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-company", task.ID),
			FallbackText: "Company creation request prepared and ready for confirm/cancel flow.",
			FinalSummary: "create-company prepared",
			Transport:    "slack",
		},
	}
}
