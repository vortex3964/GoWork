package gowork

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	permisionbox "GoWork/Tui/Components/PermisionBox"
	"GoWork/internal/mcp"
	"GoWork/internal/netfetch"
)

const (
	// impactReadLimit bounds how much of a target file the permission
	// explainer reads. Counting occurrences or lines past this point is
	// explicitly described as partial in the summary.
	impactReadLimit = 2 << 20 // 2 MiB
	// impactPreviewLineWidth bounds each displayed diff line so a single
	// minified line cannot balloon the permission box.
	impactPreviewLineWidth = 120
)

const impactReadLimitLabel = "2 MiB"

// explainToolImpact builds the read-only explanation shown in the permission
// box. It only inspects input JSON and reads target files for bounded previews
// - it never writes, moves, deletes, or executes anything.
func explainToolImpact(toolName string, rawInput json.RawMessage, root string) permisionbox.Impact {
	switch toolName {
	case "write_file":
		return explainWriteFileImpact(rawInput, root)
	case "edit_file":
		return explainEditFileImpact(rawInput, root)
	case "create_file_tool", "create_file":
		return explainCreateFileImpact(rawInput, root)
	case "move_file":
		return explainMoveFileImpact(rawInput, root)
	case "delete_file":
		return explainDeleteFileImpact(rawInput, root)
	case "bash":
		return explainBashImpact(rawInput)
	case "web_fetch":
		return explainWebFetchImpact(rawInput)
	case "web_search":
		return explainWebSearchImpact(rawInput)
	case "grep_search":
		return explainGrepImpact(rawInput)
	case "file_search":
		return explainFileSearchImpact(rawInput)
	case "read_file", "list_directory", "skill", "todo_list", "questions_tool":
		return explainReadOnlyImpact(toolName, rawInput)
	case "skill_install":
		return explainSkillInstallImpact(rawInput)
	case "skill_load":
		return explainSkillLoadImpact(rawInput)
	case "mcp_install":
		return explainMCPInstallImpact(rawInput)
	case "mcp_list":
		return permisionbox.Impact{Summary: "Lists configured MCP servers with transport, enabled/connected state, scope, trust, and tool counts. Read-only; environment and header values are never shown."}
	case "mcp_remove":
		return explainMCPRemoveImpact(rawInput)
	default:
		return permisionbox.Impact{
			Summary: fmt.Sprintf("%s will run with the arguments shown below.", toolName),
		}
	}
}

// --- file mutation tools -------------------------------------------------

func explainWriteFileImpact(raw json.RawMessage, root string) permisionbox.Impact {
	var in struct {
		FilePath   string `json:"file_path"`
		Old        string `json:"old_string"`
		New        string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all_mentions"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("write_file")
	}
	path := strings.TrimSpace(in.FilePath)
	if path == "" {
		return permisionbox.Impact{Summary: "write_file has no file_path, so it will fail without changing anything."}
	}

	oldLines := impactSnippetLines(in.Old)
	newLines := impactSnippetLines(in.New)
	data, info, truncated, err := impactReadFile(root, path)
	if err != nil {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Edits %s, but %s; write_file will fail without changing anything.", path, impactReadReason(err, info)),
		}
	}

	preview := previewImpactDiff(oldLines, newLines)
	var change string
	switch {
	case in.Old == "":
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Edits %s, but old_string is empty; write_file will fail.", path),
			Preview: preview,
		}
	case in.Old == in.New:
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Edits %s, but old_string and new_string are identical; write_file will refuse this no-op.", path),
			Preview: preview,
		}
	default:
		count := bytes.Count(data, []byte(in.Old))
		switch {
		case truncated:
			if in.ReplaceAll {
				change = fmt.Sprintf("replaces occurrences throughout the file (at least %d in the first %s; the count is partial)", count, impactReadLimitLabel)
			} else {
				change = fmt.Sprintf("found %d occurrence(s) in the first %s (the file is larger, so the count is partial)", count, impactReadLimitLabel)
			}
		case count == 0:
			change = "the snippet does not appear in the file, so the tool will fail"
		case in.ReplaceAll:
			change = fmt.Sprintf("replaces all %d occurrences", count)
		case count == 1:
			change = "replaces 1 occurrence"
		default:
			change = fmt.Sprintf("the snippet appears %d times, so the tool will fail unless replace_all_mentions is true", count)
		}
	}

	if truncated {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Edits %s: %s (%d line(s) -> %d line(s)); the first %s has %d line(s).%s",
				path, change, len(oldLines), len(newLines), impactReadLimitLabel, len(impactFileLines(data)), impactPartialNote(true)),
			Preview: preview,
		}
	}
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Edits %s: %s (%d line(s) -> %d line(s)); the file currently has %d line(s).",
			path, change, len(oldLines), len(newLines), len(impactFileLines(data))),
		Preview: preview,
	}
}

func explainEditFileImpact(raw json.RawMessage, root string) permisionbox.Impact {
	var in struct {
		FilePath   string `json:"file_path"`
		StartLine  int    `json:"start_line"`
		EndLine    int    `json:"end_line"`
		NewContent string `json:"new_content"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("edit_file")
	}
	path := strings.TrimSpace(in.FilePath)
	if path == "" {
		return permisionbox.Impact{Summary: "edit_file has no file_path, so it will fail without changing anything."}
	}

	data, info, truncated, err := impactReadFile(root, path)
	if err != nil {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Edits %s, but %s; edit_file will fail without changing anything.", path, impactReadReason(err, info)),
		}
	}

	lines := impactFileLines(data)
	replacement := impactReplacementLines(in.NewContent)
	if reason := impactRangeError(in.StartLine, in.EndLine, len(lines)); reason != "" {
		// Only past-the-end failures can be an artifact of the read cap.
		// start < 1 or end < start-1 is invalid regardless of file size.
		if truncated && in.StartLine >= 1 && in.EndLine >= in.StartLine-1 {
			return permisionbox.Impact{
				Summary: fmt.Sprintf("Edits %s at lines %d–%d, but the target is larger than %s and that range is past the inspected prefix; the edit may still be valid.%s",
					path, in.StartLine, in.EndLine, impactReadLimitLabel, impactPartialNote(true)),
			}
		}
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Edits %s, but %s; edit_file will fail without changing anything.", path, reason),
		}
	}

	if in.EndLine < in.StartLine {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Inserts %d line(s) at line %d in %s (%s has %d line(s)).%s",
				len(replacement), in.StartLine, path, impactLineScope(truncated), len(lines), impactPartialNote(truncated)),
			Preview: previewImpactDiff(nil, replacement),
		}
	}

	oldLines := lines[in.StartLine-1 : in.EndLine]
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Replaces lines %d–%d (%d line(s)) with %d line(s) in %s (%s has %d line(s)).%s",
			in.StartLine, in.EndLine, len(oldLines), len(replacement), path, impactLineScope(truncated), len(lines), impactPartialNote(truncated)),
		Preview: previewImpactDiff(oldLines, replacement),
	}
}

func explainCreateFileImpact(raw json.RawMessage, root string) permisionbox.Impact {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("create_file")
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return permisionbox.Impact{Summary: "create_file has no path, so it will fail without changing anything."}
	}
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Creates directory %s and any missing parent directories. No file content is written.", path),
		}
	}

	newLines := impactSnippetLines(in.Content)
	data, info, truncated, err := impactReadFile(root, path)
	switch {
	case err == nil:
		kind := "a file already exists"
		if info != nil && info.IsDir() {
			kind = "a directory already exists"
		}
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Creates %s, but %s there; create_file refuses to overwrite and will fail.%s", path, kind, impactPartialNote(truncated)),
			Preview: previewImpactDiff(impactFileLines(data), newLines),
		}
	case !os.IsNotExist(err):
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Creates %s, but the path cannot be used (%s); create_file will fail.", path, impactReadReason(err, info)),
		}
	}

	if strings.TrimSpace(in.Content) == "" {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Creates %s, but content is empty; create_file will refuse to create a placeholder file.", path),
		}
	}
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Creates new file %s with %d line(s) (%d bytes); missing parent directories are created.",
			path, len(newLines), len(in.Content)),
		Preview: previewImpactDiff(nil, newLines),
	}
}

func explainMoveFileImpact(raw json.RawMessage, root string) permisionbox.Impact {
	var in struct {
		SourcePath      string `json:"source_path"`
		DestinationPath string `json:"destination_path"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("move_file")
	}
	src := strings.TrimSpace(in.SourcePath)
	dst := strings.TrimSpace(in.DestinationPath)
	if src == "" || dst == "" {
		return permisionbox.Impact{Summary: "move_file needs both source_path and destination_path, so it will fail."}
	}

	srcData, srcInfo, srcTruncated, srcErr := impactReadFile(root, src)
	if srcErr != nil {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Moves %s to %s, but %s; move_file will fail without changing anything.",
				src, dst, impactSourceReason(srcErr, srcInfo)),
		}
	}
	srcLines := impactFileLines(srcData)

	dstData, dstInfo, dstTruncated, dstErr := impactReadFile(root, dst)
	switch {
	case dstErr == nil:
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Moves %s to %s. The destination already exists and will be overwritten; a now-empty source directory is removed.%s",
				src, dst, impactPartialNote(srcTruncated || dstTruncated)),
			Preview: previewImpactDiff(impactFileLines(dstData), srcLines),
		}
	case os.IsNotExist(dstErr):
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Moves %s (%s has %d line(s)) to %s. Missing destination directories are created; a now-empty source directory is removed.%s",
				src, impactLineScope(srcTruncated), len(srcLines), dst, impactPartialNote(srcTruncated)),
			Preview: previewImpactDiff(nil, srcLines),
		}
	default:
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Moves %s to %s, but the destination cannot be used (%s); move_file may fail.",
				src, dst, impactReadReason(dstErr, dstInfo)),
		}
	}
}

func explainDeleteFileImpact(raw json.RawMessage, root string) permisionbox.Impact {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("delete_file")
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return permisionbox.Impact{Summary: "delete_file has no path, so it will fail without deleting anything."}
	}

	data, info, truncated, err := impactReadFile(root, path)
	if err != nil {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Deletes %s, but %s; delete_file will fail.", path, impactReadReason(err, info)),
		}
	}
	if truncated {
		return permisionbox.Impact{
			Summary: fmt.Sprintf("Deletes %s. The file is larger than %s, so only the first %s is previewed and its line/byte counts are partial. If its parent directory becomes empty, that directory is removed too.",
				path, impactReadLimitLabel, impactReadLimitLabel),
			Preview: previewImpactDiff(impactFileLines(data), nil),
		}
	}
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Deletes %s (%d line(s), %d bytes). If its parent directory becomes empty, that directory is removed too.",
			path, len(impactFileLines(data)), len(data)),
		Preview: previewImpactDiff(impactFileLines(data), nil),
	}
}

// --- command / network / search tools ------------------------------------

func explainBashImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Command             string `json:"command"`
		WorkingDir          string `json:"working_dir"`
		RunInBackground     bool   `json:"run_in_background"`
		AutoBackgroundAfter int    `json:"auto_background_after"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("bash")
	}
	command := strings.TrimSpace(in.Command)
	if command == "" {
		return permisionbox.Impact{Summary: "bash has no command, so it will fail without running anything."}
	}

	where := "the project root"
	if wd := strings.TrimSpace(in.WorkingDir); wd != "" {
		where = fmt.Sprintf("%q under the project root", wd)
	}
	summary := fmt.Sprintf("Runs a bash command in %s. Combined stdout/stderr is captured, truncated, and the exit code is reported.", where)
	if in.RunInBackground {
		summary += " It starts immediately as a background job and returns a job ID."
	} else if in.AutoBackgroundAfter > 0 {
		summary += fmt.Sprintf(" If still running after %d seconds it becomes a background job.", in.AutoBackgroundAfter)
	}

	return permisionbox.Impact{
		Summary: summary,
		Preview: "$ " + command,
	}
}

func explainWebFetchImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		URL    string `json:"url"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("web_fetch")
	}
	url := strings.TrimSpace(in.URL)
	if url == "" {
		return permisionbox.Impact{Summary: "web_fetch has no URL, so it will fail without fetching anything."}
	}

	summary := fmt.Sprintf("Fetches %s and converts the page to plain text.", url)
	if prompt := strings.TrimSpace(in.Prompt); prompt != "" {
		summary += fmt.Sprintf(" Long pages are summarized against %q.", prompt)
	} else {
		summary += " Long pages are summarized when possible."
	}
	summary += " Long output may be cached under .agent-cache/fetch/ and returned as a short preview."
	return permisionbox.Impact{Summary: summary, Preview: "GET " + url}
}

func explainWebSearchImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("web_search")
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return permisionbox.Impact{Summary: "web_search has no query, so it will fail without searching."}
	}
	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Searches the web for %q (up to %d results). Results are returned as a compact summary of titles, links, and snippets; page contents are not downloaded.",
			query, maxResults),
		Preview: "GET duckduckgo.com/?q=" + query,
	}
}

func explainGrepImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Pattern      string `json:"pattern"`
		Path         string `json:"path"`
		Include      string `json:"include"`
		IgnoreCase   bool   `json:"ignore_case"`
		Literal      bool   `json:"literal"`
		ContextLines int    `json:"context_lines"`
		Limit        int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("grep_search")
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return permisionbox.Impact{Summary: "grep_search has no pattern, so it will fail."}
	}
	scope := strings.TrimSpace(in.Path)
	if scope == "" {
		scope = "the project"
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	var extras []string
	if in.Literal {
		extras = append(extras, "literal")
	}
	if in.IgnoreCase {
		extras = append(extras, "case-insensitive")
	}
	if in.Include != "" {
		extras = append(extras, "include "+in.Include)
	}
	if in.ContextLines > 0 {
		extras = append(extras, fmt.Sprintf("%d context line(s)", in.ContextLines))
	}
	suffix := ""
	if len(extras) > 0 {
		suffix = " (" + strings.Join(extras, ", ") + ")"
	}
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Searches file contents in %s for %q%s and returns up to %d matching line(s). Read-only.",
			scope, in.Pattern, suffix, limit),
	}
}

func explainFileSearchImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Include    string `json:"include"`
		IgnoreCase bool   `json:"ignore_case"`
		Literal    bool   `json:"literal"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("file_search")
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return permisionbox.Impact{Summary: "file_search has no pattern, so it will fail."}
	}
	scope := strings.TrimSpace(in.Path)
	if scope == "" {
		scope = "the project"
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Lists up to %d file path(s) under %s whose names match %q. Read-only.", limit, scope, in.Pattern),
	}
}

func explainReadOnlyImpact(toolName string, raw json.RawMessage) permisionbox.Impact {
	switch toolName {
	case "read_file":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(raw, &in); err == nil && strings.TrimSpace(in.Path) != "" {
			return permisionbox.Impact{Summary: fmt.Sprintf("Reads lines from %s. Read-only; no files are changed.", in.Path)}
		}
	case "list_directory":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(raw, &in); err == nil && strings.TrimSpace(in.Path) != "" {
			return permisionbox.Impact{Summary: fmt.Sprintf("Lists the immediate contents of %s. Read-only.", in.Path)}
		}
	case "skill":
		var in struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &in); err == nil && strings.TrimSpace(in.Name) != "" {
			return permisionbox.Impact{Summary: fmt.Sprintf("Loads the full instructions of skill %q. Read-only.", in.Name)}
		}
	}
	return permisionbox.Impact{Summary: fmt.Sprintf("%s does not modify files. Read-only.", toolName)}
}

// --- install / MCP tools -------------------------------------------------

// explainSkillInstallImpact previews a skill install without downloading or
// writing anything. Source URLs are stripped of credentials and query values;
// the skill body never appears in the permission box.
func explainSkillInstallImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Source    string `json:"source"`
		Path      string `json:"path"`
		Ref       string `json:"ref"`
		Name      string `json:"name"`
		Overwrite bool   `json:"overwrite"`
		Load      bool   `json:"load"`
		DryRun    bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("skill_install")
	}
	source := redactInstallText(in.Source)
	summary := "Installs a skill into the durable user skills directory."
	if source != "" {
		summary = fmt.Sprintf("Installs a skill from %s into the durable user skills directory.", source)
	}
	switch {
	case in.DryRun:
		summary += " Dry run: validates and fetches the source, then reports what would be installed; nothing is written."
	case in.Overwrite:
		summary += " An existing skill with the same name may be replaced."
	default:
		summary += " An existing skill with the same name is kept."
	}
	if in.Load {
		summary += " The skill is loaded into this session immediately."
	}

	var lines []string
	if source != "" {
		lines = append(lines, impactPreviewLine("source: "+source))
	}
	if path := strings.TrimSpace(in.Path); path != "" {
		lines = append(lines, impactPreviewLine("path: "+path))
	}
	if ref := strings.TrimSpace(in.Ref); ref != "" {
		lines = append(lines, impactPreviewLine("ref: "+ref))
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		lines = append(lines, impactPreviewLine("name: "+name))
	}
	return permisionbox.Impact{Summary: summary, Preview: strings.Join(lines, "\n")}
}

// explainSkillLoadImpact describes the session-only skill set change.
func explainSkillLoadImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("skill_load")
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	name := strings.TrimSpace(in.Name)
	switch action {
	case "load":
		if name != "" {
			return permisionbox.Impact{Summary: fmt.Sprintf("Loads skill %q into this session. Read-only; no files or configuration change.", name)}
		}
	case "unload":
		if name != "" {
			return permisionbox.Impact{Summary: fmt.Sprintf("Unloads skill %q from this session. Read-only; the installed file stays on disk.", name)}
		}
	case "list", "":
		return permisionbox.Impact{Summary: "Lists installed and loaded skills. Read-only."}
	}
	return permisionbox.Impact{Summary: "Lists, loads, or unloads skills for this session only. Read-only; nothing is downloaded or written."}
}

// explainMCPInstallImpact previews an MCP install. Inline configs may carry
// credentials in env/header values, so only server names and transports are
// shown; values are never read back into the summary.
func explainMCPInstallImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Source    string          `json:"source"`
		Config    json.RawMessage `json:"config"`
		Path      string          `json:"path"`
		Ref       string          `json:"ref"`
		Scope     string          `json:"scope"`
		Servers   []string        `json:"servers"`
		Overwrite bool            `json:"overwrite"`
		Start     bool            `json:"start"`
		DryRun    bool            `json:"dry_run"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("mcp_install")
	}

	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	if scope == "" {
		scope = "user"
	}
	source := redactInstallText(in.Source)
	inline := len(bytes.TrimSpace(in.Config)) > 0

	// Describe inline servers without exposing env/header values.
	var servers []string
	if inline {
		if data, err := unwrapInlineMCPConfig(in.Config); err == nil {
			if cfg, err := mcp.ParseConfig(data); err == nil && cfg != nil {
				for _, server := range cfg.ListServers() {
					servers = append(servers, fmt.Sprintf("%s (%s)", server.Name, mcpTransportLabel(server)))
				}
			}
		}
	}

	summary := "Installs MCP server configuration into the " + scope + " scope."
	if source != "" {
		summary = fmt.Sprintf("Installs MCP server configuration from %s into the %s scope.", source, scope)
	} else if inline {
		summary = fmt.Sprintf("Installs %d inline MCP server(s) into the %s scope.", len(servers), scope)
	}
	switch {
	case in.DryRun:
		summary += " Dry run: validates and reports only; nothing is written and no server is started."
	case in.Start:
		summary += " New or updated servers are written to the config and started immediately."
	default:
		summary += " The config is written atomically; servers start on the next launch unless start is requested."
	}
	if in.Overwrite {
		summary += " Existing servers with the same names may be replaced."
	}
	if scope == "project" {
		summary += " Project-scope servers are untrusted and never receive automatic tool approval."
	}

	var lines []string
	if source != "" {
		lines = append(lines, impactPreviewLine("source: "+source))
	}
	if path := strings.TrimSpace(in.Path); path != "" {
		lines = append(lines, impactPreviewLine("path: "+path))
	}
	if ref := strings.TrimSpace(in.Ref); ref != "" {
		lines = append(lines, impactPreviewLine("ref: "+ref))
	}
	if len(in.Servers) > 0 {
		lines = append(lines, impactPreviewLine("selected: "+strings.Join(in.Servers, ", ")))
	}
	if len(servers) > 0 {
		lines = append(lines, impactPreviewLine("servers: "+strings.Join(servers, ", ")))
	}
	if inline && len(servers) == 0 {
		lines = append(lines, "config: inline MCP JSON (values hidden)")
	}
	return permisionbox.Impact{Summary: summary, Preview: strings.Join(lines, "\n")}
}

func explainMCPRemoveImpact(raw json.RawMessage) permisionbox.Impact {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return impactInputError("mcp_remove")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return permisionbox.Impact{Summary: "mcp_remove has no name, so it will fail without removing anything."}
	}
	return permisionbox.Impact{
		Summary: fmt.Sprintf("Removes MCP server %q from its configuration scope and stops its tools.", name),
	}
}

// redactInstallText keeps install previews from echoing credentials embedded
// in a source URL. It never needs the parsed config, so a malformed input is
// still safe to display.
func redactInstallText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if token := strings.TrimSpace(netfetch.GitHubTokenFromEnv()); token != "" {
		value = strings.ReplaceAll(value, token, "[redacted]")
	}
	value = mcpUserInfoInErrorRE.ReplaceAllString(value, "://[redacted]@")
	value = mcpQueryValueInErrorRE.ReplaceAllString(value, "${1}[redacted]")
	return value
}

// --- helpers -------------------------------------------------------------

func impactInputError(toolName string) permisionbox.Impact {
	return permisionbox.Impact{
		Summary: fmt.Sprintf("%s: the tool input could not be read, so its effect is unclear.", toolName),
	}
}

// impactReadFile reads at most impactReadLimit bytes from a project file. The
// returned flag reports that the file was larger than the cap, so callers can
// describe the preview/counts as partial instead of implying they saw it all.
func impactReadFile(root, rel string) ([]byte, os.FileInfo, bool, error) {
	if root == "" {
		return nil, nil, false, fmt.Errorf("the project root is unavailable")
	}
	clean := filepath.Clean(strings.TrimSpace(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, nil, false, fmt.Errorf("the path is outside the project root")
	}

	// Use os.Root so an in-project symlink cannot make this read-only preview
	// leak a file outside the project.
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, false, err
	}
	defer rootFS.Close()

	f, err := rootFS.Open(clean)
	if err != nil {
		return nil, nil, false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, nil, false, err
	}
	if info.IsDir() {
		return nil, info, false, fmt.Errorf("the target is a directory")
	}

	// Read at most the cap into an exactly-sized buffer, so a huge target
	// cannot make the permission box allocate proportionally to the file.
	readSize := info.Size()
	if readSize > impactReadLimit {
		readSize = impactReadLimit
	}
	if readSize < 0 {
		readSize = 0
	}
	buf := bytes.NewBuffer(make([]byte, 0, int(readSize)))
	n, err := io.Copy(buf, io.LimitReader(f, readSize))
	if err != nil {
		return nil, info, false, err
	}
	data := buf.Bytes()
	return data, info, info.Size() > n, nil
}

func impactPartialNote(truncated bool) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf(" The target is larger than %s, so this preview and its counts are partial.", impactReadLimitLabel)
}

func impactLineScope(truncated bool) string {
	if truncated {
		return "the first " + impactReadLimitLabel
	}
	return "the file"
}

func impactReadReason(err error, info os.FileInfo) string {
	if info != nil && info.IsDir() {
		return "the target is a directory"
	}
	if os.IsNotExist(err) {
		return "the target does not exist"
	}
	return "the target could not be read"
}

func impactSourceReason(err error, info os.FileInfo) string {
	if info != nil && info.IsDir() {
		return "the source is a directory"
	}
	if os.IsNotExist(err) {
		return "the source does not exist"
	}
	return "the source could not be read"
}

func impactRangeError(start, end, lineCount int) string {
	switch {
	case start < 1:
		return fmt.Sprintf("start_line %d is below 1", start)
	case end < start-1:
		return fmt.Sprintf("end_line %d is before start_line %d", end, start)
	case start > lineCount+1:
		return fmt.Sprintf("start_line %d is past the end of the file (%d lines)", start, lineCount)
	case end > lineCount:
		return fmt.Sprintf("end_line %d is past the end of the file (%d lines)", end, lineCount)
	}
	return ""
}

// impactFileLines splits file bytes for previews, keeping a single trailing
// newline from creating a phantom extra line.
func impactFileLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

func impactSnippetLines(s string) []string {
	if s == "" {
		return nil
	}
	return impactFileLines([]byte(s))
}

// impactReplacementLines mirrors edit_file: a single trailing newline is the
// normal line terminator, while empty content means "no replacement lines".
func impactReplacementLines(s string) []string {
	if s == "" {
		return nil
	}
	text := strings.ReplaceAll(s, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

// previewImpactDiff renders a small, bounded before/after preview. Each side
// shows at most three lines, each line is capped to impactPreviewLineWidth
// columns with an explicit ellipsis, and the whole preview stays within eight
// lines including any truncation markers.
func previewImpactDiff(oldLines, newLines []string) string {
	const (
		sideLines    = 3
		maxDiffLines = 8
	)
	var out []string
	addSide := func(prefix string, lines []string) {
		shown := lines
		if len(shown) > sideLines {
			shown = shown[:sideLines]
		}
		for _, line := range shown {
			out = append(out, prefix+impactPreviewLine(line))
		}
		if len(lines) > sideLines {
			out = append(out, fmt.Sprintf("… %d more line(s)", len(lines)-sideLines))
		}
	}
	addSide("- ", oldLines)
	addSide("+ ", newLines)
	if len(out) > maxDiffLines {
		out = out[:maxDiffLines-1]
		out = append(out, "…")
	}
	return strings.Join(out, "\n")
}

// impactPreviewLine normalizes one preview line and caps it to the fixed
// column budget, appending "…" when content was removed.
func impactPreviewLine(line string) string {
	line = strings.ReplaceAll(line, "\t", "    ")
	line = strings.TrimRight(line, " \r")
	if lipgloss.Width(line) <= impactPreviewLineWidth {
		return line
	}

	var b strings.Builder
	width := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if width+rw > impactPreviewLineWidth {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + "…"
}
