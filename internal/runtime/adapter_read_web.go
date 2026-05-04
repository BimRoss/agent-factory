package runtime

import "fmt"

type readWebAdapter struct{}

func (readWebAdapter) CapabilityID() string {
	return "read-web"
}

func (readWebAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		ProgressUpdates: []string{
			"Running Gemini research workflow...",
			"Collecting and summarizing web findings...",
		},
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-web", task.ID),
			FallbackText: "Research summary prepared via Gemini.",
			FinalSummary: "read-web research completed",
			Transport:    "slack",
		},
	}
}
