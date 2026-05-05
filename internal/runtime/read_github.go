package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	readGitHubModeRepoSearch = "repo_search"
	readGitHubModeRepoMeta   = "repo_meta"
	readGitHubModeCodeSearch = "code_search"
	readGitHubModeTree       = "tree"
	readGitHubModeFileGet    = "file_get"
	readGitHubModeCommits    = "commits"
	readGitHubModePRs        = "prs"
	readGitHubModeBranches   = "branches"
)

var (
	reGitHubBlobURL    = regexp.MustCompile(`https?://github\.com/([^/\s]+)/([^/\s]+)/blob/([^/\s]+)/([^\s?#]+)`)
	reOwnerRepoPath    = regexp.MustCompile(`\b([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.\-/]+)\b`)
	reSlackUserMention = regexp.MustCompile(`<@[A-Z0-9]+>`)
	reGitHubNoiseWords = regexp.MustCompile(`(?i)\b(can|could|would|should|please|you|your|our|my|the|a|an|to|for|at|about|look|check|scan|read|search|find|get|show|list|see|are|able|is|am|be|been|being|access|github|git|hub|repo|repos|repository|repositories|how|many|do|we|have|in|on|org|organization|count|what|know|tell|me|us)\b`)
	// Extra scaffolding stripped only for /search/code NL questions (not repo inventory).
	reGitHubCodeSearchNoise = regexp.MustCompile(`(?i)\b(references|reference|referring|there|here|stuff|things|thing|anywhere|somewhere|appear|appears|appeared|mentioned|mentions|uses|using|used|inside|within|across|throughout|related|regarding|concerning|mostly|especially|including|example|examples|anything|something|someone|somewhat|snippets|snippet|symbols|symbol|codebase|basically|actually|probably|perhaps|please|just|also|very|really|quite|some|such|like|similar)\b|\bcode\b`)
	reRepoSlugToken         = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9._-]*-[A-Za-z0-9._-]+)\b`)
)

var genericRepoInventoryQueryWords = map[string]struct{}{
	"all":   {},
	"any":   {},
	"those": {},
	"these": {},
	"them":  {},
}

type readGitHubRequest struct {
	Mode      string
	Query     string
	CountOnly bool
	Org       string
	Owner     string
	Repo      string
	Path      string
	Ref       string
	State     string
	PerPage   int
}

type githubRepoSearchResponse struct {
	TotalCount int `json:"total_count"`
	Items      []struct {
		FullName        string `json:"full_name"`
		HTMLURL         string `json:"html_url"`
		Description     string `json:"description"`
		StargazersCount int    `json:"stargazers_count"`
		Language        string `json:"language"`
		DefaultBranch   string `json:"default_branch"`
		Topics          []string
	} `json:"items"`
}

type githubCodeSearchResponse struct {
	Items []struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		HTMLURL string `json:"html_url"`
		Repo    struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"items"`
}

type githubContentsResponse struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int    `json:"size"`
	HTMLURL  string `json:"html_url"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type githubRepoMetaResponse struct {
	FullName        string `json:"full_name"`
	HTMLURL         string `json:"html_url"`
	Description     string `json:"description"`
	StargazersCount int    `json:"stargazers_count"`
	ForksCount      int    `json:"forks_count"`
	OpenIssuesCount int    `json:"open_issues_count"`
	DefaultBranch   string `json:"default_branch"`
	Language        string `json:"language"`
	Topics          []string
}

type githubTreeEntry struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int    `json:"size"`
	HTMLURL string `json:"html_url"`
}

type githubCommitResponse struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

type githubPRResponse struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Draft    bool    `json:"draft"`
	MergedAt *string `json:"merged_at"`
}

type githubBranchResponse struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
		URL string `json:"url"`
	} `json:"commit"`
}

func (e *Engine) runReadGitHubCapability(ctx context.Context, task Task, capabilityID string) (RenderPayload, error) {
	cfg := LoadGitHubConfigForEmployee(task.OwnerEmployeeID)
	if strings.TrimSpace(cfg.Token) == "" {
		return RenderPayload{}, fmt.Errorf("read-github: set %s_GITHUB_TOKEN, %s_PERSONAL_GH_TOKEN, GITHUB_TOKEN, or PERSONAL_GH_TOKEN", strings.ToUpper(strings.TrimSpace(task.OwnerEmployeeID)), strings.ToUpper(strings.TrimSpace(task.OwnerEmployeeID)))
	}
	cfg.Owner, cfg.OwnerScope = ResolveGitHubOwnerWithScope(ctx, cfg)

	req := parseReadGitHubRequest(task.RequestText, cfg, defaultModeForReadGitHubCapability(capabilityID))
	if err := preflightReadGitHubRequest(ctx, cfg, req); err != nil {
		return RenderPayload{}, err
	}
	switch req.Mode {
	case readGitHubModeRepoMeta:
		return runGitHubRepoMeta(ctx, cfg, task, req)
	case readGitHubModeCommits:
		return runGitHubCommits(ctx, cfg, task, req)
	case readGitHubModePRs:
		return runGitHubPRs(ctx, cfg, task, req)
	case readGitHubModeBranches:
		return runGitHubBranches(ctx, cfg, task, req)
	case readGitHubModeTree:
		return runGitHubTree(ctx, cfg, task, req)
	case readGitHubModeFileGet:
		return runGitHubFileGet(ctx, cfg, task, req)
	case readGitHubModeCodeSearch:
		return runGitHubCodeSearch(ctx, cfg, task, req)
	default:
		return runGitHubRepoSearch(ctx, cfg, task, req)
	}
}

func (e *Engine) runReadGitHub(ctx context.Context, task Task) (RenderPayload, error) {
	return e.runReadGitHubCapability(ctx, task, "read-github")
}

func runGitHubRepoSearch(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	query := buildReadGitHubRepoSearchQuery(req, cfg)
	if githubRepoInventoryUsesListAPI(query) {
		return runGitHubRepoList(ctx, cfg, task, req, query)
	}

	perPage := sanitizePerPage(req.PerPage)
	params := url.Values{}
	params.Set("q", query)
	params.Set("per_page", strconv.Itoa(perPage))

	var out githubRepoSearchResponse
	if err := githubGETJSON(ctx, cfg.Token, "/search/repositories?"+params.Encode(), &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github repo search: %w", err)
	}
	if req.CountOnly {
		scope := strings.TrimSpace(req.Org)
		if scope == "" {
			scope = strings.TrimSpace(cfg.Owner)
		}
		if scope == "" {
			scope = "requested scope"
		}
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github-repos", task.ID),
			FallbackText: fmt.Sprintf("I can see **%d** repositories in org `%s`.", out.TotalCount, scope),
			FinalSummary: "read-github repo count completed",
			Transport:    "slack",
		}, nil
	}
	if len(out.Items) == 0 {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github", task.ID),
			FallbackText: fmt.Sprintf("No repositories found for `%s`.", query),
			FinalSummary: "read-github repo search returned no matches",
			Transport:    "slack",
		}, nil
	}

	lines := make([]string, 0, len(out.Items)+2)
	lines = append(lines, fmt.Sprintf("GitHub repo results for `%s`:", query))
	for i, item := range out.Items {
		if i >= 6 {
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
		topicText := ""
		if len(item.Topics) > 0 {
			topics := item.Topics
			if len(topics) > 3 {
				topics = topics[:3]
			}
			topicText = " | topics: " + strings.Join(topics, ", ")
		}
		lines = append(lines, fmt.Sprintf("- %s (%s) | stars: %d | default: %s | lang: %s%s\n  %s", item.FullName, item.HTMLURL, item.StargazersCount, item.DefaultBranch, lang, topicText, desc))
	}

	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github", task.ID),
		FallbackText: strings.Join(lines, "\n"),
		FinalSummary: "read-github repo search completed",
		Transport:    "slack",
	}, nil
}

func runGitHubRepoMeta(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	if owner == "" || repo == "" {
		return RenderPayload{}, fmt.Errorf("read-github repo_meta requires owner and repo")
	}

	endpoint := fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	var out githubRepoMetaResponse
	if err := githubGETJSON(ctx, cfg.Token, endpoint, &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github repo meta: %w", err)
	}

	desc := strings.TrimSpace(out.Description)
	if desc == "" {
		desc = "No description."
	}
	lang := strings.TrimSpace(out.Language)
	if lang == "" {
		lang = "n/a"
	}
	topicText := "none"
	if len(out.Topics) > 0 {
		topicText = strings.Join(out.Topics, ", ")
	}

	text := fmt.Sprintf(
		"GitHub repo meta for `%s/%s`:\n- URL: %s\n- Description: %s\n- Default branch: %s\n- Language: %s\n- Stars: %d | Forks: %d | Open issues: %d\n- Topics: %s",
		owner,
		repo,
		strings.TrimSpace(out.HTMLURL),
		desc,
		strings.TrimSpace(out.DefaultBranch),
		lang,
		out.StargazersCount,
		out.ForksCount,
		out.OpenIssuesCount,
		topicText,
	)
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github-repo-meta", task.ID),
		FallbackText: text,
		FinalSummary: "read-github repo metadata completed",
		Transport:    "slack",
	}, nil
}

func runGitHubCodeSearch(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return RenderPayload{}, fmt.Errorf("read-github code search needs a query (use `query:`)")
	}
	if req.Org != "" && !strings.Contains(query, "org:") {
		query += " org:" + req.Org
	}
	if req.Owner != "" && req.Repo != "" && !strings.Contains(query, "repo:") {
		query += " repo:" + req.Owner + "/" + req.Repo
	}

	perPage := sanitizePerPage(req.PerPage)
	params := url.Values{}
	params.Set("q", query)
	params.Set("per_page", strconv.Itoa(perPage))

	var out githubCodeSearchResponse
	if err := githubGETJSON(ctx, cfg.Token, "/search/code?"+params.Encode(), &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github code search: %w", err)
	}
	if len(out.Items) == 0 {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github", task.ID),
			FallbackText: fmt.Sprintf("No code matches found for `%s`.", query),
			FinalSummary: "read-github code search returned no matches",
			Transport:    "slack",
		}, nil
	}

	lines := make([]string, 0, len(out.Items)+2)
	lines = append(lines, fmt.Sprintf("GitHub code results for `%s`:", query))
	for i, item := range out.Items {
		if i >= 8 {
			break
		}
		lines = append(lines, fmt.Sprintf("- %s/%s (%s)", item.Repo.FullName, item.Path, item.HTMLURL))
	}
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github", task.ID),
		FallbackText: strings.Join(lines, "\n"),
		FinalSummary: "read-github code search completed",
		Transport:    "slack",
	}, nil
}

func runGitHubCommits(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	if owner == "" || repo == "" {
		return RenderPayload{}, fmt.Errorf("read-github commits requires owner and repo")
	}
	params := url.Values{}
	params.Set("per_page", strconv.Itoa(sanitizePerPage(req.PerPage)))
	if strings.TrimSpace(req.Ref) != "" {
		params.Set("sha", strings.TrimSpace(req.Ref))
	}
	endpoint := fmt.Sprintf("/repos/%s/%s/commits?%s", url.PathEscape(owner), url.PathEscape(repo), params.Encode())

	var out []githubCommitResponse
	if err := githubGETJSON(ctx, cfg.Token, endpoint, &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github commits: %w", err)
	}
	if len(out) == 0 {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github-commits", task.ID),
			FallbackText: fmt.Sprintf("No commits found for `%s/%s`.", owner, repo),
			FinalSummary: "read-github commits returned no entries",
			Transport:    "slack",
		}, nil
	}

	lines := []string{fmt.Sprintf("Recent commits for `%s/%s`:", owner, repo)}
	for i, item := range out {
		if i >= 8 {
			break
		}
		msg := strings.TrimSpace(item.Commit.Message)
		msg = strings.Split(msg, "\n")[0]
		if len([]rune(msg)) > 110 {
			msg = truncateRunes(msg, 110) + "..."
		}
		sha := strings.TrimSpace(item.SHA)
		if len(sha) > 8 {
			sha = sha[:8]
		}
		author := strings.TrimSpace(item.Commit.Author.Name)
		if author == "" {
			author = "unknown"
		}
		lines = append(lines, fmt.Sprintf("- `%s` by %s: %s (%s)", sha, author, msg, strings.TrimSpace(item.HTMLURL)))
	}
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github-commits", task.ID),
		FallbackText: strings.Join(lines, "\n"),
		FinalSummary: "read-github commits completed",
		Transport:    "slack",
	}, nil
}

func runGitHubPRs(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	if owner == "" || repo == "" {
		return RenderPayload{}, fmt.Errorf("read-github prs requires owner and repo")
	}
	state := strings.ToLower(strings.TrimSpace(req.State))
	if state == "" {
		state = "open"
	}
	if state != "open" && state != "closed" && state != "all" {
		state = "open"
	}

	params := url.Values{}
	params.Set("state", state)
	params.Set("per_page", strconv.Itoa(sanitizePerPage(req.PerPage)))
	endpoint := fmt.Sprintf("/repos/%s/%s/pulls?%s", url.PathEscape(owner), url.PathEscape(repo), params.Encode())

	var out []githubPRResponse
	if err := githubGETJSON(ctx, cfg.Token, endpoint, &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github prs: %w", err)
	}
	if len(out) == 0 {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github-prs", task.ID),
			FallbackText: fmt.Sprintf("No pull requests found for `%s/%s` (state=%s).", owner, repo, state),
			FinalSummary: "read-github prs returned no entries",
			Transport:    "slack",
		}, nil
	}

	lines := []string{fmt.Sprintf("Pull requests for `%s/%s` (state=%s):", owner, repo, state)}
	for i, item := range out {
		if i >= 8 {
			break
		}
		title := strings.TrimSpace(item.Title)
		if len([]rune(title)) > 110 {
			title = truncateRunes(title, 110) + "..."
		}
		draft := ""
		if item.Draft {
			draft = " [draft]"
		}
		lines = append(lines, fmt.Sprintf("- #%d%s %s by %s (%s)", item.Number, draft, title, strings.TrimSpace(item.User.Login), strings.TrimSpace(item.HTMLURL)))
	}
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github-prs", task.ID),
		FallbackText: strings.Join(lines, "\n"),
		FinalSummary: "read-github prs completed",
		Transport:    "slack",
	}, nil
}

func runGitHubBranches(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	if owner == "" || repo == "" {
		return RenderPayload{}, fmt.Errorf("read-github branches requires owner and repo")
	}
	params := url.Values{}
	params.Set("per_page", strconv.Itoa(sanitizePerPage(req.PerPage)))
	endpoint := fmt.Sprintf("/repos/%s/%s/branches?%s", url.PathEscape(owner), url.PathEscape(repo), params.Encode())

	var out []githubBranchResponse
	if err := githubGETJSON(ctx, cfg.Token, endpoint, &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github branches: %w", err)
	}
	if len(out) == 0 {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github-branches", task.ID),
			FallbackText: fmt.Sprintf("No branches found for `%s/%s`.", owner, repo),
			FinalSummary: "read-github branches returned no entries",
			Transport:    "slack",
		}, nil
	}

	lines := []string{fmt.Sprintf("Branches for `%s/%s`:", owner, repo)}
	for i, item := range out {
		if i >= 20 {
			break
		}
		sha := strings.TrimSpace(item.Commit.SHA)
		if len(sha) > 8 {
			sha = sha[:8]
		}
		protected := ""
		if item.Protected {
			protected = " [protected]"
		}
		lines = append(lines, fmt.Sprintf("- %s%s (`%s`)", strings.TrimSpace(item.Name), protected, sha))
	}
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github-branches", task.ID),
		FallbackText: strings.Join(lines, "\n"),
		FinalSummary: "read-github branches completed",
		Transport:    "slack",
	}, nil
}

func runGitHubTree(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	if owner == "" || repo == "" {
		return RenderPayload{}, fmt.Errorf("read-github tree requires owner and repo")
	}
	path := strings.Trim(strings.TrimSpace(req.Path), "/")
	if path == "" {
		path = ""
	}

	params := url.Values{}
	if strings.TrimSpace(req.Ref) != "" {
		params.Set("ref", strings.TrimSpace(req.Ref))
	}
	endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), path)
	if params.Encode() != "" {
		endpoint += "?" + params.Encode()
	}

	raw, err := githubGETRaw(ctx, cfg.Token, endpoint)
	if err != nil {
		return RenderPayload{}, fmt.Errorf("read-github tree: %w", err)
	}

	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var single githubContentsResponse
		if err := json.Unmarshal(raw, &single); err == nil && strings.TrimSpace(single.Type) == "file" {
			// If path points to a file, gracefully return file content.
			return runGitHubFileGet(ctx, cfg, task, req)
		}
	}

	var entries []githubTreeEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github tree: decode: %w", err)
	}
	if len(entries) == 0 {
		return RenderPayload{
			OutputID:     fmt.Sprintf("%s-read-github-tree", task.ID),
			FallbackText: fmt.Sprintf("No entries found for `%s/%s/%s`.", owner, repo, strings.Trim(path, "/")),
			FinalSummary: "read-github tree returned no entries",
			Transport:    "slack",
		}, nil
	}

	limit := sanitizePerPage(req.PerPage)
	lines := make([]string, 0, limit+2)
	displayPath := path
	if displayPath == "" {
		displayPath = "/"
	}
	lines = append(lines, fmt.Sprintf("GitHub tree for `%s/%s` at `%s`:", owner, repo, displayPath))
	for i, entry := range entries {
		if i >= limit {
			break
		}
		kind := strings.TrimSpace(entry.Type)
		if kind == "" {
			kind = "item"
		}
		line := fmt.Sprintf("- [%s] %s", kind, strings.TrimSpace(entry.Path))
		if entry.Size > 0 {
			line += fmt.Sprintf(" (%d bytes)", entry.Size)
		}
		if strings.TrimSpace(entry.HTMLURL) != "" {
			line += fmt.Sprintf(" - %s", strings.TrimSpace(entry.HTMLURL))
		}
		lines = append(lines, line)
	}
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github-tree", task.ID),
		FallbackText: strings.Join(lines, "\n"),
		FinalSummary: "read-github tree listing completed",
		Transport:    "slack",
	}, nil
}

func runGitHubFileGet(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	path := strings.Trim(strings.TrimSpace(req.Path), "/")
	if owner == "" || repo == "" || path == "" {
		return RenderPayload{}, fmt.Errorf("read-github file_get requires owner, repo, and path")
	}
	params := url.Values{}
	if strings.TrimSpace(req.Ref) != "" {
		params.Set("ref", strings.TrimSpace(req.Ref))
	}
	endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), path)
	if params.Encode() != "" {
		endpoint += "?" + params.Encode()
	}

	var out githubContentsResponse
	if err := githubGETJSON(ctx, cfg.Token, endpoint, &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github file get: %w", err)
	}
	if strings.TrimSpace(out.Type) != "file" {
		return RenderPayload{}, fmt.Errorf("read-github file get: %s is not a file", path)
	}
	content := decodeGitHubContent(out.Encoding, out.Content)
	if strings.TrimSpace(content) == "" {
		content = "(file is empty)"
	}
	preview := truncateRunes(content, 1800)
	if len([]rune(content)) > len([]rune(preview)) {
		preview += "\n... (truncated)"
	}

	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		ref = "default branch"
	}
	text := fmt.Sprintf("GitHub file `%s/%s/%s` (%s)\n%s\n\n```text\n%s\n```", owner, repo, path, ref, strings.TrimSpace(out.HTMLURL), preview)
	return RenderPayload{
		OutputID:     fmt.Sprintf("%s-read-github", task.ID),
		FallbackText: text,
		FinalSummary: "read-github file fetch completed",
		Transport:    "slack",
	}, nil
}

func parseReadGitHubRequest(raw string, cfg GitHubEnvConfig, defaultMode string) readGitHubRequest {
	rawLower := strings.ToLower(raw)
	req := readGitHubRequest{
		Mode:      normalizeReadGitHubMode(extractNamedField(raw, "mode")),
		Query:     strings.TrimSpace(firstNonEmpty(extractNamedField(raw, "query"), extractNamedField(raw, "code"))),
		CountOnly: strings.Contains(rawLower, "how many") || strings.Contains(rawLower, "repo count") || strings.Contains(rawLower, "count repos"),
		Org:       strings.TrimSpace(extractNamedField(raw, "org")),
		Owner:     strings.TrimSpace(extractNamedField(raw, "owner")),
		Repo:      strings.TrimSpace(extractNamedField(raw, "repo")),
		Path:      strings.TrimSpace(extractNamedField(raw, "path")),
		Ref:       strings.TrimSpace(extractNamedField(raw, "ref")),
		State:     strings.TrimSpace(extractNamedField(raw, "state")),
		PerPage:   parseIntDefault(extractNamedField(raw, "perPage"), 8),
	}

	if req.Repo != "" && strings.Contains(req.Repo, "/") {
		if owner, repo, err := splitOwnerRepo(req.Repo); err == nil {
			req.Owner = owner
			req.Repo = repo
		}
	}

	if req.Owner == "" && strings.TrimSpace(cfg.Owner) != "" {
		req.Owner = strings.TrimSpace(cfg.Owner)
	}
	if req.Repo == "" && strings.TrimSpace(cfg.Repo) != "" {
		req.Repo = strings.TrimSpace(cfg.Repo)
	}
	if req.Repo == "" {
		req.Repo = extractRepoSlug(raw)
	}

	if req.Path == "" {
		if owner, repo, ref, path, ok := parseGitHubBlobURL(raw); ok {
			req.Owner = owner
			req.Repo = repo
			req.Ref = firstNonEmpty(req.Ref, ref)
			req.Path = path
		}
	}

	if req.Path == "" {
		if owner, repo, path, ok := parseOwnerRepoPath(raw); ok {
			req.Owner = owner
			req.Repo = repo
			req.Path = path
		}
	}

	if req.Mode == "" {
		if strings.TrimSpace(defaultMode) != "" {
			req.Mode = normalizeReadGitHubMode(defaultMode)
		} else {
			switch {
			case req.Path != "":
				req.Mode = readGitHubModeFileGet
			case strings.Contains(rawLower, "code search"), strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "code:"):
				req.Mode = readGitHubModeCodeSearch
			case strings.Contains(rawLower, "repo meta"), strings.Contains(rawLower, "repository metadata"):
				req.Mode = readGitHubModeRepoMeta
			case strings.Contains(rawLower, "pull request"), strings.Contains(rawLower, "pull requests"), strings.Contains(rawLower, " prs "):
				req.Mode = readGitHubModePRs
			case strings.Contains(rawLower, "commit"), strings.Contains(rawLower, "commits"):
				req.Mode = readGitHubModeCommits
			case strings.Contains(rawLower, "branch"), strings.Contains(rawLower, "branches"):
				req.Mode = readGitHubModeBranches
			case strings.Contains(rawLower, "tree"), strings.Contains(rawLower, "directory"):
				req.Mode = readGitHubModeTree
			default:
				req.Mode = readGitHubModeRepoSearch
			}
		}
	}

	if req.Query == "" {
		if normalizeReadGitHubMode(req.Mode) == readGitHubModeCodeSearch {
			req.Query = sanitizeGitHubCodeSearchQuery(raw, strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo))
		} else {
			req.Query = sanitizeGitHubSearchQuery(raw)
		}
	}
	if normalizeReadGitHubMode(req.Mode) == readGitHubModeRepoSearch && shouldForceInventoryScopeOnly(rawLower, req.Query) {
		req.Query = ""
	}
	if req.Query == "" && normalizeReadGitHubMode(req.Mode) != readGitHubModeCodeSearch {
		if req.Org != "" {
			req.Query = "org:" + req.Org
		} else if req.Owner != "" {
			req.Query = defaultGitHubOwnerScopePrefix(cfg.OwnerScope) + ":" + req.Owner
		}
	}

	return req
}

func normalizeReadGitHubMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "repo", "repos", "repo_search", "repository", "search":
		return readGitHubModeRepoSearch
	case "repo_meta", "metadata", "meta", "read-github-repo-meta":
		return readGitHubModeRepoMeta
	case "commits", "commit", "read-github-commits":
		return readGitHubModeCommits
	case "prs", "prs_search", "pull_requests", "pulls", "read-github-prs":
		return readGitHubModePRs
	case "branches", "branch", "read-github-branches":
		return readGitHubModeBranches
	case "code", "code_search":
		return readGitHubModeCodeSearch
	case "tree", "dir", "directory", "read-github-tree":
		return readGitHubModeTree
	case "file", "file_get", "contents":
		return readGitHubModeFileGet
	default:
		return ""
	}
}

func defaultModeForReadGitHubCapability(capabilityID string) string {
	switch normalizeID(capabilityID) {
	case "read-github-repos":
		return readGitHubModeRepoSearch
	case "read-github-repo-meta":
		return readGitHubModeRepoMeta
	case "read-github-tree":
		return readGitHubModeTree
	case "read-github-file":
		return readGitHubModeFileGet
	case "read-github-code-search":
		return readGitHubModeCodeSearch
	case "read-github-commits":
		return readGitHubModeCommits
	case "read-github-prs":
		return readGitHubModePRs
	case "read-github-branches":
		return readGitHubModeBranches
	default:
		return ""
	}
}

func isReadGitHubCapability(capabilityID string) bool {
	switch normalizeID(capabilityID) {
	case "read-github", "read-github-repos", "read-github-repo-meta", "read-github-tree", "read-github-file", "read-github-code-search", "read-github-commits", "read-github-prs", "read-github-branches":
		return true
	default:
		return false
	}
}

func parseGitHubBlobURL(raw string) (owner, repo, ref, path string, ok bool) {
	m := reGitHubBlobURL.FindStringSubmatch(raw)
	if len(m) != 5 {
		return "", "", "", "", false
	}
	return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), strings.TrimSpace(m[3]), strings.TrimSpace(m[4]), true
}

func parseOwnerRepoPath(raw string) (owner, repo, path string, ok bool) {
	m := reOwnerRepoPath.FindStringSubmatch(raw)
	if len(m) != 4 {
		return "", "", "", false
	}
	owner = strings.TrimSpace(m[1])
	repo = strings.TrimSpace(m[2])
	path = strings.Trim(strings.TrimSpace(m[3]), "/")
	if owner == "" || repo == "" || path == "" {
		return "", "", "", false
	}
	return owner, repo, path, true
}

func githubGETJSON(ctx context.Context, token, endpoint string, out any) error {
	raw, err := githubGETRaw(ctx, token, endpoint)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

type githubAPIError struct {
	Endpoint    string
	StatusCode  int
	Body        string
	OAuthScopes string
}

func (e *githubAPIError) Error() string {
	if e == nil {
		return "github api error"
	}
	body := truncateRunes(strings.TrimSpace(e.Body), 280)
	if body == "" {
		body = "request failed"
	}
	return fmt.Sprintf("github %d on %s: %s", e.StatusCode, strings.TrimSpace(e.Endpoint), body)
}

type githubHTTPResult struct {
	Body       []byte
	Header     http.Header
	StatusCode int
}

func githubGET(ctx context.Context, token, endpoint string) (githubHTTPResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return githubHTTPResult{}, fmt.Errorf("missing GitHub token")
	}
	baseURL := strings.TrimSpace(firstNonEmpty(os.Getenv("GITHUB_API_BASE_URL"), "https://api.github.com"))
	baseURL = strings.TrimRight(baseURL, "/")
	u := baseURL + endpoint

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return githubHTTPResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return githubHTTPResult{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	result := githubHTTPResult{
		Body:       raw,
		Header:     resp.Header.Clone(),
		StatusCode: resp.StatusCode,
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, &githubAPIError{
			Endpoint:    endpoint,
			StatusCode:  resp.StatusCode,
			Body:        strings.TrimSpace(string(raw)),
			OAuthScopes: strings.TrimSpace(resp.Header.Get("X-OAuth-Scopes")),
		}
	}
	return result, nil
}

func githubGETRaw(ctx context.Context, token, endpoint string) ([]byte, error) {
	result, err := githubGET(ctx, token, endpoint)
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func decodeGitHubContent(encoding, content string) string {
	enc := strings.ToLower(strings.TrimSpace(encoding))
	payload := strings.TrimSpace(content)
	switch enc {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload, "\n", ""))
		if err != nil {
			return payload
		}
		return string(decoded)
	default:
		return payload
	}
}

func sanitizePerPage(v int) int {
	if v <= 0 {
		return 8
	}
	if v > 20 {
		return 20
	}
	return v
}

func parseIntDefault(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func sanitizeGitHubSearchQuery(raw string) string {
	clean := strings.TrimSpace(raw)
	clean = reSlackUserMention.ReplaceAllString(clean, " ")
	clean = strings.ReplaceAll(clean, "?", " ")
	clean = strings.ReplaceAll(clean, ",", " ")
	clean = strings.ReplaceAll(clean, ".", " ")
	clean = reGitHubNoiseWords.ReplaceAllString(clean, " ")
	clean = strings.Join(strings.Fields(clean), " ")
	return strings.TrimSpace(clean)
}

// sanitizeGitHubCodeSearchQuery turns conversational Slack questions into GitHub /search/code terms.
// Owner/repo tokens are removed when both are set because runGitHubCodeSearch adds repo:owner/name.
func sanitizeGitHubCodeSearchQuery(raw, owner, repo string) string {
	clean := sanitizeGitHubSearchQuery(raw)
	clean = reGitHubCodeSearchNoise.ReplaceAllString(clean, " ")
	clean = strings.Join(strings.Fields(clean), " ")
	clean = stripGitHubCodeSearchRepoTokens(clean, owner, repo)
	return strings.TrimSpace(clean)
}

func stripGitHubCodeSearchRepoTokens(query, owner, repo string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return query
	}
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" && repo == "" {
		return query
	}
	lo, lr := strings.ToLower(owner), strings.ToLower(repo)
	tokens := strings.Fields(query)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		base := strings.Trim(t, ",.;:!?\"'")
		bl := strings.ToLower(base)
		if lr != "" && bl == lr {
			continue
		}
		if lo != "" && bl == lo {
			continue
		}
		if lo != "" && lr != "" && bl == lo+"/"+lr {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, " ")
}

func hasExplicitGitHubScopeQualifier(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	return strings.Contains(q, "org:") || strings.Contains(q, "user:") || strings.Contains(q, "repo:")
}

func defaultGitHubOwnerScopePrefix(scope string) string {
	if strings.EqualFold(strings.TrimSpace(scope), "user") {
		return "user"
	}
	return "org"
}

func extractRepoSlug(raw string) string {
	for _, m := range reRepoSlugToken.FindAllStringSubmatch(raw, -1) {
		if len(m) < 2 {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(m[1]))
		if candidate == "" {
			continue
		}
		switch candidate {
		case "read-github", "read-github-repos", "read-github-repo-meta", "read-github-tree", "read-github-file", "read-github-code-search", "read-github-commits", "read-github-prs", "read-github-branches":
			continue
		default:
			return candidate
		}
	}
	return ""
}

func shouldForceInventoryScopeOnly(rawLower, query string) bool {
	rawLower = strings.TrimSpace(rawLower)
	if rawLower == "" {
		return false
	}
	if !strings.Contains(rawLower, "repo") && !strings.Contains(rawLower, "repository") {
		return false
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}
	if hasExplicitGitHubScopeQualifier(query) {
		return false
	}
	if strings.Contains(query, ":") {
		return false
	}
	for _, token := range strings.Fields(strings.ToLower(query)) {
		if token == "" {
			continue
		}
		if _, ok := genericRepoInventoryQueryWords[token]; !ok {
			return false
		}
	}
	return true
}
