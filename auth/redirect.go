package auth

import (
	"net/url"
	"strings"
)

// SanitizeReturnTo validates and canonicalizes a return_to URL to prevent open redirect
// and token-leakage vulnerabilities. Return targets are strictly restricted to relative paths
// and origins matching the authoritative appURL. External and custom domains are always rejected.
// If returnTo is empty, invalid, or unsafe, fallbackPath is used (defaulting to "/" if not provided).
func SanitizeReturnTo(returnTo, appURL string, fallbackPath ...string) string {
	defaultPath := "/"
	if len(fallbackPath) > 0 && strings.TrimSpace(fallbackPath[0]) != "" {
		defaultPath = strings.TrimSpace(fallbackPath[0])
	}
	if !strings.HasPrefix(defaultPath, "/") {
		defaultPath = "/" + defaultPath
	}

	defaultTarget := defaultPath
	var appU *url.URL
	if strings.TrimSpace(appURL) != "" {
		trimmedAppURL := strings.TrimRight(strings.TrimSpace(appURL), "/")
		defaultTarget = trimmedAppURL + defaultPath
		if parsed, err := url.Parse(trimmedAppURL); err == nil {
			appU = parsed
		}
	}

	trimmed := strings.TrimSpace(returnTo)
	if trimmed == "" {
		return defaultTarget
	}

	// Reject control characters, tabs, newlines, and backslashes to prevent open redirects or protocol smuggling
	if strings.ContainsAny(trimmed, "\r\n\t\\") {
		return defaultTarget
	}

	// Safe relative paths starting with a single '/'
	if strings.HasPrefix(trimmed, "/") {
		if strings.HasPrefix(trimmed, "//") {
			return defaultTarget
		}
		if appURL != "" {
			return strings.TrimRight(appURL, "/") + trimmed
		}
		return trimmed
	}

	// Absolute URLs: must match appURL host and scheme exactly
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return defaultTarget
	}

	if appU != nil {
		if strings.EqualFold(u.Host, appU.Host) {
			if appU.Scheme == "https" && u.Scheme != "https" {
				return defaultTarget
			}
			if u.Scheme == "http" || u.Scheme == "https" {
				return trimmed
			}
		}
	}

	return defaultTarget
}
