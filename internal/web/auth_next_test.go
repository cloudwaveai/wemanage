package web

import "testing"

func TestSafeNext(t *testing.T) {
	for p, want := range map[string]bool{
		"/devices":              true,
		"/logs?from=2026-09-01": true,
		"":                      false,
		"devices":               false,
		"//evil.example":        false,
		"/\\evil.example":       false,
		"https://evil.example":  false,
		"/api/auth/logout":      false,
		"/x\r\nSet-Cookie: a":   false,
	} {
		if got := safeNext(p); got != want {
			t.Errorf("safeNext(%q) = %v, want %v", p, got, want)
		}
	}
}
