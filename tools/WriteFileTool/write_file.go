//DESC: used to make edits on an alrady existing file
// prefer edit_file tool this is the nuclear option

package writefiletool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	cl "GoWork/Tui/Components/ChangesList"
	"GoWork/tools"
)

type Input struct {
	FilePath string `json:"file_path"`
	Old string `json:"old_string"`
	New string `json:"new_string"`
	ReplaceAll bool `json:"replace_all_mentions"`
}

type Tool struct{}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return "write_file" }

func (t *Tool) Kind() tools.Kind { return tools.KindWrite }

func (t *Tool) Description() string {
	return `Edits an existing file by replacing an exact snippet of text with new text.

Before using this tool:
1. The file must already exist. Use a read tool first to view its current contents.
2. old_string must match the existing file content EXACTLY, including whitespace and indentation.
3. old_string must be unique in the file unless replace_all_mentions is true. If it appears more than once, include more surrounding context (e.g. a preceding line) to make it unique, or set replace_all_mentions to true to replace every occurrence.
This tool performs a plain text substitution, not a diff/patch. It does not create new files (old_string must already be present in the file) and does not check whether the file was modified since it was last read.`
}

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the file to edit, relative to the project root.",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace. Must match the file content exactly and must be unique unless replace_all_mentions is true.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The text to replace old_string with.",
			},
			"replace_all_mentions": map[string]any{
				"type":        "boolean",
				"description": "If true, replaces every occurrence of old_string. If false (default), old_string must appear exactly once in the file.",
			},
		},
		"required":               []string{"file_path", "old_string", "new_string"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func count_appearance(file_contents []byte, search string) (int, error) {
	if search == "" {
		return 0, fmt.Errorf("search string cant be empty")
	}
	return bytes.Count(file_contents, []byte(search)), nil
}

//NOTE: this is a simple implementation we dont make any validation checks (for example if the llm actually read the file recently etch)
//just replace older text with the new one

//TODO: find a way to have it list changes plus update cache handling with context handling and add that to the test cases

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("write_file: invalid input: %w", err)
	}
	if input.FilePath == "" {
		return tools.Errf("file_path is required"), nil
	}
	if input.FilePath == "." {
		return tools.Errf("file_path must point to a file, not the project root"), nil
	}
	if input.Old == "" {
		return tools.Errf("old_string is required"), nil
	}
	if input.Old == input.New {
		return tools.Errf("old_string and new_string are identical, nothing to change"), nil
	}

	// Open and read the existing file through the sandboxed root.
	f, err := args.Root.Open(input.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.Errf("file %s does not exist - the path is wrong or the file was never created. Do NOT create it unless the task explicitly asks for a new file: locate the real path with list_directory or grep_file (in_filenames=true).", input.FilePath), nil
		}
		return tools.ToolResult{}, fmt.Errorf("write_file: opening %s: %w", input.FilePath, err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return tools.ToolResult{}, fmt.Errorf("write_file: stat %s: %w", input.FilePath, err)
	}
	if info.IsDir() {
		f.Close()
		return tools.Errf("%s is a directory, not a file", input.FilePath), nil
	}

	src, err := io.ReadAll(f)
	f.Close() // close the read handle before reopening for write
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("write_file: reading %s: %w", input.FilePath, err)
	}

	// Refuse to edit a file that changed on disk since the agent last saw
	// it (read or written by the model, or touched externally). Otherwise a
	// stale write silently clobbers newer edits.
	if args.ReadState != nil {
		key := args.PathKey(input.FilePath)
		if seen, ok := args.ReadState.SeenAt(key); ok && info.ModTime().After(seen) {
			return tools.Errf("file %s was modified after it was last read - re-read it with read_file to get the current content before editing", input.FilePath), nil
		}
	}

	var accepted, rejected []byte
	if args.WatchList != nil {
		changes := args.WatchList.GetChanges(input.FilePath)
		accepted = cl.AcceptAllChangesBytes(src, changes)
		rejected = cl.RejectAllChangesBytes(src, changes)
	} else {
		accepted = src
		rejected = src
	}

	count, err := count_appearance(accepted, input.Old)
	if err != nil {
		return tools.Errf("%s", err.Error()), nil
	}
	if count == 0 {
		return tools.Errf("old_string not found in %s", input.FilePath), nil
	}
	if count > 1 && !input.ReplaceAll {
		return tools.Errf("found %d occurrences of old_string; set replace_all_mentions to true to replace all of them, or add more surrounding context to make it unique", count), nil
	}

	var updated []byte
	if input.ReplaceAll {
		updated = bytes.ReplaceAll(accepted, []byte(input.Old), []byte(input.New))
	} else {
		updated = bytes.Replace(accepted, []byte(input.Old), []byte(input.New), 1)
	}

	// Merge the edit against the rejected (pre-existing-pending-changes)
	// baseline, producing fresh <<<<<<< old / >>>>>>> Ai change markers
	// around this new edit (and preserving any changes that were still
	// pending before this write ran).
	merged := cl.MergeMarkerBytes(rejected, updated)

	// Reopen (truncating) through the sandboxed root and write the new contents.
	wf, err := args.Root.OpenFile(input.FilePath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("write_file: opening %s for write: %w", input.FilePath, err)
	}

	if args.WatchList != nil {
		args.WatchList.Add(input.FilePath)
	}

	if _, err := wf.Write(merged); err != nil {
		wf.Close()
		return tools.ToolResult{}, fmt.Errorf("write_file: writing %s: %w", input.FilePath, err)
	}
	wf.Close()

	// Update the read-state so the model's own write isn't mistaken for an
	// external change on the next edit in the same turn.
	if args.ReadState != nil {
		if fresh, err := args.Root.Stat(input.FilePath); err == nil {
			args.ReadState.Record(args.PathKey(input.FilePath), fresh.ModTime())
		}
	}

	if count > 1 {
		return tools.Ok(fmt.Sprintf("successfully replaced %d occurrences in %s", count, input.FilePath)), nil
	}
	return tools.Ok(fmt.Sprintf("successfully edited file %s", input.FilePath)), nil
}
