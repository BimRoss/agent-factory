package channelknowledge

import "testing"

func TestSlackTSCompare(t *testing.T) {
	t.Parallel()
	if SlackTSCompare("100.0", "99.999999") <= 0 {
		t.Fatal("expected 100.0 > 99.999999")
	}
	if SlackTSCompare("100.0", "100.0") != 0 {
		t.Fatal("equal")
	}
	if SlackTSMax("1.0", "2.0") != "2.0" {
		t.Fatalf("SlackTSMax got %q", SlackTSMax("1.0", "2.0"))
	}
}

func TestCapThreadCursors(t *testing.T) {
	t.Parallel()
	m := map[string]string{
		"10.0": "10.1",
		"20.0": "20.1",
		"30.0": "30.1",
	}
	CapThreadCursors(m, 2)
	if len(m) != 2 {
		t.Fatalf("want 2 entries got %d", len(m))
	}
	if _, ok := m["30.0"]; !ok {
		t.Fatal("expected newest root keys to survive")
	}
}
