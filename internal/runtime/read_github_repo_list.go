package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type githubRepoListItem struct {
	Name            string   `json:"name"`
	FullName        string   `json:"full_name"`
	HTMLURL         string   `json:"html_url"`
	Description     string   `json:"description"`
	Private         bool     `json:"private"`
	StargazersCount int      `json:"stargazers_count"`
	Language        string   `json:"language"`
	DefaultBranch   string   `json:"default_branch"`
	Topics          []string `json:"topics"`
}

// githubRepoInventoryUsesListAPI means the qualifier string is inventory-only (scoped
// by org/user/repo tokens only) so we use list endpoints—not GET /search/repositories
// (fine-grained PATs often 422 on user:<login> search).
func githubRepoInventoryUsesListAPI(rawQuery string) bool {
	q := strings.TrimSpace(rawQuery)
	if q == "" {
		return true
	}
	lq := strings.ToLower(q)
	if strings.Contains(lq, "stars:") ||
		strings.Contains(lq, "topic:") ||
		strings.Contains(lq, "language:") ||
		strings.Contains(lq, "fork:") ||
		strings.Contains(lq, "archived:") ||
		strings.Contains(lq, "mirror:") {
		return false
	}
	for _, tok := range strings.Fields(q) {
		t := strings.TrimSpace(tok)
		if t == "" {
			continue
		}
		lt := strings.ToLower(t)
		if strings.HasPrefix(lt, "repo:") {
			return false
		}
		if strings.HasPrefix(lt, "org:") || strings.HasPrefix(lt, "user:") {
			continue
		}
		return false
	}
	return true
}

func extractSearchQualifierFold(q, prefix string) string {
	pl := strings.ToLower(prefix)
	for _, tok := range strings.Fields(q) {
		t := strings.TrimSpace(tok)
		if t == "" {
			continue
		}
		lt := strings.ToLower(t)
		if strings.HasPrefix(lt, pl) && len(t) >= len(pl) {
			return strings.TrimSpace(t[len(pl):])
		}
	}
	return ""
}

func githubAuthenticatedLogin(ctx context.Context, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	var user githubOwnerUser
	if err := githubGETJSON(ctx, token, "/user", &user); err != nil {
		return ""
	}
	return strings.TrimSpace(user.Login)
}

func githubRepoListEndpointForInventory(ctx context.Context, token string, cfg GitHubEnvConfig, req readGitHubRequest, inventoryQuery string) (endpointPath string, summaryLabel string, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", fmt.Errorf("missing GitHub token")
	}
	perPage := clampRepoListPerPage(req.PerPage)

	orgFromReq := strings.TrimSpace(req.Org)
	orgFromQuery := extractSearchQualifierFold(inventoryQuery, "org:")
	orgName := firstNonEmpty(orgFromReq, orgFromQuery)
	if orgName != "" {
		ep := fmt.Sprintf("/orgs/%s/repos?per_page=%d&type=all&sort=updated",
			url.PathEscape(strings.TrimSpace(orgName)), perPage)
		return ep, fmt.Sprintf("org `%s`", orgName), nil
	}

	selfLogin := githubAuthenticatedLogin(ctx, token)
	userQual := extractSearchQualifierFold(inventoryQuery, "user:")
	if userQual != "" && selfLogin != "" {
		if strings.EqualFold(strings.TrimSpace(userQual), selfLogin) {
			ep := fmt.Sprintf("/user/repos?per_page=%d&sort=updated&affiliation=%s",
				perPage, url.QueryEscape("owner,collaborator,organization_member"))
			return ep, fmt.Sprintf("authenticated user `%s`", selfLogin), nil
		}
		owner := strings.TrimSpace(userQual)
		ep := fmt.Sprintf("/users/%s/repos?per_page=%d&type=owner&sort=updated",
			url.PathEscape(owner), perPage)
		return ep, fmt.Sprintf("user `%s`", owner), nil
	}

	if strings.EqualFold(strings.TrimSpace(cfg.OwnerScope), "org") && strings.TrimSpace(cfg.Owner) != "" {
		o := strings.TrimSpace(cfg.Owner)
		ep := fmt.Sprintf("/orgs/%s/repos?per_page=%d&type=all&sort=updated",
			url.PathEscape(o), perPage)
		return ep, fmt.Sprintf("org `%s`", o), nil
	}

	ep := fmt.Sprintf("/user/repos?per_page=%d&sort=updated&affiliation=%s",
		perPage, url.QueryEscape("owner,collaborator,organization_member"))
	if selfLogin != "" {
		return ep, fmt.Sprintf("authenticated user `%s`", selfLogin), nil
	}
	return ep, "your accessible GitHub repos", nil
}

func clampRepoListPerPage(n int) int {
	if n <= 0 {
		return 30
	}
	if n > 100 {
		return 100
	}
	return n
}

func runGitHubRepoList(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest, inventoryQuery string) (RenderPayload, error) {
	endpointFirst, summaryLabel, err := githubRepoListEndpointForInventory(ctx, cfg.Token, cfg, req, inventoryQuery)
	if err != nil {
		return RenderPayload{}, fmt.Errorf("read-github repo list: %w", err)
	}

	if req.CountOnly {
		total, err := countReposPaginated(ctx, cfg.Token, endpointFirst)
		if err != nil {
			return RenderPayload{}, fmt.Errorf("read-github repo list count: %w", err)
		}
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github-repos", task.ID),
			FallbackText: fmt.Sprintf("I can see **%d** repositories for %s (via GitHub list API).", total, summaryLabel),
			FinalSummary: "read-github repo count completed",
			Transport:    "slack",
		}, nil
	}

	res, err := githubGET(ctx, cfg.Token, endpointFirst)
	if err != nil {
		return RenderPayload{}, fmt.Errorf("read-github repo list: %w", err)
	}
	var items []githubRepoListItem
	if err := json.Unmarshal(res.Body, &items); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github repo list: decode: %w", err)
	}
	if len(items) == 0 {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github", task.ID),
			FallbackText: fmt.Sprintf("No repositories returned for %s.", summaryLabel),
			FinalSummary: "read-github repo list returned no entries",
			Transport:    "slack",
		}, nil
	}

	displayCap := sanitizePerPage(req.PerPage)
	lines := make([]string, 0, displayCap+2)
	qShow := strings.TrimSpace(inventoryQuery)
	if qShow == "" {
		qShow = "(inventory)"
	}
	lines = append(lines, fmt.Sprintf("GitHub repositories for %s (`%s`):", summaryLabel, qShow))
	for i, item := range items {
		if i >= displayCap {
			break
		}
		desc := strings.TrimSpace(item.Description)
		if desc == "" {
			desc = "No description."
		}
		if len([]rune(desc)) > 140 {
			desc = truncateRunes(desc, 140) + "..."
		}
		lang := strings.TrimSpace(item.Language)
		if lang == "" {
			lang = "n/a"
		}
		priv := ""
		if item.Private {
			priv = " | private"
		}
		topicText := ""
		if len(item.Topics) > 0 {
			topics := item.Topics
			if len(topics) > 3 {
				topics = topics[:3]
			}
			topicText = " | topics: " + strings.Join(topics, ", ")
		}
		lines = append(lines, fmt.Sprintf("- %s (%s) | stars: %d | default: %s | lang: %s%s%s\n  %s",
			strings.TrimSpace(item.FullName),
			strings.TrimSpace(item.HTMLURL),
			item.StargazersCount,
			strings.TrimSpace(item.DefaultBranch),
			lang,
			priv,
			topicText,
			desc))
	}

	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github", task.ID),
		FallbackText: strings.Join(lines, "\n"),
		FinalSummary: "read-github repo list completed",
		Transport:    "slack",
	}, nil
}

func githubAPIRelativePath(fullURL string) string {
	fullURL = strings.TrimSpace(fullURL)
	for _, pref := range []string{"https://api.github.com", "http://api.github.com"} {
		if strings.HasPrefix(strings.ToLower(fullURL), pref) {
			return fullURL[len(pref):]
		}
	}
	return ""
}

func countReposPaginated(ctx context.Context, token, endpointPathOrURL string) (int, error) {
	total := 0
	next := normalizeGitHubRequestPath(endpointPathOrURL)
	seen := map[string]struct{}{}
	maxPages := 50
	for p := 0; p < maxPages && next != ""; p++ {
		if _, dup := seen[next]; dup {
			break
		}
		seen[next] = struct{}{}

		res, err := githubGET(ctx, token, next)
		if err != nil {
			return total, err
		}
		var items []githubRepoListItem
		if err := json.Unmarshal(res.Body, &items); err != nil {
			return total, fmt.Errorf("decode: %w", err)
		}
		total += len(items)
		if len(items) == 0 {
			break
		}

		link := strings.TrimSpace(res.Header.Get("Link"))
		next = ""
		for _, chunk := range strings.Split(link, ",") {
			if strings.Contains(strings.ToLower(chunk), `rel="next"`) {
				s := strings.TrimSpace(strings.Split(chunk, ";")[0])
				s = strings.Trim(s, "<>")
				next = normalizeGitHubRequestPath(s)
				break
			}
		}
	}
	return total, nil
}

func normalizeGitHubRequestPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if rel := githubAPIRelativePath(s); rel != "" {
		if rel != "" && rel[0] != '/' {
			rel = "/" + rel
		}
		return rel
	}
	if s[0] != '/' {
		return "/" + s
	}
	return s
}

// buildReadGitHubRepoSearchQuery mirrors runGitHubRepoSearch / preflight qualifier assembly.
func buildReadGitHubRepoSearchQuery(req readGitHubRequest, cfg GitHubEnvConfig) string {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		if strings.TrimSpace(req.Org) != "" {
			query = "org:" + strings.TrimSpace(req.Org)
		} else if strings.TrimSpace(cfg.Owner) != "" {
			query = defaultGitHubOwnerScopePrefix(cfg.OwnerScope) + ":" + strings.TrimSpace(cfg.Owner)
		}
	}
	if req.Org != "" && !strings.Contains(strings.ToLower(query), "org:") {
		query += " org:" + req.Org
	}
	if req.Org == "" && strings.TrimSpace(cfg.Owner) != "" && !hasExplicitGitHubScopeQualifier(query) {
		query += " " + defaultGitHubOwnerScopePrefix(cfg.OwnerScope) + ":" + strings.TrimSpace(cfg.Owner)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		query = "stars:>0"
	}
	return query
}
