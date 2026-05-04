package runtime

import "testing"

func TestParseReadGitHubRequestFileFromBlobURL(t *testing.T) {
	cfg := GitHubEnvConfig{Owner: "BimRoss", Repo: "create-issue"}
	req := parseReadGitHubRequest("get file https://github.com/octocat/hello-world/blob/main/README.md", cfg)
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
	req := parseReadGitHubRequest(raw, cfg)
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
	req := parseReadGitHubRequest(raw, cfg)
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
