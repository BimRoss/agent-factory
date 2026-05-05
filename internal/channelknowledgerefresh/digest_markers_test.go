package channelknowledgerefresh

import "testing"

func TestStripDigestThreadMarkers(t *testing.T) {
	in := "- **U0**: hello <!--dac m=1.0-->\n  - **U1**: reply <!--dac m=1.1 t=1.0-->\n"
	want := "- **U0**: hello\n  - **U1**: reply\n"
	got := StripDigestThreadMarkers(in)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
