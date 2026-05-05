package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	reQuotedRepoSlug     = regexp.MustCompile("(?i)[\"'`]([a-zA-Z0-9][a-zA-Z0-9._-]{0,98})[\"'`]")
	reCalledRepoSlug     = regexp.MustCompile(`(?i)\b(?:called|named)\s+([a-zA-Z0-9][a-zA-Z0-9._-]{0,98})\b`)
	reGithubRepoSlugOnly = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,98}$`)
)

type githubRepoCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private"`
}

type githubRepoCreateAPIResponse struct {
	HTMLURL  string `json:"html_url"`
	FullName string `json:"full_name"`
	Name     string `json:"name"`
}

func (e *Engine) runCreateGitHubRepo(ctx context.Context, task Task) (RenderPayload, error) {
	cfg := LoadGitHubConfigForEmployee(task.OwnerEmployeeID)
	if cfg.Token == "" {
		return RenderPayload{}, fmt.Errorf("create-github-repo: set %s_GITHUB_TOKEN, %s_PERSONAL_GH_TOKEN, GITHUB_TOKEN, or PERSONAL_GH_TOKEN", strings.ToUpper(strings.TrimSpace(task.OwnerEmployeeID)), strings.ToUpper(strings.TrimSpace(task.OwnerEmployeeID)))
	}
	owner, scope := ResolveGitHubOwnerWithScope(ctx, cfg)
	if ownerFromField := strings.TrimSpace(extractNamedField(task.RequestText, "owner")); ownerFromField != "" {
		owner = ownerFromField
		scope = InferGitHubOwnerScope(ctx, cfg.Token, owner)
	}

	login := strings.TrimSpace(gitHubAuthenticateLogin(ctx, cfg.Token))
	if login == "" {
		return RenderPayload{}, fmt.Errorf("create-github-repo: unable to resolve GitHub user login from token")
	}

	name := strings.TrimSpace(extractNamedField(task.RequestText, "name"))
	if name == "" {
		name = inferRepoNameFragment(task.RequestText)
	}
	if !reGithubRepoSlugOnly.MatchString(name) {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-create-github-repo", task.ID),
			FallbackText: "I need a valid GitHub repository *name* (letters, digits, `.`, `_`, hyphen). Send something like `name: my-repo` or put the slug in quotes.",
			FinalSummary: "create-github-repo awaiting name",
			Transport:    "slack",
		}, nil
	}

	description := strings.TrimSpace(extractNamedField(task.RequestText, "description"))
	private, privacySet := parsePrivateFlag(extractNamedField(task.RequestText, "private"))
	if !privacySet {
		private = true
	}

	var endpoint string
	if strings.EqualFold(scope, "user") && strings.EqualFold(owner, login) {
		endpoint = "/user/repos"
	} else {
		org := strings.TrimSpace(owner)
		if org == "" {
			return RenderPayload{}, fmt.Errorf("create-github-repo: set GITHUB_OWNER (org or user) when creating outside your personal login")
		}
		endpoint = "/orgs/" + url.PathEscape(org) + "/repos"
	}

	payload := githubRepoCreatePayload{
		Name:        name,
		Description: description,
		Private:     private,
	}
	rawJSON, err := json.Marshal(payload)
	if err != nil {
		return RenderPayload{}, fmt.Errorf("create-github-repo: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com"+endpoint, bytes.NewReader(rawJSON))
	if err != nil {
		return RenderPayload{}, fmt.Errorf("create-github-repo: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.Token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return RenderPayload{}, fmt.Errorf("create-github-repo: http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RenderPayload{}, fmt.Errorf("create-github-repo: github %d: %s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(respBody)), 280))
	}
	var out githubRepoCreateAPIResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return RenderPayload{}, fmt.Errorf("create-github-repo: decode: %w", err)
	}
	urlDone := strings.TrimSpace(out.HTMLURL)
	if urlDone == "" {
		urlDone = "https://github.com/" + strings.TrimSpace(out.FullName)
	}
	sum := truncateRunes(urlDone, 120)
	txt := fmt.Sprintf("Created GitHub repository *%s* — %s", strings.TrimSpace(out.FullName), sum)
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-create-github-repo", task.ID),
		FallbackText: txt,
		FinalSummary: "create-github-repo completed",
		Transport:    "slack",
	}, nil
}

func gitHubAuthenticateLogin(ctx context.Context, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := githubGETJSON(ctx, token, "/user", &user); err != nil {
		return ""
	}
	return strings.TrimSpace(user.Login)
}

func inferRepoNameFragment(raw string) string {
	plain := strings.TrimSpace(raw)
	if plain == "" {
		return ""
	}
	if m := reQuotedRepoSlug.FindStringSubmatch(plain); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	if m := reCalledRepoSlug.FindStringSubmatch(plain); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	// Strip hyphenated skill echoes and Slack mentions — keep slug-like trailing token(s).
	toLower := regexp.MustCompile(`(?i)<@[A-Z0-9]+>`)
	line := toLower.ReplaceAllString(plain, " ")
	stop := regexp.MustCompile(`(?i)\b(?:create-github-repo|create\s+repo|github\s+repo|new\s+github\s+repo|initialize-github-repo)\b`)
	line = stop.ReplaceAllString(line, " ")
	line = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(line), " ")
	if line == "" {
		return ""
	}
	toks := strings.Fields(line)
	for i := len(toks) - 1; i >= 0; i-- {
		cand := strings.Trim(toks[i], ".,!?;:\"'`")
		if reGithubRepoSlugOnly.MatchString(cand) && len(cand) > 2 {
			return cand
		}
	}
	return ""
}

// parsePrivateFlag parses yes/no/true/false/1/0/public/private. Second return is whether the caller provided a preference.
func parsePrivateFlag(raw string) (private bool, set bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return false, false
	}
	switch raw {
	case "yes", "y", "true", "1", "on", "private":
		return true, true
	case "no", "n", "false", "0", "off", "public":
		return false, true
	default:
		return false, false
	}
}
