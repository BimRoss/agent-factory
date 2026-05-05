package runtime

import (
	"strings"
	"testing"
)

func TestLooksLikeMultiSpeakerReply(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{
			name: "plain single speaker",
			in:   "We should pick one option, define done, and run the smallest test this week.",
			want: false,
		},
		{
			name: "teammate labels",
			in:   "Teammate A: push this now.\nTeammate B: hold for evidence.",
			want: true,
		},
		{
			name: "colleague typo label",
			in:   "Colleage A: yes.\nColleague B: no.",
			want: true,
		},
		{
			name: "side numbered label",
			in:   "Side 1: short-term win.\nSide 2: long-term risk.",
			want: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := looksLikeMultiSpeakerReply(tc.in); got != tc.want {
				t.Fatalf("looksLikeMultiSpeakerReply()=%v want %v input=%q", got, tc.want, tc.in)
			}
		})
	}
}

func TestConversationToneConstraintsIncludesSingleSpeakerRule(t *testing.T) {
	t.Parallel()
	got := conversationToneConstraints()
	if got == "" {
		t.Fatal("conversationToneConstraints must not be empty")
	}
	for _, needle := range []string{"one speaker", "Teammate A", "Colleague B"} {
		if strings.Contains(got, needle) {
			continue
		}
		t.Fatalf("conversationToneConstraints missing single-speaker guard: %q", got)
	}
}
