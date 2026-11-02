// redact.go — pattern-based secret redaction for log lines. Journal output
// routinely contains passwords/tokens from services that log their own
// config; every entry is scrubbed BEFORE it leaves the agent (phase 13
// criterion 3: "secret redaction test edilmeli").
//
// The patterns are deliberately CONSERVATIVE: they redact structured
// key/value pairs and well-known token shapes, and they redact the VALUE
// while keeping the KEY visible (so the line stays diagnosable).
package logops

import "regexp"

// redactPlaceholder replaces every captured secret value.
const redactPlaceholder = "[REDACTED]"

// Ordered redaction patterns. Each pattern captures the KEY part and
// replaces the VALUE part. All matches are case-insensitive on the key.
var redactPatterns = []struct {
	re *regexp.Regexp
}{
	// key=value / key: value / key value (quoted or bare token) for
	// secret-ish keys: password, passwd, pwd, secret, token, apikey,
	// api_key, auth, authorization, credential, passphrase, private_key.
	{
		regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|apikey|api_key|credential|credentials|passphrase|private_key|access_key|secret_key)(\s*[=:]\s*|\s+)("[^"\s]*"|'[^'\s]*'|[^\s,;}&]+)`),
	},
	// Authorization headers: Basic/Bearer/Digest <token>.
	{
		regexp.MustCompile(`(?i)\b(authorization\s*[=:]\s*)(basic|bearer|digest)\s+[^\s,;}&]+`),
	},
	// PEM private key headers — the whole line is a secret.
	{
		regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----.*`),
	},
	// AWS access key id shape.
	{
		regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),
	},
	// JWT-shaped tokens (three base64url segments).
	{
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	},
}

// RedactLine scrubs secret-looking material from one log line. Idempotent:
// applying it twice yields the same output.
func RedactLine(line string) string {
	out := line
	for _, p := range redactPatterns[:2] {
		out = p.re.ReplaceAllStringFunc(out, func(m string) string {
			// Replace only the value group (group 3) with the placeholder,
			// keeping the key (group 1) and separator (group 2) visible.
			loc := p.re.FindStringSubmatchIndex(m)
			if loc == nil || len(loc) < 8 {
				return redactPlaceholder
			}
			return m[:loc[6]] + redactPlaceholder
		})
	}
	// Whole-line patterns (PEM, AWS, JWT) — no groups to preserve.
	out = redactPatterns[2].re.ReplaceAllString(out, "-----PRIVATE KEY [REDACTED]-----")
	out = redactPatterns[3].re.ReplaceAllString(out, redactPlaceholder)
	out = redactPatterns[4].re.ReplaceAllString(out, redactPlaceholder)
	return out
}
