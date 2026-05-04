package runtime

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGoogleDocsClient_CreateAndGrantEditor(t *testing.T) {
	client := &GoogleDocsClient{
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPost && req.URL.String() == docsCreateEndpointBase:
					return responseJSON(200, `{"documentId":"doc-123","title":"War Summary"}`), nil
				case req.Method == http.MethodPost && strings.Contains(req.URL.String(), "/documents/doc-123:batchUpdate"):
					return responseJSON(200, `{"replies":[]}`), nil
				case req.Method == http.MethodPost && strings.Contains(req.URL.String(), "/files/doc-123/permissions"):
					return responseJSON(200, `{"id":"perm-1"}`), nil
				default:
					return responseJSON(500, `{"error":"unexpected request"}`), nil
				}
			}),
		},
	}

	res, err := client.Create(context.Background(), GoogleDocsCreateInput{
		Title: "War Summary",
		Body:  "Body text",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.DocumentID != "doc-123" {
		t.Fatalf("document id=%q", res.DocumentID)
	}
	if !strings.Contains(res.URL, "doc-123") {
		t.Fatalf("url=%q", res.URL)
	}

	if err := client.GrantEditor(context.Background(), "doc-123", "grant@example.com"); err != nil {
		t.Fatalf("grant editor failed: %v", err)
	}
}

func TestGoogleDocsClient_GrantEditorRejectsInvalidEmail(t *testing.T) {
	client := &GoogleDocsClient{http: &http.Client{}}
	if err := client.GrantEditor(context.Background(), "doc-123", "not-an-email"); err == nil {
		t.Fatal("expected invalid email error")
	}
}

func responseJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
