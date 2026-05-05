package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bimross/agent-factory/internal/orchestratorevent"
)

func TestIsGitHubLikelyFollowUp(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "commits plus hyphenated repo name", raw: "how many commits does slack-orchestrator have?", want: true},
		{name: "owner slash repo", raw: "what is the status of bimross/agent-factory?", want: true},
		{name: "github host", raw: "can you check github.com/bimross/cursor-rules", want: true},
		{name: "prs shorthand", raw: "any open PRs?", want: true},
		{name: "hyphen slug without scm cue", raw: "tell me about makeacompany-ai status", want: false},
		{name: "company channel slug intro", raw: "Welcome to #grant-llc! This channel is your company workspace.", want: false},
		{name: "non github message", raw: "what should we prioritize this week?", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGitHubLikelyFollowUp(tc.raw); got != tc.want {
				t.Fatalf("isGitHubLikelyFollowUp(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestEmployeeHandlesGitHubFollowupHints(t *testing.T) {
	if !employeeHandlesGitHubFollowupHints("ross") {
		t.Fatalf("expected ross")
	}
	if employeeHandlesGitHubFollowupHints("alex") {
		t.Fatalf("did not expect alex")
	}
}

func TestThreadCapabilityHintsTTL(t *testing.T) {
	hints := newThreadCapabilityHints(2 * time.Second)
	now := time.Now().UTC()
	hints.remember("ross", "C1:123.456", "read-github", now)
	capability, ok := hints.recall("ross", "C1:123.456", now.Add(1*time.Second))
	if !ok {
		t.Fatalf("expected sticky capability to be available before ttl")
	}
	if capability != "read-github" {
		t.Fatalf("unexpected sticky capability %q", capability)
	}
	if _, ok := hints.recall("ross", "C1:123.456", now.Add(3*time.Second)); ok {
		t.Fatalf("expected sticky capability to expire after ttl")
	}
}

func TestIsReadGitHubCapabilityID(t *testing.T) {
	if !isReadGitHubCapabilityID("read-github-commits") {
		t.Fatalf("expected read-github-commits to be detected")
	}
	if isReadGitHubCapabilityID("read-web") {
		t.Fatalf("did not expect read-web to be detected as github capability")
	}
}

func TestEffectiveThreadTS(t *testing.T) {
	msg := orchestratorevent.MessageV1{
		ThreadTS:  "177.100",
		MessageTS: "177.200",
	}
	if got := effectiveThreadTS(msg); got != "177.100" {
		t.Fatalf("effectiveThreadTS() = %q, want thread ts", got)
	}
	msg.ThreadTS = ""
	if got := effectiveThreadTS(msg); got != "177.200" {
		t.Fatalf("effectiveThreadTS() = %q, want message ts fallback", got)
	}
}

func TestBuildTaskRequestText_PipelineFollowupIncludesAnchor(t *testing.T) {
	event := orchestratorevent.EventV1{
		Decision: orchestratorevent.DecisionV1{
			ExecutionMode:     orchestratorevent.ExecutionModePipeline,
			PipelineStepIndex: 3,
		},
		Message: orchestratorevent.MessageV1{
			Text:               "summarize what everyone said",
			PipelineAnchorText: "<!here> each of you should come up with ONE recommendation and then <@UTIM> summarize what everyone said",
		},
	}
	got := buildTaskRequestText(event)
	if got == event.Message.Text {
		t.Fatalf("expected follow-up step to include pipeline anchor context, got only step text: %q", got)
	}
	if !containsAll(got, "Current pipeline step request:", "Original pipeline anchor message:") {
		t.Fatalf("missing expected labels in payload: %q", got)
	}
}

func TestBuildTaskRequestText_NonPipelineUnchanged(t *testing.T) {
	event := orchestratorevent.EventV1{
		Decision: orchestratorevent.DecisionV1{ExecutionMode: ""},
		Message: orchestratorevent.MessageV1{
			Text:               "what should we do tomorrow?",
			PipelineAnchorText: "ignored anchor",
		},
	}
	if got := buildTaskRequestText(event); got != "what should we do tomorrow?" {
		t.Fatalf("non-pipeline request should stay unchanged, got %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestShouldDropStaleOrchestratorEvent_StartupBarrier(t *testing.T) {
	orig := orchestratorIngressNotBefore
	defer func() { orchestratorIngressNotBefore = orig }()

	now := time.Now().UTC()
	orchestratorIngressNotBefore = now
	ev := orchestratorevent.EventV1{
		Message: orchestratorevent.MessageV1{
			MessageTS: formatSlackTS(now.Add(-2 * time.Minute)),
		},
	}
	stale, reason, _, _ := shouldDropStaleOrchestratorEvent(ev)
	if !stale {
		t.Fatalf("expected stale=true for message older than startup barrier")
	}
	if reason != "before_startup_barrier" {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestShouldDropStaleOrchestratorEvent_AllowsFreshMessage(t *testing.T) {
	orig := orchestratorIngressNotBefore
	defer func() { orchestratorIngressNotBefore = orig }()

	now := time.Now().UTC()
	orchestratorIngressNotBefore = now.Add(-10 * time.Second)
	ev := orchestratorevent.EventV1{
		Message: orchestratorevent.MessageV1{
			MessageTS: formatSlackTS(now),
		},
	}
	stale, reason, _, _ := shouldDropStaleOrchestratorEvent(ev)
	if stale {
		t.Fatalf("expected fresh message to pass, reason=%q", reason)
	}
}

func formatSlackTS(t time.Time) string {
	sec := t.Unix()
	nano := t.Nanosecond() / 1000
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%06d", sec, nano), "0"), ".")
}
