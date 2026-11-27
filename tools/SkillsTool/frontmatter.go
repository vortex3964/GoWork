package skillstool

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// frontmatterInfo is the small YAML subset the skills format understands.
// Body text is deliberately not stored here; discovery stays metadata-only.
type frontmatterInfo struct {
	name           string
	description    string
	closed         bool
	hasName        bool
	hasDescription bool
}

// parseFrontmatter reads a leading "---" block. In strict mode a missing or
// unterminated block is an error; discovery stays tolerant so a malformed
// file in the skills folder can never crash the tab.
//
// Supported description values are plain scalars (including indented
// continuations), single/double quoted scalars, and folded (>) or literal (|)
// block scalars with the usual -/+ chomping indicators.
func parseFrontmatter(r *bufio.Reader, strict bool) (frontmatterInfo, bool) {
	var info frontmatterInfo

	first, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return info, false
	}
	first = strings.TrimPrefix(first, "\ufeff")
	if strings.TrimSpace(first) != "---" {
		// No frontmatter at all. Discovery tolerates it; installs do not.
		return info, !strict
	}

	var blockMode byte // '>' folded, '|' literal
	var blockLines []string
	active := ""

	flushBlock := func() {
		if blockMode == 0 {
			return
		}
		info.description = strings.TrimSpace(renderBlockScalar(blockLines, blockMode))
		info.hasDescription = true
		blockMode = 0
		blockLines = nil
		active = ""
	}

	for {
		raw, readErr := r.ReadString('\n')
		if raw == "" && readErr != nil {
			break
		}
		raw = strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")

		if blockMode != 0 {
			if leadingIndent(raw) == 0 && strings.TrimSpace(raw) == "---" {
				flushBlock()
				info.closed = true
				break
			}
			if strings.TrimSpace(raw) == "" {
				blockLines = append(blockLines, "")
			} else if leadingIndent(raw) > 0 {
				blockLines = append(blockLines, strings.TrimSpace(raw))
			} else {
				// The block ended at the first non-indented key.
				flushBlock()
			}
		}

		if blockMode == 0 {
			line := strings.TrimSpace(raw)
			if line == "" {
				if readErr != nil {
					break
				}
				continue
			}
			if line == "---" {
				info.closed = true
				break
			}
			if leadingIndent(raw) > 0 {
				// Continuation of a plain scalar description.
				if active == "description" && info.hasDescription {
					info.description = strings.TrimSpace(info.description + " " + line)
				}
				if readErr != nil {
					break
				}
				continue
			}

			key, value, ok := strings.Cut(line, ":")
			if !ok {
				active = ""
				if readErr != nil {
					break
				}
				continue
			}
			key = strings.ToLower(strings.TrimSpace(key))
			value = strings.TrimSpace(value)
			switch key {
			case "name":
				if !info.hasName {
					info.name = parseScalar(value)
					info.hasName = true
					active = "name"
				}
			case "description":
				if !info.hasDescription {
					if mode, ok := blockIndicator(value); ok {
						blockMode = mode
						active = "description"
					} else {
						info.description = parseScalar(value)
						info.hasDescription = true
						active = "description"
					}
				}
			default:
				active = ""
			}
		}

		if readErr != nil {
			break
		}
	}
	if blockMode != 0 {
		flushBlock()
	}

	info.name = strings.TrimSpace(info.name)
	info.description = strings.TrimSpace(info.description)
	if strict && !info.closed {
		return info, false
	}
	return info, true
}

func leadingIndent(line string) int {
	n := 0
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

// blockIndicator recognizes ">", "|", ">-", "|+", and optional indentation
// indicators. A plain scalar that merely starts with those characters (for
// example "> important") is not treated as a block.
func blockIndicator(value string) (byte, bool) {
	value = strings.TrimSpace(stripYAMLComment(value))
	if value == "" || (value[0] != '>' && value[0] != '|') {
		return 0, false
	}
	for _, r := range value[1:] {
		if r == '-' || r == '+' || (r >= '1' && r <= '9') {
			continue
		}
		return 0, false
	}
	return value[0], true
}

func renderBlockScalar(lines []string, mode byte) string {
	if mode == '|' {
		return strings.Join(lines, "\n")
	}

	// Folded: non-empty lines become spaces, blank lines become paragraph
	// breaks. Chomping only affects trailing newlines, which TrimSpace above
	// already removes for the metadata string.
	var b strings.Builder
	pendingBlank := false
	wrote := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if wrote {
				pendingBlank = true
			}
			continue
		}
		if wrote {
			if pendingBlank {
				b.WriteString("\n\n")
			} else {
				b.WriteString(" ")
			}
		}
		b.WriteString(strings.TrimSpace(line))
		wrote = true
		pendingBlank = false
	}
	return b.String()
}

// parseScalar handles the plain and quoted scalar forms used in skill
// frontmatter. Unknown escapes stay literal rather than failing discovery.
func parseScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return strings.TrimSpace(stripYAMLComment(value))
}

// stripYAMLComment removes an unquoted trailing "# comment". YAML only starts
// a comment at a '#' preceded by whitespace, so values such as "C#" survive.
func stripYAMLComment(value string) string {
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			return strings.TrimSpace(value[:i])
		}
	}
	return value
}
