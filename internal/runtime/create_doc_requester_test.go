package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestInjectCreateDocRequesterEditor_AppendsResolvedEmail(t *testing.T) {
	req := createDocRequest{
		Editors: []string{"already@example.com"},
	}
	resolver := func(ctx context.Context, task Task) (string, string, error) {
		return "requester@example.com", "test_source", nil
	}

	email, source, err := injectCreateDocRequesterEditor(context.Background(), &req, Task{HumanUserID: "U123"}, resolver)
	if err != nil {
		t.Fatalf("injectCreateDocRequesterEditor error: %v", err)
	}
	if email != "requester@example.com" {
		t.Fatalf("email=%q", email)
	}
	if source != "test_source" {
		t.Fatalf("source=%q", source)
	}
	if len(req.Editors) != 2 {
		t.Fatalf("editors=%v", req.Editors)
	}
	if req.Editors[1] != "requester@example.com" {
		t.Fatalf("editors=%v", req.Editors)
	}
}

func TestInjectCreateDocRequesterEditor_ResolverErrorDoesNotMutateEditors(t *testing.T) {
	req := createDocRequest{
		Editors: []string{"already@example.com"},
	}
	wantErr := errors.New("lookup failed")
	resolver := func(ctx context.Context, task Task) (string, string, error) {
		return "", "", wantErr
	}

	_, _, err := injectCreateDocRequesterEditor(context.Background(), &req, Task{HumanUserID: "U123"}, resolver)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v want=%v", err, wantErr)
	}
	if len(req.Editors) != 1 || req.Editors[0] != "already@example.com" {
		t.Fatalf("editors mutated: %v", req.Editors)
	}
}
