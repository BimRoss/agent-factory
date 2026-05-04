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
	readGitHubModeCodeSearch = "code_search"
	readGitHubModeFileGet    = "file_get"
)

var (
	reGitHubBlobURL   = regexp.MustCompile(`https?://github\.com/([^/\s]+)/([^/\s]+)/blob/([^/\s]+)/([^\s?#]+)`)
	reOwnerRepoPath   = regexp.MustCompile(`\b([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.\-/]+)\b`)
	reReadGitHubNoise = regexp.MustCompile(`(?i)\b(read|search|find|get|show)\b(?:\s+(?:a|an|the))?\s+(?:github\s+)?(?:repo|repos|repository|repositories|code|file|contents?)?\b`)
)

type readGitHubRequest struct {
	Mode    string
	Query   string
	Org     string
	Owner   string
	Repo    string
	Path    string
	Ref     string
	PerPage int
}

type githubRepoSearchResponse struct {
	Items []struct {
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

func (e *Engine) runReadGitHub(ctx context.Context, task Task) (RenderPayload, error) {
	cfg := LoadGitHubConfigForEmployee(task.OwnerEmployeeID)
	if strings.TrimSpace(cfg.Token) == "" {
		return RenderPayload{}, fmt.Errorf("read-github: set GITHUB_TOKEN or %s_GITHUB_TOKEN", strings.ToUpper(strings.TrimSpace(task.OwnerEmployeeID)))
	}

	req := parseReadGitHubRequest(task.RequestText, cfg)
	switch req.Mode {
	case readGitHubModeFileGet:
		return runGitHubFileGet(ctx, cfg, task, req)
	case readGitHubModeCodeSearch:
		return runGitHubCodeSearch(ctx, cfg, task, req)
	default:
		return runGitHubRepoSearch(ctx, cfg, task, req)
	}
}

func runGitHubRepoSearch(ctx context.Context, cfg GitHubEnvConfig, task Task, req readGitHubRequest) (RenderPayload, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = "org:" + strings.TrimSpace(cfg.Owner)
	}
	if req.Org != "" && !strings.Contains(query, "org:") {
		query += " org:" + req.Org
	}

	perPage := sanitizePerPage(req.PerPage)
	params := url.Values{}
	params.Set("q", query)
	params.Set("per_page", strconv.Itoa(perPage))

	var out githubRepoSearchResponse
	if err := githubGETJSON(ctx, cfg.Token, "/search/repositories?"+params.Encode(), &out); err != nil {
		return RenderPayload{}, fmt.Errorf("read-github repo search: %w", err)
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

func parseReadGitHubRequest(raw string, cfg GitHubEnvConfig) readGitHubRequest {
	req := readGitHubRequest{
		Mode:    normalizeReadGitHubMode(extractNamedField(raw, "mode")),
		Query:   strings.TrimSpace(firstNonEmpty(extractNamedField(raw, "query"), extractNamedField(raw, "code"))),
		Org:     strings.TrimSpace(extractNamedField(raw, "org")),
		Owner:   strings.TrimSpace(extractNamedField(raw, "owner")),
		Repo:    strings.TrimSpace(extractNamedField(raw, "repo")),
		Path:    strings.TrimSpace(extractNamedField(raw, "path")),
		Ref:     strings.TrimSpace(extractNamedField(raw, "ref")),
		PerPage: parseIntDefault(extractNamedField(raw, "perPage"), 8),
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
		switch {
		case req.Path != "":
			req.Mode = readGitHubModeFileGet
		case strings.Contains(strings.ToLower(raw), "code search"), strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "code:"):
			req.Mode = readGitHubModeCodeSearch
		default:
			req.Mode = readGitHubModeRepoSearch
		}
	}

	if req.Query == "" {
		clean := strings.TrimSpace(raw)
		clean = reReadGitHubNoise.ReplaceAllString(clean, " ")
		clean = strings.Join(strings.Fields(clean), " ")
		req.Query = strings.TrimSpace(clean)
	}
	if req.Query == "" {
		if req.Org != "" {
			req.Query = "org:" + req.Org
		} else if req.Owner != "" {
			req.Query = "user:" + req.Owner
		}
	}

	return req
}

func normalizeReadGitHubMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "repo", "repos", "repo_search", "repository", "search":
		return readGitHubModeRepoSearch
	case "code", "code_search":
		return readGitHubModeCodeSearch
	case "file", "file_get", "contents":
		return readGitHubModeFileGet
	default:
		return ""
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
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("missing GitHub token")
	}
	if out == nil {
		return fmt.Errorf("output target is required")
	}
	baseURL := strings.TrimSpace(firstNonEmpty(os.Getenv("GITHUB_API_BASE_URL"), "https://api.github.com"))
	baseURL = strings.TrimRight(baseURL, "/")
	u := baseURL + endpoint

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github %d: %s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(raw)), 280))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
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
