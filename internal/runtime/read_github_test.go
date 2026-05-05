package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseReadGitHubRequestFileFromBlobURL(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss", Repo: "create-issue"}
	req := parseReadGitHubRequest("get file https://github.com/octocat/hello-world/blob/main/README.md", cfg, "")
	if req.Mode != readGitHubModeFileGet {
		t.Fatalf("expected mode %q, got %q", readGitHubModeFileGet, req.Mode)
	}
	if req.Owner != "octocat" || req.Repo != "hello-world" {
		t.Fatalf("unexpected owner/repo: %s/%s", req.Owner, req.Repo)
	}
	if req.Path != "README.md" {
		t.Fatalf("unexpected path: %q", req.Path)
	}
	if req.Ref != "main" {
		t.Fatalf("unexpected ref: %q", req.Ref)
	}
}

func TestParseReadGitHubRequestCodeSearch(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss"}
	raw := "mode: code\nquery: routerToolIntentEnum\norg: BimRoss\nperPage: 25"
	req := parseReadGitHubRequest(raw, cfg, "")
	if req.Mode != readGitHubModeCodeSearch {
		t.Fatalf("expected mode %q, got %q", readGitHubModeCodeSearch, req.Mode)
	}
	if req.Query != "routerToolIntentEnum" {
		t.Fatalf("unexpected query: %q", req.Query)
	}
	if req.Org != "BimRoss" {
		t.Fatalf("unexpected org: %q", req.Org)
	}
	if req.PerPage != 25 {
		t.Fatalf("unexpected perPage: %d", req.PerPage)
	}
}

func TestParseReadGitHubRequestRepoField(t *testing.T) {
	cfg := GitHubEnvConfig{}
	raw := "mode: file\nrepo: BimRoss/agent-factory\npath: internal/runtime/engine.go\nref: main"
	req := parseReadGitHubRequest(raw, cfg, "")
	if req.Owner != "BimRoss" || req.Repo != "agent-factory" {
		t.Fatalf("unexpected owner/repo: %s/%s", req.Owner, req.Repo)
	}
	if req.Path != "internal/runtime/engine.go" {
		t.Fatalf("unexpected path: %q", req.Path)
	}
	if req.Ref != "main" {
		t.Fatalf("unexpected ref: %q", req.Ref)
	}
}

func TestDecodeGitHubContentBase64(t *testing.T) {
	got := decodeGitHubContent("base64", "aGVsbG8K")
	if got != "hello\n" {
		t.Fatalf("unexpected decoded content: %q", got)
	}
}

func TestSanitizePerPage(t *testing.T) {
	if got := sanitizePerPage(0); got != 8 {
		t.Fatalf("expected default 8, got %d", got)
	}
	if got := sanitizePerPage(999); got != 20 {
		t.Fatalf("expected cap 20, got %d", got)
	}
	if got := sanitizePerPage(6); got != 6 {
		t.Fatalf("expected passthrough 6, got %d", got)
	}
}

func TestParseReadGitHubRequestBroadReposDefaultsToOrgQuery(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss"}
	raw := "<@U0ATGEYJ18T> can you read our github repos?"
	req := parseReadGitHubRequest(raw, cfg, readGitHubModeRepoSearch)
	if req.Query != "org:BimRoss" {
		t.Fatalf("expected org default query, got %q", req.Query)
	}
	if req.Mode != readGitHubModeRepoSearch {
		t.Fatalf("expected mode %q, got %q", readGitHubModeRepoSearch, req.Mode)
	}
}

func TestParseReadGitHubRequestNaturalReposQuestionDefaultsToOrgQuery(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss"}
	raw := "<@U0ATGEYJ18T> what do you know about our github repos?"
	req := parseReadGitHubRequest(raw, cfg, readGitHubModeRepoSearch)
	if req.Query != "org:BimRoss" {
		t.Fatalf("expected org default query, got %q", req.Query)
	}
	if req.Mode != readGitHubModeRepoSearch {
		t.Fatalf("expected mode %q, got %q", readGitHubModeRepoSearch, req.Mode)
	}
}

func TestParseReadGitHubRequestAllReposQuestionDefaultsToOrgQuery(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss"}
	raw := "<@U0ATGEYJ18T> tell me all our repositories"
	req := parseReadGitHubRequest(raw, cfg, readGitHubModeRepoSearch)
	if req.Query != "org:BimRoss" {
		t.Fatalf("expected org default query, got %q", req.Query)
	}
}

func TestParseReadGitHubRequestKeepsExplicitRepoSearchTerms(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss"}
	raw := "<@U0ATGEYJ18T> list terraform repos"
	req := parseReadGitHubRequest(raw, cfg, readGitHubModeRepoSearch)
	if req.Query != "terraform" {
		t.Fatalf("expected explicit search term to remain, got %q", req.Query)
	}
}

func TestParseReadGitHubRequestRepoCountQueryDefaultsToOrg(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss"}
	raw := "<@U0ATGEYJ18T> how many repos do we have in our org?"
	req := parseReadGitHubRequest(raw, cfg, readGitHubModeRepoSearch)
	if !req.CountOnly {
		t.Fatalf("expected CountOnly true")
	}
	if req.Query != "org:BimRoss" {
		t.Fatalf("expected org default query, got %q", req.Query)
	}
}

func TestParseReadGitHubRequestRepoCountQueryDefaultsToUserScope(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "grantfoster", OwnerScope: "user"}
	raw := "how many repos do we have?"
	req := parseReadGitHubRequest(raw, cfg, readGitHubModeRepoSearch)
	if req.Query != "user:grantfoster" {
		t.Fatalf("expected user default query, got %q", req.Query)
	}
}

func TestHasExplicitGitHubScopeQualifier(t *testing.T) {
	if !hasExplicitGitHubScopeQualifier("org:BimRoss archived:false") {
		t.Fatalf("expected org qualifier")
	}
	if hasExplicitGitHubScopeQualifier("interesting repos") {
		t.Fatalf("did not expect qualifier")
	}
}

func TestDefaultModeForReadGitHubCapability(t *testing.T) {
	if got := defaultModeForReadGitHubCapability("read-github-file"); got != readGitHubModeFileGet {
		t.Fatalf("unexpected default mode for read-github-file: %q", got)
	}
	if got := defaultModeForReadGitHubCapability("read-github-tree"); got != readGitHubModeTree {
		t.Fatalf("unexpected default mode for read-github-tree: %q", got)
	}
	if got := defaultModeForReadGitHubCapability("read-github"); got != "" {
		t.Fatalf("expected empty default mode for read-github, got %q", got)
	}
	if got := defaultModeForReadGitHubCapability("read-github-prs"); got != readGitHubModePRs {
		t.Fatalf("unexpected default mode for read-github-prs: %q", got)
	}
	if got := defaultModeForReadGitHubCapability("read-github-commits"); got != readGitHubModeCommits {
		t.Fatalf("unexpected default mode for read-github-commits: %q", got)
	}
	if got := defaultModeForReadGitHubCapability("read-github-branches"); got != readGitHubModeBranches {
		t.Fatalf("unexpected default mode for read-github-branches: %q", got)
	}
}

func TestNormalizeReadGitHubModeNewModularModes(t *testing.T) {
	if got := normalizeReadGitHubMode("read-github-prs"); got != readGitHubModePRs {
		t.Fatalf("expected prs mode, got %q", got)
	}
	if got := normalizeReadGitHubMode("commits"); got != readGitHubModeCommits {
		t.Fatalf("expected commits mode, got %q", got)
	}
	if got := normalizeReadGitHubMode("branches"); got != readGitHubModeBranches {
		t.Fatalf("expected branches mode, got %q", got)
	}
}

func TestParseReadGitHubRequestExtractsRepoSlugFromNaturalText(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss"}
	raw := "Tell me about makeacompany-ai codebase and commits"
	req := parseReadGitHubRequest(raw, cfg, readGitHubModeCommits)
	if req.Repo != "makeacompany-ai" {
		t.Fatalf("expected repo makeacompany-ai, got %q", req.Repo)
	}
	if req.Owner != "BimRoss" {
		t.Fatalf("expected owner BimRoss, got %q", req.Owner)
	}
	if req.Mode != readGitHubModeCommits {
		t.Fatalf("expected commits mode, got %q", req.Mode)
	}
}

func TestReadGitHubPreflightEndpointCommits(t *testing.T) {
	cfg := GitHubEnvConfig{}
	req := readGitHubRequest{
		Mode:  readGitHubModeCommits,
		Owner: "BimRoss",
		Repo:  "slack-orchestrator",
		Ref:   "main",
	}
	endpoint, modeLabel, err := readGitHubPreflightEndpoint(cfg, req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if modeLabel != readGitHubModeCommits {
		t.Fatalf("unexpected mode label: %q", modeLabel)
	}
	if !strings.Contains(endpoint, "/repos/BimRoss/slack-orchestrator/commits") {
		t.Fatalf("unexpected endpoint: %q", endpoint)
	}
	if !strings.Contains(endpoint, "sha=main") {
		t.Fatalf("expected sha param in endpoint: %q", endpoint)
	}
}

func TestPreflightReadGitHubRequestIncludesOAuthScopeHintOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-OAuth-Scopes", "repo,read:org")
		http.Error(w, `{"message":"Resource not accessible by personal access token"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	oldBase := os.Getenv("GITHUB_API_BASE_URL")
	t.Cleanup(func() {
		_ = os.Setenv("GITHUB_API_BASE_URL", oldBase)
	})
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)

	cfg := GitHubEnvConfig{Token: "test-token"}
	req := readGitHubRequest{
		Mode:  readGitHubModeRepoMeta,
		Owner: "BimRoss",
		Repo:  "slack-orchestrator",
	}
	err := preflightReadGitHubRequest(context.Background(), cfg, req)
	if err == nil {
		t.Fatalf("expected preflight failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "preflight blocked") {
		t.Fatalf("expected preflight blocked message, got %q", msg)
	}
	if !strings.Contains(msg, "repo,read:org") {
		t.Fatalf("expected oauth scope hint in error, got %q", msg)
	}
}
