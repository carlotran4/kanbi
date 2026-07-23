package diagnostics

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Shared credential matching. Keep aligned with storage.RedactSecretText intent
// and extended for support-bundle config/env/path sanitization.
var (
	redactBearerRE   = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)(\S+)`)
	redactBasicRE    = regexp.MustCompile(`(?i)(authorization:\s*basic\s+)(\S+)`)
	redactHeaderRE   = regexp.MustCompile(`(?i)((?:x-)?api[-_]?key|x-access-token)\s*[:=]\s*(\S+)`)
	redactTokenEqRE  = regexp.MustCompile(`(?i)((?:api[_-]?token|access[_-]?token|refresh[_-]?token|bearer[_-]?token|github[_-]?token|jira[_-]?api[_-]?token|token|password|secret|client[_-]?secret)=)([^\s&;,]+)`)
	redactGHTokenRE  = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)
	redactGLTokenRE  = regexp.MustCompile(`\bglpat-[A-Za-z0-9\-_]{20,}\b`)
	redactSlackTokRE = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	redactJWTRE      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	redactOpenAIRE   = regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)
	redactSessionRef = regexp.MustCompile(`(?i)((?:session[_-]?ref|harness[_-]?session[_-]?ref|session[_-]?id)\s*[:=]\s*)([^\s,;"']+)`)
)

// SensitiveEnvKeys lists environment variable names that must never appear in support bundles.
var SensitiveEnvKeys = []string{
	"KANBI_GITHUB_TOKEN",
	"GITHUB_TOKEN",
	"GH_TOKEN",
	"KANBI_JIRA_API_TOKEN",
	"JIRA_API_TOKEN",
	"ATLASSIAN_API_TOKEN",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"COPILOT_GITHUB_TOKEN",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AZURE_CLIENT_SECRET",
	"GOOGLE_APPLICATION_CREDENTIALS",
}

// RedactText removes credential-shaped substrings, auth headers, token formats,
// and harness session-ref shaped values from free text.
func RedactText(s string) string {
	if s == "" {
		return s
	}
	out := s
	out = redactBearerRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactBasicRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactHeaderRE.ReplaceAllString(out, `${1}=[REDACTED]`)
	out = redactTokenEqRE.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactSessionRef.ReplaceAllString(out, `${1}[REDACTED]`)
	out = redactGHTokenRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactGLTokenRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactSlackTokRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactJWTRE.ReplaceAllString(out, `[REDACTED]`)
	out = redactOpenAIRE.ReplaceAllString(out, `[REDACTED]`)
	return out
}

// RedactURL strips userinfo and query secret parameters from a URL string.
func RedactURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return RedactText(raw)
	}
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass {
			u.User = url.UserPassword(u.User.Username(), "[REDACTED]")
		} else if u.User.Username() != "" {
			u.User = url.User("[REDACTED]")
		}
	}
	if u.RawQuery != "" {
		q := u.Query()
		for key := range q {
			lk := strings.ToLower(key)
			if strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "password") || strings.Contains(lk, "key") || strings.Contains(lk, "auth") {
				q.Set(key, "[REDACTED]")
			} else {
				for i, v := range q[key] {
					q[key][i] = RedactText(v)
				}
			}
		}
		u.RawQuery = q.Encode()
	}
	return RedactText(u.String())
}

// RedactCommandPath hides configured command paths while preserving bare command
// names. Support bundles only need to report harness presence, so executable
// paths and their private directory structure are never useful diagnostics.
func RedactCommandPath(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return command
	}
	if filepath.IsAbs(command) || strings.HasPrefix(command, "~/") || strings.HasPrefix(command, "./") || strings.HasPrefix(command, "../") || strings.Contains(command, "/") || strings.Contains(command, "\\") {
		return "[REDACTED PATH]"
	}
	return RedactText(command)
}

// RedactPath replaces the user home directory prefix with ~ when present.
// Absolute paths outside home are left as-is except credential patterns.
func RedactPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		cleanHome := filepath.Clean(home)
		cleanPath := filepath.Clean(path)
		if cleanPath == cleanHome {
			return "~"
		}
		prefix := cleanHome + string(filepath.Separator)
		if strings.HasPrefix(cleanPath, prefix) {
			return "~" + string(filepath.Separator) + strings.TrimPrefix(cleanPath, prefix)
		}
		if strings.HasPrefix(path, home) {
			return "~" + strings.TrimPrefix(path, home)
		}
	}
	return RedactText(path)
}

// SanitizeEnvMap copies selected keys, redacting values for sensitive keys and
// running RedactText on remaining values.
func SanitizeEnvMap(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	sensitive := make(map[string]struct{}, len(SensitiveEnvKeys))
	for _, k := range SensitiveEnvKeys {
		sensitive[strings.ToUpper(k)] = struct{}{}
	}
	for k, v := range env {
		uk := strings.ToUpper(k)
		if _, ok := sensitive[uk]; ok {
			out[k] = "[REDACTED]"
			continue
		}
		if strings.Contains(uk, "TOKEN") || strings.Contains(uk, "SECRET") || strings.Contains(uk, "PASSWORD") || strings.HasSuffix(uk, "_KEY") || strings.Contains(uk, "CREDENTIAL") {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = RedactText(v)
	}
	return out
}

// SanitizeConfigMap removes secret-looking YAML/config leaves by key name and
// redacts URLs/paths/credential substrings elsewhere.
func SanitizeConfigMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = sanitizeConfigValue(k, v)
	}
	return out
}

func sanitizeConfigValue(key string, v any) any {
	lk := strings.ToLower(key)
	secretKey := strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "password") ||
		strings.Contains(lk, "authorization") || strings.HasSuffix(lk, "_key") || lk == "api_key" || lk == "apikey"
	switch t := v.(type) {
	case map[string]any:
		return SanitizeConfigMap(t)
	case map[any]any:
		converted := make(map[string]any, len(t))
		for mk, mv := range t {
			converted[fmt.Sprint(mk)] = mv
		}
		return SanitizeConfigMap(converted)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = sanitizeConfigValue(key, item)
		}
		return out
	case string:
		if secretKey {
			return "[REDACTED]"
		}
		if lk == "start" || lk == "start_with_prompt" || lk == "resume" {
			return RedactCommandPath(t)
		}
		if strings.Contains(lk, "url") || strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
			return RedactURL(t)
		}
		if filepath.IsAbs(t) || strings.HasPrefix(t, "~/") || t == "~" {
			return RedactPath(t)
		}
		return RedactText(t)
	default:
		if secretKey {
			return "[REDACTED]"
		}
		return v
	}
}
