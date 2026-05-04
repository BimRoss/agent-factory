package runtime

import "testing"

func TestLoadGoogleDocsConfigForEmployee_PrefersEmployeeKeys(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "global-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "global-secret")
	t.Setenv("GOOGLE_REFRESH_TOKEN", "global-refresh")
	t.Setenv("JOANNE_GOOGLE_CLIENT_ID", "joanne-id")
	t.Setenv("JOANNE_GOOGLE_CLIENT_SECRET", "joanne-secret")
	t.Setenv("JOANNE_GOOGLE_REFRESH_TOKEN", "joanne-refresh")

	cfg := LoadGoogleDocsConfigForEmployee("joanne")
	if cfg.ClientID != "joanne-id" || cfg.ClientSecret != "joanne-secret" || cfg.RefreshToken != "joanne-refresh" {
		t.Fatalf("expected employee-prefixed config, got %+v", cfg)
	}
}

func TestLoadGoogleDocsConfigForEmployee_FallsBackToGlobal(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "global-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "global-secret")
	t.Setenv("GOOGLE_REFRESH_TOKEN", "global-refresh")

	cfg := LoadGoogleDocsConfigForEmployee("ross")
	if cfg.ClientID != "global-id" || cfg.ClientSecret != "global-secret" || cfg.RefreshToken != "global-refresh" {
		t.Fatalf("expected global fallback config, got %+v", cfg)
	}
}

func TestGoogleDocsEnvConfigValidate(t *testing.T) {
	good := GoogleDocsEnvConfig{
		ClientID:     "id",
		ClientSecret: "secret",
		RefreshToken: "refresh",
	}
	if err := good.Validate("joanne"); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	if err := (GoogleDocsEnvConfig{ClientSecret: "x", RefreshToken: "y"}).Validate("joanne"); err == nil {
		t.Fatalf("expected missing client id error")
	}
	if err := (GoogleDocsEnvConfig{ClientID: "x", RefreshToken: "y"}).Validate("joanne"); err == nil {
		t.Fatalf("expected missing client secret error")
	}
	if err := (GoogleDocsEnvConfig{ClientID: "x", ClientSecret: "y"}).Validate("joanne"); err == nil {
		t.Fatalf("expected missing refresh token error")
	}
}
