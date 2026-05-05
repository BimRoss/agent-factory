package runtime

import "fmt"

type createGitHubRepoAdapter struct{}

func (createGitHubRepoAdapter) CapabilityID() string {
	return "create-github-repo"
}

func (createGitHubRepoAdapter) BuildPlan(task Task) CapabilityExecutionResult {
	return CapabilityExecutionResult{
		ProgressUpdates: nil,
		FinalPayload: RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-github-repo", task.ID),
			FallbackText: "",
			FinalSummary: "create-github-repo",
			Transport:    "slack",
		},
	}
}
