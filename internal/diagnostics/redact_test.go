package diagnostics

import (
	"strings"
	"testing"
)

func TestRedactTextStripsProviderTokensHeadersAndSessionRefs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		bad  []string
	}{
		{
			name: "github and bearer",
			in:   "Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz012345 and token=sekrit",
			bad:  []string{"ghp_", "sekrit"},
		},
		{
			name: "jwt and openai",
			in:   "jwt=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0In0.signature and sk-abcdefghijklmnopqrstuvwxyz0123",
			bad:  []string{"eyJhbGciOi", "sk-abcdefgh"},
		},
		{
			name: "session ref",
			in:   "harness_session_ref=sess_abc123-xyz and session_id: abcdef",
			bad:  []string{"sess_abc123", "session_id: abcdef"},
		},
		{
			name: "slack and gitlab",
			in:   "xoxb-1234567890-abcdefghij and glpat-abcdefghijklmnopqrstuv",
			bad:  []string{"xoxb-", "glpat-"},
		},
		{
			name: "api header",
			in:   "X-Api-Key: super-secret-key-value",
			bad:  []string{"super-secret-key-value"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := RedactText(tc.in)
			for _, b := range tc.bad {
				if strings.Contains(out, b) {
					t.Fatalf("still contains %q in %q", b, out)
				}
			}
			if !strings.Contains(out, "[REDACTED]") {
				t.Fatalf("expected redaction marker in %q", out)
			}
		})
	}
}

func TestRedactURLStripsUserinfoAndSecrets(t *testing.T) {
	out := RedactURL("https://user:hunter2@example.com/x?api_token=abc&q=ok")
	if strings.Contains(out, "hunter2") || strings.Contains(out, "api_token=abc") || strings.Contains(out, "token=abc") {
		t.Fatalf("url not redacted: %q", out)
	}
}

func TestSanitizeEnvAndConfig(t *testing.T) {
	env := SanitizeEnvMap(map[string]string{
		"GITHUB_TOKEN":  "ghp_abcdefghijklmnopqrstuvwxyz012345",
		"TERM":          "xterm-256color",
		"CUSTOM_SECRET": "nope",
	})
	if env["GITHUB_TOKEN"] != "[REDACTED]" || env["CUSTOM_SECRET"] != "[REDACTED]" {
		t.Fatalf("env=%v", env)
	}
	if env["TERM"] != "xterm-256color" {
		t.Fatalf("term=%q", env["TERM"])
	}
	cfg := SanitizeConfigMap(map[string]any{
		"default_harness": "pi",
		"backend": map[string]any{
			"api_token": "secret-value",
			"site_url":  "https://user:pw@example.atlassian.net",
		},
		"notes": "token=leakplease",
	})
	backend := cfg["backend"].(map[string]any)
	if backend["api_token"] != "[REDACTED]" {
		t.Fatalf("token field=%v", backend["api_token"])
	}
	if strings.Contains(fmtStringLocal(backend["site_url"]), "pw") {
		t.Fatalf("url=%v", backend["site_url"])
	}
	if strings.Contains(fmtStringLocal(cfg["notes"]), "leakplease") {
		t.Fatalf("notes=%v", cfg["notes"])
	}
}

func fmtStringLocal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
