package runtime

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// preflightReadGitHubRequest runs a lightweight endpoint probe for the target
// read-github mode so shared read-github-* capabilities fail fast with a clear
// auth/permission explanation before execution logic runs.
func preflightReadGitHubRequest(ctx context.Context, cfg GitHubEnvConfig, req readGitHubRequest) error {
	endpoint, modeLabel, err := readGitHubPreflightEndpoint(ctx, cfg, req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(endpoint) == "" {
		return nil
	}
	res, err := githubGET(ctx, cfg.Token, endpoint)
	if err != nil {
		if apiErr, ok := err.(*githubAPIError); ok {
			scopeNote := ""
			if strings.TrimSpace(apiErr.OAuthScopes) != "" {
				scopeNote = fmt.Sprintf(" token scopes header=%q.", strings.TrimSpace(apiErr.OAuthScopes))
			}
			return fmt.Errorf("read-github %s preflight blocked: %s%s", modeLabel, apiErr.Error(), scopeNote)
		}
		return fmt.Errorf("read-github %s preflight blocked: %w", modeLabel, err)
	}
	if strings.TrimSpace(res.Header.Get("X-OAuth-Scopes")) != "" {
		// Intentional no-op: header is available for diagnostics in higher-level probe/reporting.
	}
	return nil
}

func readGitHubPreflightEndpoint(ctx context.Context, cfg GitHubEnvConfig, req readGitHubRequest) (endpoint string, modeLabel string, err error) {
	mode := normalizeReadGitHubMode(req.Mode)
	if mode == "" {
		mode = readGitHubModeRepoSearch
	}
	switch mode {
	case readGitHubModeRepoSearch:
		query := buildReadGitHubRepoSearchQuery(req, cfg)
		if githubRepoInventoryUsesListAPI(query) {
			ep, _, err := githubRepoListEndpointForInventory(ctx, cfg.Token, cfg, req, query)
			if err != nil {
				return "", mode, err
			}
			return ep, mode, nil
		}
		params := url.Values{}
		params.Set("q", query)
		params.Set("per_page", "1")
		return "/search/repositories?" + params.Encode(), mode, nil
	case readGitHubModeCodeSearch:
		query := strings.TrimSpace(req.Query)
		if query == "" {
			return "", mode, nil
		}
		if req.Org != "" && !strings.Contains(query, "org:") {
			query += " org:" + strings.TrimSpace(req.Org)
		}
		if req.Owner != "" && req.Repo != "" && !strings.Contains(query, "repo:") {
			query += " repo:" + strings.TrimSpace(req.Owner) + "/" + strings.TrimSpace(req.Repo)
		}
		params := url.Values{}
		params.Set("q", query)
		params.Set("per_page", "1")
		return "/search/code?" + params.Encode(), mode, nil
	case readGitHubModeRepoMeta:
		owner, repo := strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo)
		if owner == "" || repo == "" {
			return "", mode, nil
		}
		return fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo)), mode, nil
	case readGitHubModeCommits:
		owner, repo := strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo)
		if owner == "" || repo == "" {
			return "", mode, nil
		}
		params := url.Values{}
		params.Set("per_page", "1")
		if strings.TrimSpace(req.Ref) != "" {
			params.Set("sha", strings.TrimSpace(req.Ref))
		}
		return fmt.Sprintf("/repos/%s/%s/commits?%s", url.PathEscape(owner), url.PathEscape(repo), params.Encode()), mode, nil
	case readGitHubModePRs:
		owner, repo := strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo)
		if owner == "" || repo == "" {
			return "", mode, nil
		}
		params := url.Values{}
		params.Set("state", "open")
		params.Set("per_page", "1")
		return fmt.Sprintf("/repos/%s/%s/pulls?%s", url.PathEscape(owner), url.PathEscape(repo), params.Encode()), mode, nil
	case readGitHubModeBranches:
		owner, repo := strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo)
		if owner == "" || repo == "" {
			return "", mode, nil
		}
		params := url.Values{}
		params.Set("per_page", "1")
		return fmt.Sprintf("/repos/%s/%s/branches?%s", url.PathEscape(owner), url.PathEscape(repo), params.Encode()), mode, nil
	case readGitHubModeTree:
		owner, repo := strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo)
		if owner == "" || repo == "" {
			return "", mode, nil
		}
		path := strings.Trim(strings.TrimSpace(req.Path), "/")
		params := url.Values{}
		if strings.TrimSpace(req.Ref) != "" {
			params.Set("ref", strings.TrimSpace(req.Ref))
		}
		endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), path)
		if params.Encode() != "" {
			endpoint += "?" + params.Encode()
		}
		return endpoint, mode, nil
	case readGitHubModeFileGet:
		owner, repo := strings.TrimSpace(req.Owner), strings.TrimSpace(req.Repo)
		if owner == "" || repo == "" || strings.TrimSpace(req.Path) == "" {
			return "", mode, nil
		}
		path := strings.Trim(strings.TrimSpace(req.Path), "/")
		params := url.Values{}
		if strings.TrimSpace(req.Ref) != "" {
			params.Set("ref", strings.TrimSpace(req.Ref))
		}
		endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), path)
		if params.Encode() != "" {
			endpoint += "?" + params.Encode()
		}
		return endpoint, mode, nil
	default:
		return "", mode, nil
	}
}
