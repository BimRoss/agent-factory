package runtime

import "fmt"

type createCompanyAdapter struct{}

func (createCompanyAdapter) CapabilityID() string {
	return "create-company"
}

func (createCompanyAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		// Progress lives in engine.ExecuteCapability (PublishUpdate + runCreateCompany).
		ProgressUpdates: nil,
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-company", task.ID),
			FallbackText: "",
			FinalSummary: "create-company",
			Transport:    "slack",
		},
	}
}
