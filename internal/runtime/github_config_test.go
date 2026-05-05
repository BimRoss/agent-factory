package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLoadGitHubConfigTokenPersonalPath(t *testing.T) {
	t.Setenv("ROSS_PERSONAL_GH_TOKEN", "user-pat")
	t.Setenv("ROSS_ORG_GH_TOKEN", "org-pat-should-be-ignored")
	cfg := LoadGitHubConfigForEmployee("ross")
	if cfg.Token != "user-pat" {
		t.Fatalf("expected ROSS_PERSONAL_GH_TOKEN (org slot no longer used), got %q", cfg.Token)
	}
}

func TestLoadGitHubConfigExplicitGithubTokenWins(t *testing.T) {
	t.Setenv("ROSS_GITHUB_TOKEN", "explicit")
	t.Setenv("ROSS_PERSONAL_GH_TOKEN", "user-pat")
	cfg := LoadGitHubConfigForEmployee("ross")
	if cfg.Token != "explicit" {
		t.Fatalf("expected ROSS_GITHUB_TOKEN to win, got %q", cfg.Token)
	}
}

func TestResolveGitHubOwnerPrefersConfiguredOwner(t *testing.T) {
	cfg := GitHubEnvConfig{
		Token: "token",
		Owner: "ConfiguredOrg",
	}
	if got := ResolveGitHubOwner(context.Background(), cfg); got != "ConfiguredOrg" {
		t.Fatalf("expected configured owner, got %q", got)
	}
}

func TestResolveGitHubOwnerUsesAuthenticatedUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"token-user-login"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prev := os.Getenv("GITHUB_API_BASE_URL")
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)
	defer func() { _ = os.Setenv("GITHUB_API_BASE_URL", prev) }()

	cfg := GitHubEnvConfig{Token: "token"}
	if got := ResolveGitHubOwner(context.Background(), cfg); got != "token-user-login" {
		t.Fatalf("expected /user login, got %q", got)
	}
}

func TestResolveGitHubOwnerFallsBackEmptyOnUserEndpointFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	prev := os.Getenv("GITHUB_API_BASE_URL")
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)
	defer func() { _ = os.Setenv("GITHUB_API_BASE_URL", prev) }()

	cfg := GitHubEnvConfig{Token: "token"}
	if got := ResolveGitHubOwner(context.Background(), cfg); got != "" {
		t.Fatalf("expected empty owner when /user fails, got %q", got)
	}
}

func TestResolveGitHubOwnerWithScopeFallsBackToUserLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"solo-user"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	prev := os.Getenv("GITHUB_API_BASE_URL")
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)
	defer func() { _ = os.Setenv("GITHUB_API_BASE_URL", prev) }()

	cfg := GitHubEnvConfig{Token: "token"}
	owner, scope := ResolveGitHubOwnerWithScope(context.Background(), cfg)
	if owner != "solo-user" || scope != "user" {
		t.Fatalf("expected solo-user/user, got %q/%q", owner, scope)
	}
}

func TestProbeGitHubAccessMissingToken(t *testing.T) {
	t.Setenv("ROSS_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("ROSS_PERSONAL_GH_TOKEN", "")
	t.Setenv("PERSONAL_GH_TOKEN", "")
	probe := ProbeGitHubAccess(context.Background(), "ross")
	if probe.TokenConfigured {
		t.Fatalf("expected token to be missing")
	}
	if probe.Warning == "" {
		t.Fatalf("expected warning for missing token")
	}
}

func TestProbeGitHubAccessOrgScopeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orgs/BimRoss/repos":
			q := r.URL.Query()
			if q.Get("type") != "all" || q.Get("per_page") != "1" {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/user":
			_, _ = w.Write([]byte(`{"login":"fallback-user"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prevBase := os.Getenv("GITHUB_API_BASE_URL")
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)
	defer func() { _ = os.Setenv("GITHUB_API_BASE_URL", prevBase) }()

	t.Setenv("ROSS_GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_OWNER", "BimRoss")
	defer func() { _ = os.Unsetenv("GITHUB_OWNER") }()
	probe := ProbeGitHubAccess(context.Background(), "ross")
	if !probe.TokenConfigured {
		t.Fatalf("expected token configured")
	}
	if !probe.ListScopeOK {
		t.Fatalf("expected scope list check success, got err=%s warning=%s", probe.Error, probe.Warning)
	}
	if probe.Owner != "BimRoss" || probe.Scope != "org" {
		t.Fatalf("expected BimRoss/org when GITHUB_OWNER is set, got %s/%s", probe.Owner, probe.Scope)
	}
}

func TestResolveGitHubOwnerWithScopeInfersUserWhenOwnerMatchesLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"solo-user"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	prev := os.Getenv("GITHUB_API_BASE_URL")
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)
	defer func() { _ = os.Setenv("GITHUB_API_BASE_URL", prev) }()

	cfg := GitHubEnvConfig{Token: "token", Owner: "solo-user", OwnerScope: ""}
	owner, scope := ResolveGitHubOwnerWithScope(context.Background(), cfg)
	if owner != "solo-user" || scope != "user" {
		t.Fatalf("expected solo-user/user for matching login, got %q/%q", owner, scope)
	}
}

func TestResolveGitHubOwnerWithScopeInfersOrgWhenOwnerIsNotLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"login":"solo-user"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	prev := os.Getenv("GITHUB_API_BASE_URL")
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)
	defer func() { _ = os.Setenv("GITHUB_API_BASE_URL", prev) }()

	cfg := GitHubEnvConfig{Token: "token", Owner: "BimRoss", OwnerScope: ""}
	owner, scope := ResolveGitHubOwnerWithScope(context.Background(), cfg)
	if owner != "BimRoss" || scope != "org" {
		t.Fatalf("expected BimRoss/org when owner differs from login, got %q/%q", owner, scope)
	}
}

func TestResolveGitHubOwnerWithScopeRespectsExplicitUserScope(t *testing.T) {
	cfg := GitHubEnvConfig{Token: "token", Owner: "some-org-name", OwnerScope: "user"}
	owner, scope := ResolveGitHubOwnerWithScope(context.Background(), cfg)
	if owner != "some-org-name" || scope != "user" {
		t.Fatalf("expected explicit user scope, got %q/%q", owner, scope)
	}
}

func TestProbeGitHubAccessUserScopeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"solo-user"}`))
		case "/user/repos":
			http.Error(w, `{"message":"Requires authentication"}`, http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prevBase := os.Getenv("GITHUB_API_BASE_URL")
	_ = os.Setenv("GITHUB_API_BASE_URL", srv.URL)
	defer func() { _ = os.Setenv("GITHUB_API_BASE_URL", prevBase) }()

	t.Setenv("ROSS_GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_OWNER", "")
	probe := ProbeGitHubAccess(context.Background(), "ross")
	if !probe.TokenConfigured {
		t.Fatalf("expected token configured")
	}
	if probe.ListScopeOK {
		t.Fatalf("expected scope list check failure")
	}
	if probe.Warning == "" || probe.Error == "" {
		t.Fatalf("expected warning and error when scope listing fails")
	}
}
