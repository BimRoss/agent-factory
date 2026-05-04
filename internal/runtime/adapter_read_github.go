package runtime

import "fmt"

type readGitHubAdapter struct{}

func (readGitHubAdapter) CapabilityID() string {
	return "read-github"
}

func (readGitHubAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		ProgressUpdates: []string{
			"Searching GitHub and collecting results...",
		},
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github", task.ID),
			FallbackText: "GitHub lookup complete.",
			FinalSummary: "read-github completed",
			Transport:    "slack",
		},
	}
}
