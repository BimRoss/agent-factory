package emailaction

import (
	"strings"
	"testing"
)

func TestInferConversationalEmailSubject(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{
			raw:  `<@U123> send an email to me, subject is Testers, with 3 paragraphs describing why`,
			want: "Testers",
		},
		{
			raw:  `send email to me title is "Quarterly recap", instruction: keep it short`,
			want: "Quarterly recap",
		},
		{
			raw:  `draft email subject line is Hello World; body: foo`,
			want: "Hello World",
		},
		{
			raw:  `send email instruction: hello`,
			want: "",
		},
		{
			raw:  `<!here> debate the war in iran and email me a summary, subject War In Iran`,
			want: "War In Iran",
		},
	}
	for _, tc := range cases {
		got := inferConversationalEmailSubject(tc.raw)
		if got != tc.want {
			t.Fatalf("inferConversationalEmailSubject(%q)=%q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseRecipientEmailsSelfAliases(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"me", "myself", "self", "ME "} {
		got, err := ParseRecipientEmails(raw)
		if err != nil {
			t.Fatalf("ParseRecipientEmails(%q): %v", raw, err)
		}
		if len(got) != 1 || got[0] != strings.TrimSpace(strings.ToLower(raw)) {
			t.Fatalf("ParseRecipientEmails(%q)=%v want single alias token", raw, got)
		}
	}
}

func TestParseSendEmailActionBareSubjectCommaSeparated(t *testing.T) {
	t.Parallel()
	raw := `<!here> debate the war in iran and email me a summary, subject War In Iran`
	action, matched, err := ParseSendEmailAction(raw)
	if err != nil || !matched {
		t.Fatalf("parse: matched=%v err=%v", matched, err)
	}
	if strings.TrimSpace(action.Subject) != "War In Iran" {
		t.Fatalf("subject=%q want War In Iran", action.Subject)
	}
	if strings.TrimSpace(action.BodyInstruction) == "" {
		t.Fatal("expected body instruction remainder")
	}
}

func TestParseSendEmailActionConversationalSubjectStripsFromInstruction(t *testing.T) {
	t.Parallel()
	raw := `<@U0ABCD> send an email to me, subject is Testers, with 3 paragraphs about woodworking`
	action, matched, err := ParseSendEmailAction(raw)
	if err != nil || !matched {
		t.Fatalf("parse: matched=%v err=%v", matched, err)
	}
	if strings.TrimSpace(action.Subject) != "Testers" {
		t.Fatalf("subject=%q want Testers", action.Subject)
	}
	got := strings.TrimSpace(action.BodyInstruction)
	if strings.Contains(strings.ToLower(got), "subject is testers") {
		t.Fatalf("instruction should not repeat subject phrase: %q", got)
	}
	if got == "" {
		t.Fatal("expected body instruction remainder")
	}
}

func TestParseSendEmailPatch_ContinuationFields(t *testing.T) {
	t.Parallel()
	action, matched, err := ParseSendEmailPatch("button is Join now, link is https://example.com/welcome")
	if err != nil || !matched {
		t.Fatalf("patch parse: matched=%v err=%v", matched, err)
	}
	if strings.TrimSpace(action.CTAText) != "Join now" {
		t.Fatalf("cta text=%q want %q", action.CTAText, "Join now")
	}
	if strings.TrimSpace(action.CTAURL) != "https://example.com/welcome" {
		t.Fatalf("cta url=%q", action.CTAURL)
	}
}

func TestParseSendEmailAction_LinkToNaturalLanguage(t *testing.T) {
	t.Parallel()
	raw := `<@U123> send a welcome email to me, subject Welcome!, with 3 paragraphs describing why you love your job, button is Our Company, link to https://makeacompany.ai`
	action, matched, err := ParseSendEmailAction(raw)
	if err != nil || !matched {
		t.Fatalf("parse: matched=%v err=%v", matched, err)
	}
	if strings.TrimSpace(action.CTAText) != "Our Company" {
		t.Fatalf("cta text=%q want Our Company", action.CTAText)
	}
	if strings.TrimSpace(action.CTAURL) != "https://makeacompany.ai" {
		t.Fatalf("cta url=%q want https://makeacompany.ai", action.CTAURL)
	}
	if strings.TrimSpace(action.Subject) != "Welcome!" {
		t.Fatalf("subject=%q want Welcome!", action.Subject)
	}
}

func TestInferConversationalLinkURL_LinkTo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw, want string
	}{
		{`foo link to https://example.com/path bar`, `https://example.com/path`},
		{`link to http://a.co`, `http://a.co`},
		{`url to https://makeacompany.ai`, `https://makeacompany.ai`},
		{`still supports link is https://x.y/z`, `https://x.y/z`},
	} {
		got := inferConversationalLinkURL(tc.raw)
		if got != tc.want {
			t.Fatalf("inferConversationalLinkURL(%q)=%q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseSendEmailPatch_RejectsBadRecipient(t *testing.T) {
	t.Parallel()
	_, matched, err := ParseSendEmailPatch("to: not-an-email")
	if !matched {
		t.Fatal("expected matched continuation fields")
	}
	if err == nil {
		t.Fatal("expected invalid recipient error")
	}
}
