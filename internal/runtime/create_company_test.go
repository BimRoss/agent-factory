package runtime

import (
	"strings"
	"testing"
)

func TestParseCreateCompanyRequest_Called(t *testing.T) {
	req := parseCreateCompanyRequest(Task{
		RequestText: "@Ross create a company called legendz",
		HumanUserID: "U12345",
	})
	if req.ChannelSlug != "legendz" {
		t.Fatalf("slug=%q", req.ChannelSlug)
	}
	if len(req.FounderUserIDs) < 1 || req.FounderUserIDs[0] != "U12345" {
		t.Fatalf("founders=%v", req.FounderUserIDs)
	}
}

func TestParseCreateCompanyRequest_MentionedFounder(t *testing.T) {
	req := parseCreateCompanyRequest(Task{
		RequestText: "create a company called brothas with <@U99AAAAAA>",
		HumanUserID: "U12345",
	})
	if req.ChannelSlug != "brothas" {
		t.Fatalf("slug=%q", req.ChannelSlug)
	}
	ids := map[string]bool{}
	for _, id := range req.FounderUserIDs {
		ids[id] = true
	}
	if !ids["U12345"] || !ids["U99AAAAAA"] {
		t.Fatalf("founders=%v", req.FounderUserIDs)
	}
}

func TestNormalizeSlackChannelName(t *testing.T) {
	if s := normalizeSlackChannelName("My_Company.Name"); s != "my-company-name" {
		t.Fatalf("got %q", s)
	}
}

func TestChannelSlugWithNumericSuffix(t *testing.T) {
	if got := channelSlugWithNumericSuffix("grant-llc", 0); got != "grant-llc" {
		t.Fatalf("attempt0=%q", got)
	}
	if got := channelSlugWithNumericSuffix("grant-llc", 1); got != "grant-llc-1" {
		t.Fatalf("attempt1=%q", got)
	}
	if got := channelSlugWithNumericSuffix("grant-llc", 2); got != "grant-llc-2" {
		t.Fatalf("attempt2=%q", got)
	}
}

func TestTruncateSlackConversationName(t *testing.T) {
	long := strings.Repeat("x", 90)
	got := truncateSlackConversationName(long)
	if len(got) > 80 {
		t.Fatalf("len=%d", len(got))
	}
	if truncateSlackConversationName("grant-llc") != "grant-llc" {
		t.Fatal("unexpected trim")
	}
}

func TestExpandCompanyInviteCohort_Dedupes(t *testing.T) {
	t.Setenv("EMPLOYEE_ID", "joanne")
	t.Setenv("ORCHESTRATOR_BOT_USER_ID", "U_ORCH")
	t.Setenv("MULTIAGENT_BOT_USER_IDS", "ross=U_ROSS,alex=U_ALEX")
	out := expandCompanyInviteCohort([]string{"U_HUMAN", "U_ORCH"})
	seen := map[string]int{}
	for _, id := range out {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("dup %s count=%d", id, n)
		}
	}
}

func TestCompanyPostCreateCreatedMrkdwn(t *testing.T) {
	got := companyPostCreateCreatedMrkdwn("C123", "grant-llc")
	if got != "Created: <#C123|grant-llc>" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestCompanyPostCreateCreatedMrkdwn_NoDashboardEvenWhenURLConfigured(t *testing.T) {
	t.Setenv("MAKEACOMPANY_APP_BASE_URL", "http://localhost:3000")
	got := companyPostCreateCreatedMrkdwn("C123", "this-is-so-sweet")
	if got != "Created: <#C123|this-is-so-sweet>" {
		t.Fatalf("unexpected: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "dashboard") {
		t.Fatalf("create-company fallback should not mention dashboard: %q", got)
	}
}
