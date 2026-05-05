package channelknowledgerefresh

import "regexp"

// digestThreadMarkerRE matches HTML comment markers appended by the refresh job for admin UI
// thread grouping (MakeACompany transcript). Stripped before channel digest is sent to LLMs.
var digestThreadMarkerRE = regexp.MustCompile(`\s*<!--dac m=[\d.]+(?: t=[\d.]+)?-->`)

// StripDigestThreadMarkers removes machine-readable thread markers from digest markdown.
func StripDigestThreadMarkers(s string) string {
	return digestThreadMarkerRE.ReplaceAllString(s, "")
}
