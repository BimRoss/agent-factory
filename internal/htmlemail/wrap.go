// Package htmlemail wraps HTML fragments for MIME email (parity with employee-factory).
package htmlemail

import "strings"

// WrapMessageIfFragment wraps a body fragment in a full HTML5 document with light
// color-scheme metadata when needed.
func WrapMessageIfFragment(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if looksLikeFullHTMLDocument(body) {
		return body
	}
	return buildShell(body)
}

func looksLikeFullHTMLDocument(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(low, "<!doctype html") {
		return true
	}
	if strings.HasPrefix(low, "<html") {
		return true
	}
	return false
}

func buildShell(inner string) string {
	return strings.TrimSpace(
		`<!doctype html>
<html lang="en" xmlns="http://www.w3.org/1999/xhtml" style="color-scheme: light;">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width" />
<meta name="color-scheme" content="light" />
<meta name="supported-color-schemes" content="light" />
<style type="text/css">html,body{background-color:#ffffff !important;}html{color-scheme: light only;}</style>
</head>
<body style="margin:0;padding:0;-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%;background-color:#ffffff;">` + inner + `
</body>
</html>`)
}
