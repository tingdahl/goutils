package auth

import (
	"testing"
)

func TestSanitizeReturnTo(t *testing.T) {
	appURL := "https://example.com"
	defaultTarget := "https://example.com/console.html"

	tests := []struct {
		name         string
		returnTo     string
		appURL       string
		fallbackPath string
		expected     string
	}{
		{
			name:         "empty returns fallback with appURL",
			returnTo:     "",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     defaultTarget,
		},
		{
			name:         "default fallback when empty",
			returnTo:     "",
			appURL:       appURL,
			fallbackPath: "",
			expected:     "https://example.com/",
		},
		{
			name:         "relative path prefixed with appURL",
			returnTo:     "/dashboard",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     "https://example.com/dashboard",
		},
		{
			name:         "relative path without appURL",
			returnTo:     "/dashboard",
			appURL:       "",
			fallbackPath: "/console.html",
			expected:     "/dashboard",
		},
		{
			name:         "protocol relative double slash rejected",
			returnTo:     "//attacker.com/steal",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     defaultTarget,
		},
		{
			name:         "backslash rejected",
			returnTo:     "/\\attacker.com/steal",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     defaultTarget,
		},
		{
			name:         "tab rejected",
			returnTo:     "/\t/attacker.com",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     defaultTarget,
		},
		{
			name:         "newline rejected",
			returnTo:     "/\r\n/attacker.com",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     defaultTarget,
		},
		{
			name:         "external domain rejected",
			returnTo:     "https://attacker.com/steal",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     defaultTarget,
		},
		{
			name:         "insecure http rejected when appURL is https",
			returnTo:     "http://example.com/console.html",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     defaultTarget,
		},
		{
			name:         "exact appURL match allowed",
			returnTo:     "https://example.com/custom?foo=bar",
			appURL:       appURL,
			fallbackPath: "/console.html",
			expected:     "https://example.com/custom?foo=bar",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeReturnTo(tc.returnTo, tc.appURL, tc.fallbackPath)
			if got != tc.expected {
				t.Errorf("SanitizeReturnTo(%q, %q, %q) = %q, expected %q", tc.returnTo, tc.appURL, tc.fallbackPath, got, tc.expected)
			}
		})
	}
}
