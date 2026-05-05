package runtime

import "testing"

func TestInferIssueTargetRepo_OwnerRepoInMessage(t *testing.T) {
	got := inferIssueTargetRepo("create issue repo BimRoss/makeacompany-ai for onboarding bug", "", "", "BimRoss")
	if got != "BimRoss/makeacompany-ai" {
		t.Fatalf("expected explicit owner/repo, got %q", got)
	}
}

func TestInferIssueTargetRepo_RepoSlugUsesDefaultOwner(t *testing.T) {
	got := inferIssueTargetRepo("create issue for makeacompany-ai about auth", "", "", "BimRoss")
	if got != "BimRoss/makeacompany-ai" {
		t.Fatalf("expected inferred repo with owner, got %q", got)
	}
}

func TestInferIssueTargetRepo_FallbackRepo(t *testing.T) {
	got := inferIssueTargetRepo("create issue about docs", "", "BimRoss/makeacompany-ai", "BimRoss")
	if got != "BimRoss/makeacompany-ai" {
		t.Fatalf("expected fallback repo, got %q", got)
	}
}

func TestInferIssueTargetRepo_NoSignalsReturnsEmpty(t *testing.T) {
	got := inferIssueTargetRepo("create issue about docs", "", "", "")
	if got != "" {
		t.Fatalf("expected empty repo when no signals, got %q", got)
	}
}
