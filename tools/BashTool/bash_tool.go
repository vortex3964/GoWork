// DESC: bash lets the model run shell commands inside the project root.
// DESC: it's explicitly allowed (KindAllowed) and carries its own guardrails:
// banned commands and project-root confinement.
package bashtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"GoWork/tools"
)

const (
	ToolName = "bash"

	// DefaultAutoBackgroundAfter is how long a command may run before the
	// tool stops waiting and hands it to the background job manager.
	DefaultAutoBackgroundAfter = 60 // seconds

	// MaxOutputLength truncates command output so one huge result can't
	// flood the conversation context.
	MaxOutputLength = 30000

	// BashNoOutput is what gets returned when a command produces nothing.
	BashNoOutput = "no output"

	// fastFailWait is how long a run_in_background command is watched for
	// immediate failures (bad syntax, missing binary, etc.) before the
	// tool returns the background job ID.
	fastFailWait = 500 * time.Millisecond

	shell = "bash"
)

type Input struct {
	Description         string `json:"description"`
	Command             string `json:"command"`
	WorkingDir          string `json:"working_dir,omitempty"`
	RunInBackground     bool   `json:"run_in_background,omitempty"`
	AutoBackgroundAfter int    `json:"auto_background_after,omitempty"`
}

type Tool struct{}

func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return ToolName }

func (t *Tool) Kind() tools.Kind { return tools.KindAllowed }

func (t *Tool) Description() string {
	return `Run a shell command inside the project.

The command runs with bash in the project root unless working_dir is given (relative to the root). It can never leave the project: absolute paths, ".." escapes, and symlinks that resolve outside the root are rejected. Output (stdout + stderr) is captured, truncated to keep the context small, and non-zero exit codes are reported.

Safety restrictions:
- Network/download/sysadmin commands (curl, wget, ssh, scp, sudo, su, systemctl, mount, mkfs, ...) and package installs (apt install, npm install -g, etc.) are BLOCKED. Use the file tools for files and ask the user to install things system-wide.
- working_dir must stay inside the project root.

Long-running commands: after auto_background_after seconds (default 60) or immediately if run_in_background is true, the command is moved to a background job and the job ID is returned so the caller knows it is still running.`
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"description": map[string]any{
				"type":        "string",
				"description": "A brief description of what the command does, under 30 characters or so.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute.",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": `Directory to run in, relative to the project root. Defaults to the project root. Cannot escape the project.`,
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Start the command directly as a background job and return its job ID immediately (default false).",
			},
			"auto_background_after": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Seconds to wait before moving an undecided command to a background job (default %d).", DefaultAutoBackgroundAfter),
			},
		},
		"required":             []string{"command"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{}, err
	}

	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("%s: invalid input: %w", ToolName, err)
	}
	if strings.TrimSpace(input.Command) == "" {
		return tools.Errf("missing command"), nil
	}
	if blocked := blockedIn(input.Command); blocked != "" {
		return tools.Errf("command %q is not allowed: %q is banned for safety", input.Command, blocked), nil
	}

	wd, err := resolveWorkingDir(*args.RootPath, input.WorkingDir)
	if err != nil {
		return tools.Errf("%s", err.Error()), nil
	}

	bg.Cleanup()

	if input.RunInBackground {
		return runInBackground(input, wd)
	}
	return runForeground(ctx, input, wd)
}

// runForeground runs the command synchronously. If it finishes within
// AutoBackgroundAfter (default DefaultAutoBackgroundAfter) the output is
// returned directly; past that it's moved to the background job manager and
// the job ID is returned instead. An incoming ctx.Done() kills the child.
func runForeground(ctx context.Context, input Input, wd string) (tools.ToolResult, error) {
	// context.Background: if the command crosses the auto-background
	// threshold it must survive the caller cancelling the turn.
	job, err := bg.start(context.Background(), wd, input.Command)
	if err != nil {
		return tools.Errf("starting command: %v", err), nil
	}

	threshold := input.AutoBackgroundAfter
	if threshold <= 0 {
		threshold = DefaultAutoBackgroundAfter
	}
	timeout := time.After(time.Duration(threshold) * time.Second)

	select {
	case <-job.Done():
		return syncResult(job), nil
	case <-timeout:
		// Just finished as the timeout fired: return the real output.
		select {
		case <-job.Done():
			return syncResult(job), nil
		default:
		}
		return tools.Ok(bgMessage(job)), nil
	case <-ctx.Done():
		job.Kill()
		<-job.Done()
		bg.remove(job.ID)
		return tools.ToolResult{}, ctx.Err()
	}
}

// runInBackground starts the command immediately as a background job, waits a
// moment to catch fast failures (bad syntax, missing binary, non-zero exit),
// then returns the job ID. A job that already finished returns its output.
func runInBackground(input Input, wd string) (tools.ToolResult, error) {
	job, err := bg.start(context.Background(), wd, input.Command)
	if err != nil {
		return tools.Errf("starting command: %v", err), nil
	}

	select {
	case <-job.Done():
		return syncResult(job), nil
	case <-time.After(fastFailWait):
	}
	return tools.Ok(bgMessage(job)), nil
}

// syncResult formats a finished job's output the way the rest of the codebase
// does: truncate, attach stderr/exit-code notes, and stamp the working dir.
func syncResult(job *job) tools.ToolResult {
	stdout, stderr, execErr := job.Result()
	bg.remove(job.ID)

	// A real error with no exit code (nothing that exited with a status
	// other than the run) means the command never completed normally, and
	// it wasn't an interrupt either - surface it as a hard failure.
	code, interrupted := exitInfo(execErr)
	if execErr != nil && !interrupted && code == 0 {
		return tools.Errf("error executing command: %v", execErr)
	}

	content := formatOutput(stdout, stderr, execErr)
	if content == "" {
		return tools.Ok(BashNoOutput)
	}
	content += fmt.Sprintf("\n\n<cwd>%s</cwd>", job.WorkingDir)
	return tools.Ok(content)
}

// bgMessage is the response for a job that is still running in the background.
func bgMessage(job *job) string {
	stdout, _, running := job.Snapshot()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Background job started with ID: %s\n\n", job.ID))
	b.WriteString(fmt.Sprintf("command: %s\ncwd: %s\n\n", job.Command, job.WorkingDir))
	if !running {
		b.WriteString("job completed while the response was being built\n")
	}
	if stdout != "" {
		b.WriteString(fmt.Sprintf("partial output so far:\n%s", truncateOutput(stdout, MaxOutputLength)))
	}
	b.WriteString("\nUse the exported bash job API (bg) to read or kill this job.\n")
	return b.String()
}

// formatOutput truncates both streams and appends stderr/exit-code info.
func formatOutput(stdout, stderr string, execErr error) string {
	stdout = truncateOutput(stdout, MaxOutputLength)
	stderr = truncateOutput(stderr, MaxOutputLength)

	errorMessage := stderr
	code, interrupted := exitInfo(execErr)

	if interrupted {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += "Command was aborted before completion"
	} else if code != 0 {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += fmt.Sprintf("Exit code %d", code)
	}

	if stdout != "" && stderr != "" {
		stdout += "\n"
	}
	if errorMessage != "" {
		stdout += "\n" + errorMessage
	}
	return stdout
}

// exitInfo returns the numeric exit code (0 if the error isn't an ExitError)
// and whether the command was interrupted (killed by signal/cancel rather
// than exiting on its own).
func exitInfo(err error) (code int, interrupted bool) {
	if err == nil {
		return 0, false
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), false
	}
	return 0, true
}

// truncateOutput cuts content down to max characters, keeping the head and
// tail and reporting how much was dropped in the middle.
func truncateOutput(content string, max int) string {
	if utf8.RuneCountInString(content) <= max {
		return content
	}
	runes := []rune(content)
	half := max / 2
	keep := len(runes) - half - half
	var b strings.Builder
	b.WriteString(string(runes[:half]))
	b.WriteString("\n\n... [")
	b.WriteString(strconv.Itoa(keep))
	b.WriteString(" chars truncated] ...\n\n")
	b.WriteString(string(runes[len(runes)-half:]))
	return b.String()
}
