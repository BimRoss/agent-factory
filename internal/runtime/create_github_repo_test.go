package runtime

import "testing"

func TestParsePrivateFlag(t *testing.T) {
	if _, set := parsePrivateFlag(""); set {
		t.Fatal("expect unset for empty")
	}
	if p, set := parsePrivateFlag("YES"); !p || !set {
		t.Fatal("yes")
	}
	if p, _ := parsePrivateFlag("public"); p {
		t.Fatal("public should be explicit false")
	}
}

func TestInferRepoNameFragment(t *testing.T) {
	if got := inferRepoNameFragment(`create github repo called infra-smoke-helper`); got != `infra-smoke-helper` {
		t.Fatalf("called phrase: got %q", got)
	}
	if got := inferRepoNameFragment(`something "QuotedRepo"` + ` trailing`); got != `QuotedRepo` {
		t.Fatalf("quoted: got %q", got)
	}
}
