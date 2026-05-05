package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveGitHubCapabilitiesWithEnvToken(t *testing.T) {
	if os.Getenv("RUN_LIVE_GITHUB") != "1" {
		t.Skip("set RUN_LIVE_GITHUB=1 to run live GitHub verification")
	}
	loadEnvDevForLiveGitHubTest(t)

	// Preflight sanity: this test validates Ross GitHub toolchain because
	// read-github* skills are bound to ross runtime tools.
	probe := ProbeGitHubAccess(context.Background(), "ross")
	if !probe.TokenConfigured {
		t.Fatalf("ross token not configured")
	}
	if !probe.ListScopeOK {
		t.Fatalf("ross token cannot list repo scope: warning=%q error=%q", probe.Warning, probe.Error)
	}

	cases := []struct {
		name               string
		capabilityID       string
		requestText        string
		summaryMustContain string
	}{
		{
			name:               "read-github",
			capabilityID:       "read-github",
			requestText:        "<@U0ATGEYJ18T> are you able to read github?",
			summaryMustContain: "repo search",
		},
		{
			name:               "read-github-repos",
			capabilityID:       "read-github-repos",
			requestText:        "<@U0ATGEYJ18T> what repos can you see on github?",
			summaryMustContain: "repo",
		},
		{
			name:               "read-github-repo-meta",
			capabilityID:       "read-github-repo-meta",
			requestText:        "owner: BimRoss\nrepo: agent-factory",
			summaryMustContain: "repo metadata",
		},
		{
			name:               "read-github-tree",
			capabilityID:       "read-github-tree",
			requestText:        "owner: BimRoss\nrepo: agent-factory\npath: internal/runtime",
			summaryMustContain: "tree",
		},
		{
			name:               "read-github-file",
			capabilityID:       "read-github-file",
			requestText:        "owner: BimRoss\nrepo: agent-factory\npath: README.md",
			summaryMustContain: "file fetch",
		},
		{
			name:               "read-github-code-search",
			capabilityID:       "read-github-code-search",
			requestText:        "query: parseReadGitHubRequest repo:agent-factory org:BimRoss",
			summaryMustContain: "code search",
		},
		{
			name:               "read-github-commits",
			capabilityID:       "read-github-commits",
			requestText:        "owner: BimRoss\nrepo: agent-factory",
			summaryMustContain: "commits",
		},
		{
			name:               "read-github-prs",
			capabilityID:       "read-github-prs",
			requestText:        "owner: BimRoss\nrepo: agent-factory\nstate: all",
			summaryMustContain: "prs",
		},
		{
			name:               "read-github-branches",
			capabilityID:       "read-github-branches",
			requestText:        "owner: BimRoss\nrepo: agent-factory",
			summaryMustContain: "branches",
		},
	}

	e := &Engine{}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := e.runReadGitHubCapability(ctx, Task{
				ID:              "live-github-test",
				OwnerEmployeeID: "ross",
				RequestText:     tc.requestText,
			}, tc.capabilityID)
			if err != nil {
				t.Fatalf("capability %s failed: %v", tc.capabilityID, err)
			}
			if strings.TrimSpace(payload.FallbackText) == "" {
				t.Fatalf("capability %s returned empty fallback text", tc.capabilityID)
			}
			if !strings.Contains(strings.ToLower(payload.FinalSummary), strings.ToLower(tc.summaryMustContain)) {
				t.Fatalf("capability %s final summary %q missing %q", tc.capabilityID, payload.FinalSummary, tc.summaryMustContain)
			}
		})
	}
}

func loadEnvDevForLiveGitHubTest(t *testing.T) {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(root, ".env.dev"))
	if err != nil {
		t.Fatalf("read .env.dev: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"'")
		_ = os.Setenv(key, val)
	}
}
