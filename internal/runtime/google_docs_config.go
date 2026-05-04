package runtime

import (
	"fmt"
	"os"
	"strings"
)

// GoogleDocsEnvConfig holds OAuth refresh-token credentials for Google Docs + Drive.
// Resolution order matches existing employee overrides: EMPLOYEE_* first, then global.
type GoogleDocsEnvConfig struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
}

func (c GoogleDocsEnvConfig) Validate(employeeID string) error {
	emp := strings.ToUpper(strings.TrimSpace(employeeID))
	if emp == "" {
		emp = "EMPLOYEE"
	}
	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("create-doc: missing Google OAuth client id (set %s_GOOGLE_CLIENT_ID or GOOGLE_CLIENT_ID)", emp)
	}
	if strings.TrimSpace(c.ClientSecret) == "" {
		return fmt.Errorf("create-doc: missing Google OAuth client secret (set %s_GOOGLE_CLIENT_SECRET or GOOGLE_CLIENT_SECRET)", emp)
	}
	if strings.TrimSpace(c.RefreshToken) == "" {
		return fmt.Errorf("create-doc: missing Google OAuth refresh token (set %s_GOOGLE_REFRESH_TOKEN or GOOGLE_REFRESH_TOKEN)", emp)
	}
	return nil
}

func LoadGoogleDocsConfigForEmployee(employeeID string) GoogleDocsEnvConfig {
	emp := strings.ToUpper(strings.TrimSpace(employeeID))
	prefix := emp + "_"
	return GoogleDocsEnvConfig{
		ClientID: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_CLIENT_ID"),
			os.Getenv("GOOGLE_CLIENT_ID"),
		)),
		ClientSecret: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_CLIENT_SECRET"),
			os.Getenv("GOOGLE_CLIENT_SECRET"),
		)),
		RefreshToken: strings.TrimSpace(firstNonEmpty(
			os.Getenv(prefix+"GOOGLE_REFRESH_TOKEN"),
			os.Getenv("GOOGLE_REFRESH_TOKEN"),
		)),
	}
}
