package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// GitHubEnvConfig holds repo + auth for GitHub REST calls (create-issue, etc.).
// Token resolution: EMPLOYEE_GITHUB_TOKEN, then
// EMPLOYEE_ORG_GH_TOKEN (org-wide PAT, preferred over personal), then EMPLOYEE_PERSONAL_GH_TOKEN,
// then global GITHUB_TOKEN, ORG_GH_TOKEN, PERSONAL_GH_TOKEN.
type GitHubEnvConfig struct {
	Token      string
	Owner      string
	OwnerScope string // org or user
	Repo       string // repository name only (not owner/repo)
}

type GitHubAccessProbe struct {
	EmployeeID      string
	TokenConfigured bool
	TokenScopes     string
	TokenTypeHint   string
	Owner           string
	Scope           string
	ListScopeOK     bool
	Warning         string
	Error           string
}

func (c GitHubEnvConfig) FullRepo() string {
	owner := strings.TrimSpace(c.Owner)
	repo := strings.TrimSpace(c.Repo)
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

func LoadGitHubConfigForEmployee(employeeID string) GitHubEnvConfig {
	emp := strings.TrimSpace(strings.ToLower(employeeID))
	prefix := strings.ToUpper(emp) + "_"

	token := firstNonEmpty(
		os.Getenv(prefix+"GITHUB_TOKEN"),
		os.Getenv(prefix+"ORG_GH_TOKEN"),
		os.Getenv(prefix+"PERSONAL_GH_TOKEN"),
		os.Getenv("GITHUB_TOKEN"),
		os.Getenv("ORG_GH_TOKEN"),
		os.Getenv("PERSONAL_GH_TOKEN"),
	)
	owner := firstNonEmpty(
		os.Getenv(prefix+"GITHUB_OWNER"),
		os.Getenv("GITHUB_OWNER"),
	)
	repo := firstNonEmpty(
		os.Getenv(prefix+"GITHUB_REPO"),
		os.Getenv("GITHUB_REPO"),
	)
	ownerScope := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		os.Getenv(prefix+"GITHUB_OWNER_SCOPE"),
		os.Getenv("GITHUB_OWNER_SCOPE"),
	)))
	switch ownerScope {
	case "user", "org":
	default:
		ownerScope = ""
	}
	return GitHubEnvConfig{
		Token:      strings.TrimSpace(token),
		Owner:      strings.TrimSpace(owner),
		OwnerScope: ownerScope,
		Repo:       strings.TrimSpace(repo),
	}
}

type githubOwnerUser struct {
	Login string `json:"login"`
}

type githubOwnerOrg struct {
	Login string `json:"login"`
}

func ResolveGitHubOwner(ctx context.Context, cfg GitHubEnvConfig) string {
	if owner := strings.TrimSpace(cfg.Owner); owner != "" {
		return owner
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return ""
	}

	var orgs []githubOwnerOrg
	if err := githubGETJSON(ctx, token, "/user/orgs?per_page=100", &orgs); err == nil {
		if owner := pickPreferredOrgOwner(orgs); owner != "" {
			return owner
		}
	}

	var user githubOwnerUser
	if err := githubGETJSON(ctx, token, "/user", &user); err == nil {
		return strings.TrimSpace(user.Login)
	}
	return ""
}

// InferGitHubOwnerScope returns "user" when owner matches the authenticated token's login, else "org"
// (for GitHub Search repo qualifiers: user:login vs org:orgname). On error, defaults to "org".
func InferGitHubOwnerScope(ctx context.Context, token, owner string) string {
	owner = strings.TrimSpace(owner)
	if owner == "" || strings.TrimSpace(token) == "" {
		return "org"
	}
	var user githubOwnerUser
	if err := githubGETJSON(ctx, token, "/user", &user); err != nil {
		return "org"
	}
	if strings.EqualFold(strings.TrimSpace(user.Login), owner) {
		return "user"
	}
	return "org"
}

func ResolveGitHubOwnerWithScope(ctx context.Context, cfg GitHubEnvConfig) (owner, scope string) {
	if o := strings.TrimSpace(cfg.Owner); o != "" {
		switch strings.ToLower(strings.TrimSpace(cfg.OwnerScope)) {
		case "user", "org":
			return o, strings.ToLower(strings.TrimSpace(cfg.OwnerScope))
		default:
			return o, InferGitHubOwnerScope(ctx, cfg.Token, o)
		}
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return "", ""
	}

	var orgs []githubOwnerOrg
	if err := githubGETJSON(ctx, token, "/user/orgs?per_page=100", &orgs); err == nil {
		if owner := pickPreferredOrgOwner(orgs); owner != "" {
			return owner, "org"
		}
	}

	var user githubOwnerUser
	if err := githubGETJSON(ctx, token, "/user", &user); err == nil {
		if login := strings.TrimSpace(user.Login); login != "" {
			return login, "user"
		}
	}
	return "", ""
}

func pickPreferredOrgOwner(orgs []githubOwnerOrg) string {
	if len(orgs) == 0 {
		return ""
	}
	for _, org := range orgs {
		login := strings.TrimSpace(org.Login)
		if strings.EqualFold(login, "bimross") {
			return login
		}
	}
	return strings.TrimSpace(orgs[0].Login)
}

func ProbeGitHubAccess(ctx context.Context, employeeID string) GitHubAccessProbe {
	probe := GitHubAccessProbe{
		EmployeeID: normalizeID(employeeID),
	}
	cfg := LoadGitHubConfigForEmployee(employeeID)
	if strings.TrimSpace(cfg.Token) == "" {
		probe.Warning = "github token not configured"
		return probe
	}
	probe.TokenConfigured = true
	if headerRes, err := githubGET(ctx, cfg.Token, "/user"); err == nil {
		probe.TokenScopes = strings.TrimSpace(headerRes.Header.Get("X-OAuth-Scopes"))
	} else if apiErr, ok := err.(*githubAPIError); ok {
		probe.TokenScopes = strings.TrimSpace(apiErr.OAuthScopes)
	}
	if strings.TrimSpace(probe.TokenScopes) == "" {
		probe.TokenTypeHint = "fine-grained-pat-or-app-token"
	} else {
		probe.TokenTypeHint = "classic-pat-or-oauth"
	}
	owner, scope := ResolveGitHubOwnerWithScope(ctx, cfg)
	probe.Owner = strings.TrimSpace(owner)
	probe.Scope = strings.TrimSpace(scope)
	if probe.Owner == "" || probe.Scope == "" {
		probe.Warning = "unable to resolve github owner from token"
		return probe
	}

	endpoint, scopeLabel := githubScopeProbeEndpoint(probe.Owner, probe.Scope)
	if _, err := githubGETRaw(ctx, cfg.Token, endpoint); err != nil {
		probe.Error = err.Error()
		probe.Warning = fmt.Sprintf("unable to list %s repositories with current token", scopeLabel)
		return probe
	}
	probe.ListScopeOK = true
	return probe
}

func githubScopeProbeEndpoint(owner, scope string) (endpoint, scopeLabel string) {
	if strings.EqualFold(strings.TrimSpace(scope), "user") {
		return "/users/" + strings.TrimSpace(owner) + "/repos?per_page=1&type=owner", "user"
	}
	return "/orgs/" + strings.TrimSpace(owner) + "/repos?per_page=1&type=all", "org"
}
