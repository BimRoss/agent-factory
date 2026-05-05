package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var reCreatedIssueNumber = regexp.MustCompile(`#(\d+)`)

func TestLiveCreateIssueWithEnvToken(t *testing.T) {
	if os.Getenv("RUN_LIVE_GITHUB_WRITE") != "1" {
		t.Skip("set RUN_LIVE_GITHUB_WRITE=1 to run live create-issue verification")
	}
	loadEnvDevForLiveGitHubTest(t)

	ctx := context.Background()
	cfg := LoadGitHubConfigForEmployee("ross")
	if strings.TrimSpace(cfg.Token) == "" {
		t.Fatalf("ross github token not configured")
	}

	title := fmt.Sprintf("[live-smoke] create-issue runtime write test %d", time.Now().UnixNano())
	body := "Live smoke test from agent-factory runtime. This issue should be auto-closed by test cleanup."

	e := &Engine{}
	payload, err := e.runCreateIssue(Task{
		ID:              "live-create-issue-test",
		OwnerEmployeeID: "ross",
		RequestText: strings.Join([]string{
			"repo: BimRoss/create-issue",
			"title: " + title,
			"body: " + body,
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("runCreateIssue failed: %v", err)
	}
	if !strings.Contains(payload.FinalSummary, "create-issue completed") {
		t.Fatalf("unexpected final summary: %q", payload.FinalSummary)
	}

	m := reCreatedIssueNumber.FindStringSubmatch(payload.FallbackText)
	if len(m) != 2 {
		t.Fatalf("could not parse created issue number from payload: %q", payload.FallbackText)
	}
	issueNumber := strings.TrimSpace(m[1])
	if issueNumber == "" {
		t.Fatalf("parsed empty issue number from payload: %q", payload.FallbackText)
	}

	defer func() {
		if cerr := closeGitHubIssue(ctx, cfg.Token, "BimRoss", "create-issue", issueNumber); cerr != nil {
			t.Logf("warning: failed to close smoke issue #%s: %v", issueNumber, cerr)
		}
	}()

	var issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
	}
	if err := githubGETJSON(ctx, cfg.Token, "/repos/BimRoss/create-issue/issues/"+issueNumber, &issue); err != nil {
		t.Fatalf("verify created issue failed: %v", err)
	}
	if strings.TrimSpace(issue.Title) != title {
		t.Fatalf("unexpected title: got %q want %q", issue.Title, title)
	}
	if !strings.EqualFold(strings.TrimSpace(issue.State), "open") {
		t.Fatalf("expected newly created issue to be open, got %q", issue.State)
	}
}

func TestLiveCreateGitHubIssueAliasExecuteCapability(t *testing.T) {
	if os.Getenv("RUN_LIVE_GITHUB_WRITE") != "1" {
		t.Skip("set RUN_LIVE_GITHUB_WRITE=1 to run live create-github-issue alias verification")
	}
	loadEnvDevForLiveGitHubTest(t)

	ctx := context.Background()
	cfg := LoadGitHubConfigForEmployee("ross")
	if strings.TrimSpace(cfg.Token) == "" {
		t.Fatalf("ross github token not configured")
	}

	title := fmt.Sprintf("[live-smoke] create-github-issue alias test %d", time.Now().UnixNano())
	body := "Live smoke test for create-github-issue alias through ExecuteCapability. This issue should be auto-closed."

	pub := &liveTestPublisher{}
	taskStore := &liveTestTaskStore{}
	traceStore := &liveTestTraceStore{}
	registry := Registry{
		Employees: map[string]Employee{
			"ross": {ID: "ross", PackagedSkill: setFromSlice([]string{"create-github-issue"})},
		},
		BuiltInCapabilities:  map[string]struct{}{},
		PackagedSkillCatalog: setFromSlice([]string{"create-github-issue"}),
		order:                []string{"ross"},
	}
	e := &Engine{
		publisher: pub,
		tasks:     taskStore,
		traces:    traceStore,
		registry:  registry,
	}

	task := Task{
		ID:              "live-create-github-issue-alias",
		ThreadAnchor:    "live-create-github-issue-alias",
		TraceID:         "live-create-github-issue-alias",
		OwnerEmployeeID: "ross",
		RequestText: strings.Join([]string{
			"repo: BimRoss/create-issue",
			"title: " + title,
			"body: " + body,
		}, "\n"),
	}
	task.LastState = StatePlanning
	if err := taskStore.SaveTask(task); err != nil {
		t.Fatalf("seed task store failed: %v", err)
	}

	if _, err := e.ExecuteCapability(ctx, task, "create-github-issue", nil); err != nil {
		t.Fatalf("ExecuteCapability(create-github-issue) failed: %v", err)
	}

	if strings.TrimSpace(pub.final.FinalSummary) != "create-issue completed" {
		t.Fatalf("unexpected final summary: %q", pub.final.FinalSummary)
	}
	if !strings.Contains(pub.final.FallbackText, "Created GitHub issue #") {
		t.Fatalf("expected created issue message, got: %q", pub.final.FallbackText)
	}

	m := reCreatedIssueNumber.FindStringSubmatch(pub.final.FallbackText)
	if len(m) != 2 || strings.TrimSpace(m[1]) == "" {
		t.Fatalf("could not parse created issue number from payload: %q", pub.final.FallbackText)
	}
	issueNumber := strings.TrimSpace(m[1])

	defer func() {
		if cerr := closeGitHubIssue(ctx, cfg.Token, "BimRoss", "create-issue", issueNumber); cerr != nil {
			t.Logf("warning: failed to close smoke issue #%s: %v", issueNumber, cerr)
		}
	}()

	var issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
	}
	if err := githubGETJSON(ctx, cfg.Token, "/repos/BimRoss/create-issue/issues/"+issueNumber, &issue); err != nil {
		t.Fatalf("verify created alias issue failed: %v", err)
	}
	if strings.TrimSpace(issue.Title) != title {
		t.Fatalf("unexpected alias issue title: got %q want %q", issue.Title, title)
	}
	if !strings.EqualFold(strings.TrimSpace(issue.State), "open") {
		t.Fatalf("expected alias-created issue to be open, got %q", issue.State)
	}
}

func closeGitHubIssue(ctx context.Context, token, owner, repo, issueNumber string) error {
	body, err := json.Marshal(map[string]string{"state": "closed"})
	if err != nil {
		return fmt.Errorf("encode close issue body: %w", err)
	}
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%s", strings.TrimSpace(owner), strings.TrimSpace(repo), strings.TrimSpace(issueNumber))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build close issue request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("close issue http request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("close issue github %d: %s", resp.StatusCode, truncateRunes(strings.TrimSpace(string(respBody)), 280))
	}
	return nil
}

type liveTestPublisher struct {
	final RenderPayload
}

func (p *liveTestPublisher) PublishStatus(event LifecycleEvent) error { return nil }
func (p *liveTestPublisher) PublishUpdate(task Task, message string) error {
	return nil
}
func (p *liveTestPublisher) PublishThreadNotice(task Task, text string) error { return nil }
func (p *liveTestPublisher) PublishFinal(task Task, payload RenderPayload) error {
	p.final = payload
	return nil
}
func (p *liveTestPublisher) ClearInboundReaction(task Task) error { return nil }

type liveTestTaskStore struct {
	tasks map[string]Task
}

func (s *liveTestTaskStore) SaveTask(task Task) error {
	if s.tasks == nil {
		s.tasks = map[string]Task{}
	}
	s.tasks[task.ID] = task
	return nil
}

func (s *liveTestTaskStore) GetTask(taskID string) (Task, bool) {
	if s.tasks == nil {
		return Task{}, false
	}
	task, ok := s.tasks[taskID]
	return task, ok
}

type liveTestTraceStore struct {
	entries map[string][]TraceEntry
}

func (s *liveTestTraceStore) AppendTrace(taskID string, entry TraceEntry) error {
	if s.entries == nil {
		s.entries = map[string][]TraceEntry{}
	}
	s.entries[taskID] = append(s.entries[taskID], entry)
	return nil
}

func (s *liveTestTraceStore) ListTrace(taskID string) []TraceEntry {
	if s.entries == nil {
		return nil
	}
	return s.entries[taskID]
}
