package runtime

import "fmt"

type createDocAdapter struct{}

func (createDocAdapter) CapabilityID() string {
	return "create-google-doc"
}

func (createDocAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		ProgressUpdates: []string{
			"Drafting document content...",
			"Creating Google Doc and applying sharing...",
		},
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-google-doc", task.ID),
			FallbackText: "",
			FinalSummary: "create-google-doc",
			Transport:    "slack",
		},
	}
}
