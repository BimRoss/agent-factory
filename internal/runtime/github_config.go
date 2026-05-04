package runtime

import (
	"os"
	"strings"
)

// GitHubEnvConfig holds repo + auth for GitHub REST calls (create-issue, etc.).
// Resolution matches employee-factory: EMPLOYEE_GITHUB_* then global GITHUB_*.
type GitHubEnvConfig struct {
	Token string
	Owner string
	Repo  string // repository name only (not owner/repo)
}

func (c GitHubEnvConfig) FullRepo() string {
	owner := strings.TrimSpace(c.Owner)
	repo := strings.TrimSpace(c.Repo)
	if owner == "" {
		owner = "BimRoss"
	}
	if repo == "" {
		repo = "create-issue"
	}
	return owner + "/" + repo
}

func LoadGitHubConfigForEmployee(employeeID string) GitHubEnvConfig {
	emp := strings.TrimSpace(strings.ToLower(employeeID))
	prefix := strings.ToUpper(emp) + "_"

	token := firstNonEmpty(
		os.Getenv(prefix+"GITHUB_TOKEN"),
		os.Getenv("GITHUB_TOKEN"),
	)
	owner := firstNonEmpty(
		os.Getenv(prefix+"GITHUB_OWNER"),
		os.Getenv("GITHUB_OWNER"),
	)
	repo := firstNonEmpty(
		os.Getenv(prefix+"GITHUB_REPO"),
		os.Getenv("GITHUB_REPO"),
	)
	return GitHubEnvConfig{
		Token: strings.TrimSpace(token),
		Owner: strings.TrimSpace(owner),
		Repo:  strings.TrimSpace(repo),
	}
}
