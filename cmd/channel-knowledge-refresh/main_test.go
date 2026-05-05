package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bimross/agent-factory/internal/companychannel"
)

func TestPostKnowledgeRefreshFailureMessage(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("redis: connection refused")
	msg := formatKnowledgeRefreshFailureMessage("#bimross", err, "")
	if !strings.HasPrefix(msg, "❗ ") {
		t.Fatalf("expected leading heavy exclamation, got %q", msg)
	}
	if !strings.Contains(msg, "redis: connection refused") {
		t.Fatalf("expected error text in message: %q", msg)
	}
	if !strings.Contains(msg, "#bimross") {
		t.Fatalf("expected channel label: %q", msg)
	}
}

func TestRegistryNotifyLabel(t *testing.T) {
	t.Parallel()
	meta := map[string]companychannel.CompanyChannelRuntime{
		"C1": {DisplayName: "#humans", CompanySlug: "humans"},
		"C2": {DisplayName: "Make A Company", CompanySlug: "bimross"},
		"C3": {CompanySlug: "acme"},
		"C4": {DisplayName: "LegacyCo"},
	}
	tests := []struct {
		id   string
		want string
	}{
		{"C1", "#humans"},
		{"C2", "#bimross"},
		{"C3", "#acme"},
		{"C4", "#legacyco"},
		{"Cmissing", ""},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			if got := registryNotifyLabel(meta, tc.id); got != tc.want {
				t.Fatalf("registryNotifyLabel(meta, %q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
