package main

import (
	"testing"
	"time"
)

func TestIsGitHubLikelyFollowUp(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "github keyword", raw: "how many commits does slack-orchestrator have?", want: true},
		{name: "repo slug cue", raw: "tell me about makeacompany-ai status", want: true},
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
