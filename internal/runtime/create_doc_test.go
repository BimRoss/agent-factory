package runtime

import "testing"

func TestParseCreateDocRequest(t *testing.T) {
	raw := "title: Iran Update; summarize this argument in a 5 page google doc; share with editor grant@example.com; commenters: ops@example.com; viewers: read@example.com"
	req := parseCreateDocRequest(raw)

	if req.Title != "Iran Update" {
		t.Fatalf("title=%q", req.Title)
	}
	if req.Pages != 5 {
		t.Fatalf("pages=%d", req.Pages)
	}
	if len(req.Editors) != 1 || req.Editors[0] != "grant@example.com" {
		t.Fatalf("editors=%v", req.Editors)
	}
	if len(req.Commenters) != 1 || req.Commenters[0] != "ops@example.com" {
		t.Fatalf("commenters=%v", req.Commenters)
	}
	if len(req.Viewers) != 1 || req.Viewers[0] != "read@example.com" {
		t.Fatalf("viewers=%v", req.Viewers)
	}
}

func TestParseCreateDocRequest_ExtractsBody(t *testing.T) {
	raw := "create doc body: This is the full body to use."
	req := parseCreateDocRequest(raw)
	if req.Body != "This is the full body to use." {
		t.Fatalf("body=%q", req.Body)
	}
}

func TestDefaultCreateDocTitle(t *testing.T) {
	title := defaultCreateDocTitle(Task{})
	if title == "" {
		t.Fatal("expected non-empty title")
	}
}
