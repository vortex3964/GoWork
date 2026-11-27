// DESC: child-environment scrubbing for shell commands. Bash is approved
// per call, but the approved command should not be able to print the agent's
// provider credentials just by running `env`.
package bashtool

import "strings"

// sensitiveEnvSegments are environment-name segments that mark a value as a
// credential. Matching whole underscore/dash/dot-separated segments keeps
// harmless names such as MONKEY or COMPASS while catching the usual forms:
// API_KEY, ANTHROPIC_API_KEY, GITHUB_TOKEN, DB_PASSWORD, AWS_ACCESS_KEY_ID, ...
var sensitiveEnvSegments = map[string]struct{}{
	"KEY":         {},
	"KEYS":        {},
	"TOKEN":       {},
	"TOKENS":      {},
	"SECRET":      {},
	"SECRETS":     {},
	"PASSWORD":    {},
	"PASSWORDS":   {},
	"PASSWD":      {},
	"PASSPHRASE":  {},
	"CREDENTIAL":  {},
	"CREDENTIALS": {},
	"APIKEY":      {},
	"PRIVATEKEY":  {},
	"ACCESSKEY":   {},
	"SECRETKEY":   {},
}

// sensitiveEnvMarkers cover concatenated spellings such as PGPASSWORD that
// have no separator for segment matching to find.
var sensitiveEnvMarkers = []string{
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"APIKEY",
	"ACCESSKEY",
	"PRIVATEKEY",
	"SECRETKEY",
}

// shellStartupVars can make bash source an arbitrary file before running the
// command, which would let a child re-import values the scrubber removed.
var shellStartupVars = map[string]struct{}{
	"BASH_ENV": {},
	"ENV":      {},
}

// isSensitiveEnvName reports whether name looks like a credential variable.
// It is a name-based heuristic: values can still be present under an
// unrelated name, and the agent's .env file is readable from the project.
func isSensitiveEnvName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}

	segments := strings.FieldsFunc(upper, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for _, segment := range segments {
		if _, ok := sensitiveEnvSegments[segment]; ok {
			return true
		}
	}
	for _, marker := range sensitiveEnvMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// sanitizedEnv returns environ minus credential-looking variables and shell
// startup hooks. Everything else (PATH, HOME, locale, terminal and other
// system settings) is preserved so commands keep working normally.
func sanitizedEnv(environ []string) []string {
	clean := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			clean = append(clean, entry)
			continue
		}
		if _, drop := shellStartupVars[strings.ToUpper(strings.TrimSpace(name))]; drop {
			continue
		}
		if isSensitiveEnvName(name) {
			continue
		}
		clean = append(clean, entry)
	}
	return clean
}
