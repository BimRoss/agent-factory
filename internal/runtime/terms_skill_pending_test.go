package runtime

import "testing"

func TestTermsSkillConfirmationRedisKeyEmployeeFactoryShape(t *testing.T) {
	const want = "agent-factory:skill_confirmation:terms_accept|C0CHANNEL|U0OPERATOR|1234567890.123456"
	got := TermsSkillConfirmationRedisKey("C0CHANNEL", "U0OPERATOR", "1234567890.123456")
	if got != want {
		t.Fatalf("redis key mismatch\ngot:  %q\nwant: %q", got, want)
	}
}
