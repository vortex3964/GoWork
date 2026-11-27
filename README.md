# GoWork

![GoWork logo](logo/logo.svg)

GoWork is a terminal AI coding agent for real project work. It runs a
streaming agent loop against cloud or local models, edits files through
guarded tools, and keeps every change reviewable before it lands.

## Features

- **Streaming agent loop.** Replies stream token by token. Independent tool
  calls run in parallel, their results go back to the model, and the loop
  continues across tool batches until the model finishes the task - there is
  no turn cap. Press ctrl+i at any point to interrupt a running turn, and one
  reply can request up to 10 tools.
- **Project-confined tools.** File operations are rooted at the project you
  open. `bash`, `edit_file`, `write_file`, `create_file_tool`, `move_file`,
  `delete_file`, `web_fetch`, `web_search`, and the install/remove tools require
  approval in the permission box, with an optional note for the model. Read-only
  tools run without a prompt; see [Tool approval](#tool-approval).
- **Reviewable changes.** Every edit is tracked as a diff. Accept or reject a
  single change, all changes in a file from its header row, or the whole batch.
- **Skills.** Reusable instruction sets live as `SKILL.md` files in the durable
  per-user config directory. Load the ones a session needs from the Skills tab
  or with `skill_load`; the model receives them through the `skill` tool.
  `skill_install` adds a skill from GitHub, a URL, or a local path.
- **Bootstrap skill.** `skills/gowork-bootstrap/SKILL.md` ships with GoWork and
  is seeded on first run, but not auto-loaded. It teaches the model the install
  workflow, verification steps, and security rules.
- **Tools tab.** Skills, MCP servers, and language servers in one place. See
  what is active, add or remove MCP/LSP servers, and inspect connection,
  tool-count, and error status. MCP tools and LSP diagnostics/symbol tools are
  registered with the model automatically.
- **Stats and session history.** Token usage and session messages are stored
  in SQLite. The Stats tab shows per-model usage, and `-S` restores a previous
  session with its messages and loaded skills.
- **Cloud and local models.** Google, Anthropic, Groq, OpenAI, OpenCode Go,
  Ollama, llama.cpp, and LM Studio are supported out of the box.
- **Terminal-first interface.** Tabs for code, skills, tools, and stats, a todo
  panel, provider picker, permission prompts, and automatic context
  compaction.
- **PDF reading.** `read_file` converts PDFs to Markdown through an optional
  Python environment. PyMuPDF4LLM is the lightweight default; Marker is used
  when it is installed.

## Installation

### Linux and macOS

The installer downloads a release, verifies it against the published
checksums, installs the binary with its assets, and links `gowork` into your
bin directory.

Install with the one-liner:

```sh
curl -fsSL https://raw.githubusercontent.com/vortex3964/GoWork/main/scripts/install.sh | bash
```

Update an existing installation:

```sh
curl -fsSL https://raw.githubusercontent.com/vortex3964/GoWork/main/scripts/install.sh | bash -s -- --update
```

Remove GoWork, its launcher link, and the installer's PATH block:

```sh
curl -fsSL https://raw.githubusercontent.com/vortex3964/GoWork/main/scripts/install.sh | bash -s -- --uninstall
```

From a checkout, use the standalone entry points:

```sh
scripts/install.sh
scripts/update.sh
scripts/uninstall.sh
```

`update.sh` and `uninstall.sh` are thin wrappers around `install.sh --update`
and `install.sh --uninstall`. They forward every other option, so you can pin a
release or choose an install directory:

```sh
scripts/update.sh --version v1.2.3 --dir "$HOME/opt/gowork"
scripts/uninstall.sh --dir "$HOME/opt/gowork"
```

The wrappers contain no install logic of their own: path safety, checksum
verification, launcher handling and user data preservation all stay in
`install.sh`.

Options:

| Flag | Meaning |
| --- | --- |
| `--update` | Replace an existing installation with the latest release or `--version`. |
| `--uninstall` | Remove the install directory, the `gowork` symlink, and the marked PATH block. |
| `--version <tag>` | Install a specific release, for example `--version v1.2.3`. Defaults to `latest`. |
| `--dir <path>` | Install somewhere other than the default directory. |
| `--no-modify-path` | Do not add the launcher directory to your shell PATH. |
| `-h`, `--help` | Show installer help. |

Defaults:

- Install directory: `${XDG_DATA_HOME:-$HOME/.local/share}/gowork`
- Launcher: `${XDG_BIN_HOME:-$HOME/.local/bin}/gowork`
- PATH: the installer adds the launcher directory to `~/.bashrc`,
  `~/.bash_profile`, `~/.profile`, `~/.zshrc`, or the fish config, inside a
  marked block that `--uninstall` removes.

Open a new terminal after installing so the updated PATH is active.

### Windows

Run PowerShell 5 or newer. Download and execute the installer, or run the
copy in a checkout:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/vortex3964/GoWork/main/scripts/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

Update or uninstall with the installer mode:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Update
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Uninstall
```

From a checkout, use the standalone entry points:

```powershell
.\scripts\install.ps1
.\scripts\update.ps1
.\scripts\uninstall.ps1
```

`update.ps1` and `uninstall.ps1` are thin wrappers around
`install.ps1 -Update` and `install.ps1 -Uninstall`. They forward every
additional argument, so you can pin a release or choose an install directory:

```powershell
.\scripts\update.ps1 -Version v1.2.3 -Dir C:\Tools\GoWork
.\scripts\uninstall.ps1 -Dir C:\Tools\GoWork
```

The wrappers contain no install logic of their own: path safety, checksum
verification, PATH handling and user data preservation all stay in
`install.ps1`.

Options:

| Parameter | Meaning |
| --- | --- |
| `-Update` | Replace an existing installation with the latest release or `-Version`. |
| `-Uninstall` | Remove the install directory and the user PATH entry. |
| `-Version <tag>` | Install a specific release, for example `-Version v1.2.3`. Defaults to `latest`. |
| `-Dir <path>` | Install somewhere other than `%LOCALAPPDATA%\Programs\GoWork`. |
| `-NoModifyPath` | Do not add the install directory to the user PATH. |
| `-Help` | Show installer help. |

The Windows installer verifies the downloaded ZIP against `checksums.txt` and
adds the install directory to the **user** PATH. Open a new terminal after
installing.

## Requirements

- Linux, macOS, or Windows on 64-bit x86 or ARM64.
- Installer on Linux/macOS: `bash`, `curl` or `wget`, `tar`, and
  `sha256sum` or `shasum`.
- Installer on Windows: PowerShell 5 or newer.
- Building from source: Go 1.25 or newer.
- Runtime: `ripgrep` (`rg`) on PATH is preferred by `grep_search`,
  `file_search`, and the project snapshot. A built-in Go fallback keeps them
  working when it is not installed.
- Optional: Python 3 and a virtual environment for PDF reading.

## Quick start

```sh
cd /path/to/project
gowork .
```

The project path is optional; without it GoWork uses the current directory.
On first launch, choose a provider and model with `ctrl+p`. Cloud providers
ask for an API key; local providers list the models their server exposes.

```sh
gowork /path/to/project   # open a specific project
gowork -S 12              # resume session 12
gowork -v                 # print the version
gowork -h                 # print command help
```

## Providers

| Provider | Notes |
| --- | --- |
| `google` | Gemini API. Requires `API_KEY`. |
| `anthropic` | Claude API. Requires `API_KEY`. |
| `groq` | Groq API. Requires `API_KEY`. |
| `openai` | OpenAI API. Requires `API_KEY`. |
| `opencode-go` | OpenCode Go subscription gateway. Uses the same `API_KEY` field and sends an `x-opencode-session` header to keep a conversation stable on the gateway. |
| `ollama` | Local Ollama server. No API key. |
| `llamaCpp` (llama.cpp) | Local llama.cpp server. No API key. |
| `lmStudio` (LM Studio) | Local LM Studio server. No API key. |

The provider picker loads the model list from the selected server, so local
models appear as soon as the server is running. Models that do not support
tool calling trigger a warning when selected.

For `llamaCpp` and `lmStudio`, the local-provider step also has a launch
control: highlight the provider and press `s` to start or stop its server.
GoWork looks for `llama-server` on `PATH` and for LM Studio's `lms` CLI, waits
for the health/model endpoint (bounded by `LOCAL_SERVER_START_TIMEOUT`), and
shows progress or errors on the picker's status line. Servers GoWork starts are
stopped when it exits; a server you launched yourself is detected and left
alone. Nothing is started automatically. Pass a model to llama.cpp with
`LLAMACPP_ARGS` (for example `-m /models/qwen.gguf`), and set
`LLAMACPP_COMMAND`/`LMSTUDIO_COMMAND` when the binary is not on `PATH` (LM
Studio installs its CLI under `~/.lmstudio/bin`).

## Configuration

GoWork reads environment variables and an optional `.env` file:

- `gowork /path/to/project` loads `/path/to/project/.env` at startup.
- `gowork` with no path loads `.env` from the current working directory.
- The provider picker writes `PROVIDER` and `model` (and a newly entered
  `API_KEY`) to `.env` in the **current working directory**. Launch from the
  project root if you want those choices loaded on the next run.
- Variables already exported in your shell take precedence over `.env`.

| Variable | Purpose | Default |
| --- | --- | --- |
| `API_KEY` | API key for cloud providers and OpenCode Go. | none |
| `PROVIDER` | `google`, `anthropic`, `groq`, `openai`, `opencode-go`, `ollama`, `llamaCpp`, or `lmStudio`. | none |
| `model` | Model identifier passed to the provider. `MODEL` is accepted as an alias. | none |
| `OLLAMA_HOST` | Ollama base URL. | `http://localhost:11434` |
| `LLAMACPP_HOST` | llama.cpp base URL. | `http://localhost:8080` |
| `LMSTUDIO_HOST` | LM Studio base URL. | `http://localhost:1234/v1` |
| `LLAMACPP_COMMAND` | `llama-server` executable used by the picker's `s` launch control. | detected on `PATH` |
| `LLAMACPP_ARGS` | Arguments for `llama-server`; JSON array or whitespace-separated. Include `-m <model.gguf>`. | none |
| `LMSTUDIO_COMMAND` | LM Studio `lms` executable used by the picker's `s` launch control. | detected on `PATH` |
| `LMSTUDIO_ARGS` | Arguments for `lms`; JSON array or whitespace-separated. | `server start` |
| `LOCAL_SERVER_START_TIMEOUT` | How long the launch control waits for a local server to become ready (`3m`, `45s`, or seconds). | `2m` |
| `GOWORK_CONFIG_DIR` | Override the durable per-user config directory that holds skills, `.mcp.json`, and `.lsp.json`. | platform config directory + `GoWork` |
| `GOWORK_ALLOW_PROJECT_PDF_SCRIPTS` | Trust the PDF parser script and venv inside the project being read. Off by default because that is project-controlled code. | off |

The agent loop is uncapped: it keeps running tool batches for as long as the
model requests them, and ctrl+i interrupts a running turn (ctrl+c quits).

`.env` is ignored by git, so keys stay out of the repository.

### Installing skills and MCP servers

GoWork can install both from GitHub, a raw HTTPS URL, or a local path without
leaving the app. The model exposes this through five tools:

| Tool | Effect |
| --- | --- |
| `skill_install` | Validate and install a `SKILL.md` into the user skills directory. Supports `path`, `ref`, `name`, `overwrite`, `load`, and `dry_run`. |
| `skill_load` | List installed skills and load or unload one in the current session. |
| `mcp_install` | Validate and merge an MCP JSON config into the user or project scope. Supports `source` or inline `config`, `path`, `ref`, `servers`, `overwrite`, `start`, and `dry_run`. |
| `mcp_list` | Show configured servers with scope, trust, connection state, tool count, and sanitized errors. |
| `mcp_remove` | Remove a configured server and stop its tools. |

Examples (the model emits these tool calls after you approve the permission
preview):

```json
{"source":"owner/repo","path":"skills/example/SKILL.md","ref":"<commit-sha>","load":true}
```

```json
{"source":"owner/repo","path":".mcp.json","ref":"<commit-sha>","start":true}
```

```json
{"config":{"mcpServers":{"files":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","."]}}},"start":true}
```

The bundled `gowork-bootstrap` skill is installed automatically on first run
into `$GOWORK_CONFIG_DIR/skills/gowork-bootstrap/SKILL.md` (default platform
config directory, for example `~/.config/GoWork/skills/...`). It is never
loaded automatically, and an existing copy is never overwritten, so local
edits survive upgrades. Load it with `skill_load`, or ask the model to install
a skill or MCP server and it will load the bootstrap skill first.

Security notes:

- Pin remote sources to a full commit SHA. Branches and tags can move.
- Downloaded skills and MCP configs are untrusted. `mcp_install` can start a
  server command or contact a remote host, so review the permission preview
  and the source before approving.
- Never put tokens, passwords, or `.env` contents in a source URL or MCP
  config. The tools redact URL credentials/query values, and `mcp_list` never
  shows env or header values.
- Project-scope MCP servers live in `<project>/.mcp.json`, are always
  untrusted, and never receive automatic tool approval. User-scope servers may
  opt into `trusted`/`autoApprove`; enable that only for a server you trust.
- The install tools are blocked in plan (read-only) mode. `skill_load` and
  `mcp_list` remain available for inspection.
- `dry_run` validates and reports without writing. Installs are atomic and
  private; existing skills/servers are kept unless `overwrite` is true.

### MCP servers and language servers

MCP configuration lives in the per-user config directory:
`$GOWORK_CONFIG_DIR/.mcp.json`, defaulting to the platform config directory
(for example `~/.config/GoWork/.mcp.json`). A `<project>/.mcp.json` is also
loaded when present, with project entries overriding user entries of the same
name and always requiring approval. LSP configuration lives beside the user
MCP file as `.lsp.json`. Files are created the first time you add an entry from
the Tools tab or with `mcp_install`. The legacy executable-adjacent
`.GoWork/skills` layout is migrated into the user config directory on startup.

`.mcp.json` uses the familiar `mcpServers` object:

```json
{
  "mcpServers": {
    "files": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "remote": {
      "type": "http",
      "url": "https://example.com/mcp"
    },
    "disabled-example": {
      "command": "some-server",
      "disabled": true
    }
  }
}
```

A server with `command` runs over stdio; a server with `url` uses Streamable
HTTP (with an SSE fallback). `env`, `headers`, and the optional `startupTimeout`,
`requestTimeout`, and `shutdownTimeout` fields are supported. Enabled servers
are started at launch, their tools are discovered through `tools/list`, and
each tool is registered with a provider-safe namespaced name such as
`mcp_files_read_file_1a2b3c4d`. A server that cannot start does not prevent
the app from launching: its error is shown in the Tools tab and the rest of
the tools keep working. Clients are stopped when the app exits.

`.lsp.json` stores user-configured language servers:

```json
{
  "servers": [
    {
      "name": "gopls",
      "command": "gopls",
      "args": [],
      "language_ids": ["go"],
      "enabled": true
    }
  ]
}
```

Enabled language servers expose `lsp_diagnostics`, `lsp_symbols`, and
`lsp_definition`. Server processes start lazily on the first LSP tool call and
are shut down when the app exits or the server is disabled/removed. `/lsp`
lists this configuration; `/lsp configure <name>` enables an existing entry or
adds a known server found on `PATH`.

## Keybinds

### Global

| Key | Action |
| --- | --- |
| `ctrl+p` | Open the provider and model picker. |
| `ctrl+t` | Jump to the Tools tab (or back to the code tab when it is active). |
| `ctrl+i` or `tab` while a turn is running | Interrupt the current generation or tool batch. |
| `tab` / `shift+tab` while idle | Next / previous tab. |
| `ctrl+l` | Open or close the changes list on the code tab. |
| `ctrl+shift+p` | Toggle read-only plan mode. Press it again to leave, or press `esc` from the idle status line. |
| `ctrl+c` | Quit. |

### Plan mode

Plan mode is a session-wide read-only switch. The status line shows its
`PLAN` badge while it is active. It blocks every mutating tool, including
`bash`, `edit_file`, and the create/write/move/delete tools, before the
permission box is shown. Read-only tools and `todo_list`'s `list` action keep
working, so the model can investigate and present a plan. Toggle it with
`ctrl+shift+p`; `esc` also leaves it when the prompt is not focused.

### Provider picker

| Key | Action |
| --- | --- |
| `enter` | Choose the highlighted provider or model. |
| `s` (local-provider step) | Start or stop the highlighted llama.cpp / LM Studio server. |
| `/` | Filter the list. |
| `esc` | Go back one step. |

### Prompt bar

| Key | Action |
| --- | --- |
| `enter` | Send the prompt. |
| `shift+enter` or `ctrl+j` | Insert a newline. |
| `up` on an empty prompt | Recall the previous prompt. |
| `ctrl+a` | Copy the prompt to the clipboard. |
| `ctrl+v` | Paste into the prompt. |
| `ctrl+u` | Clear the prompt. |
| `esc` | Leave prompt entry. |
| `up` / `down` (palette open) | Move the slash-command selection. |
| `tab` (palette open) | Complete the selected command. |
| `enter` (palette open) | Run the selected command. |
| `esc` (palette open) | Dismiss the palette without clearing the prompt. |

### Message area

| Key | Action |
| --- | --- |
| `ctrl+y` | Enter copy mode (starts on the newest message). |
| `up` / `down` (copy mode) | Move the message selection. |
| `shift+up` / `shift+down` (copy mode) | Extend or shrink the selected range. |
| `a` (copy mode) | Select every message. |
| `enter` or `ctrl+y` (copy mode) | Copy the selection to the clipboard. |
| `esc` (copy mode) | Cancel the selection. |
| left click | Select the clicked message; shift-click extends the range. |
| right click | Copy the clicked message immediately. |

### Changes list

| Key | Action |
| --- | --- |
| `up` / `down` | Move the selection. |
| `ctrl+a` | Accept the selected change, or every change in a file when its header is selected. |
| `ctrl+r` | Reject the selected change, or every change in a file when its header is selected. |
| `ctrl+f` | Accept all changes. |
| `ctrl+d` | Reject all changes. |
| `?` | Toggle the keybind help. |
| `esc`, `enter`, or `ctrl+l` | Close the changes list. |

### Permission box

| Key | Action |
| --- | --- |
| `left` / `right` | Choose accept, reject, or accept all. |
| `tab` or `down` | Focus the optional reason field. |
| `enter` | Confirm the decision. |
| `esc` | Reject. |

### Skills tab

| Key | Action |
| --- | --- |
| `a`, `l`, `u` | Show all, loaded, or unloaded skills. |
| `f` | Cycle the filter. |
| `/` or a printable key | Search skills. |
| `x` | Unload the selected skill. |
| `p` | Load the selected skill. |
| `i` or `enter` (list) | Focus the prompt bar. |
| `enter` (prompt, normal text) | Open the new-skill form; the prompt text becomes the skill body. |
| `enter` (prompt, starts with `/`) | Run the slash command instead of opening the form. |
| `ctrl+n` | Open the new-skill form, even when the prompt is not focused. |
| `esc` | Leave the tab. |

### Tools tab

Open the Tools tab with `ctrl+t` or `/tools`; `ctrl+t` toggles back to the code
tab when the Tools tab is active. It lists Skills, MCP servers, and LSP servers
in three sections. Status lines report loaded/enabled counts, MCP connection
state and discovered tool counts, and any startup or configuration error.
Configuration changes are written immediately and the model-visible tool list
is refreshed.

| Key | Action |
| --- | --- |
| `left` / `right`, `h` / `l` | Move between the Skills, MCP, and LSP sections. |
| `1`, `2`, `3` | Jump directly to Skills, MCP, or LSP. |
| `up` / `down`, `k` / `j` | Select a row. |
| `space` or `enter` | Load/unload a skill, or enable/disable an MCP or LSP server. |
| `p` / `x` | Load / unload the selected skill. |
| `a` | Add an MCP server (MCP section) or language server (LSP section). |
| `d` | Remove the selected MCP or LSP server. |
| `c` | Create a skill from the prompt bar text. |
| `i` | Focus the prompt bar for the new skill's body. |
| `tab` / `shift+tab` (form closed) | Switch tabs. |
| `esc` | Close the add form, or leave the tab. |

### Stats tab

| Key | Action |
| --- | --- |
| `/` or a printable key | Filter models. |
| `up` / `down` | Move between models. |
| `left` / `right` | Move the day selection on the usage chart. |
| `enter` | Open the selected model's details. |
| `esc` | Close a view or leave the tab. |

## Slash commands

Type `/` in the prompt bar to open the command palette. Matching commands
appear above the prompt; `up`/`down` select, `tab` completes, `enter` runs the
selection, and `esc` dismisses the palette. Command text is handled by the app
and never sent to the model.

| Command | Action |
| --- | --- |
| `/help` | Show the keybind and command help modal. |
| `/model` | Open the provider/model picker. |
| `/stats` | Switch to the usage dashboard. |
| `/skills` | Switch to the skills tab. |
| `/newskill [body]` | Open the new-skill form; optional arguments seed the skill body. |
| `/tools` | Switch to the tools tab (skills, MCP, LSP). |
| `/save` | Save the current session to the local database immediately. |
| `/new` | Clear the conversation context and start a new session record. |
| `/fork` | Fork the current context into a new session record. |
| `/compact` | Condense the conversation now. |
| `/context [path]` | Show estimated context tokens, or add a file (bounded to 32 KiB) to the conversation. |
| `/coffee` | Play the coffee-break animation; `esc` or `ctrl+c` stops it. |
| `/lsp [list\|configure <server>]` | List the configured language servers, or enable/configure one. `configure` also adds a known server detected on `PATH`. |

## Tools

These are the exact tool names the model can call:

### Tool approval

These run without a permission prompt: `read_file`, `grep_search`,
`file_search`, `list_directory`, `todo_list`, `questions_tool`, `skill`,
`skill_load`, `mcp_list`, and the LSP tools.

These open the permission box before running: `bash`, `edit_file`,
`write_file`, `create_file_tool`, `move_file`, `delete_file`, `web_fetch`,
`web_search`, `skill_install`, `mcp_install`, and `mcp_remove`. MCP tools are
prompted per call unless a user-scope server opts into `trusted`/`autoApprove`;
project-scope servers are never auto-approved. Accept All in the permission box
auto-approves the remaining calls in that turn only. Plan mode blocks mutating
tools before this prompt, regardless of the decision. `questions_tool` pauses
the turn for answers even though it needs no approval.

| Tool | Purpose |
| --- | --- |
| `read_file` | Read a text file by line range. PDFs are converted to Markdown; images are refused because the provider layer is text-only. |
| `write_file` | Replace an exact, unique text snippet in an existing file. Requires approval. |
| `edit_file` | Replace an inclusive, 1-indexed line range in an existing file. Requires approval. |
| `create_file_tool` | Create a new file with complete, non-empty content; a path ending in `/` creates a directory. Requires approval. |
| `move_file` | Move or rename a file. Requires approval. |
| `delete_file` | Delete a file and remove its parent directory if that directory becomes empty. Requires approval. |
| `file_search` | Find files by name or path pattern. |
| `list_directory` | List a directory's immediate entries. |
| `grep_search` | Search file contents with ripgrep when available, with an in-process fallback. Respects `.gitignore` and `.agentignore`. |
| `bash` | Run a shell command after per-call approval. Only `working_dir` is confined; the command itself is not sandboxed, may touch other paths, and may use the network. A best-effort deny list blocks common download/admin/install commands. Long commands can move to the background. |
| `todo_list` | Maintain the session's short task list. |
| `questions_tool` | Ask up to 7 questions with up to 3 options each and pause the turn for the answer. |
| `skill` | Load one of the session's loaded skills. |
| `skill_install` | Install a `SKILL.md` from GitHub, a raw URL, or a project-relative local path. Requires approval; supports `dry_run`. |
| `skill_load` | List installed skills and load or unload one for the session. Read-only on disk. |
| `mcp_install` | Install MCP servers from a remote/local config or inline JSON. Requires approval; supports `dry_run` and optional immediate start. |
| `mcp_list` | List configured MCP servers with sanitized status. Read-only and allowed in plan mode. |
| `mcp_remove` | Remove a configured MCP server and stop its tools. Requires approval. |
| `mcp_<server>_<tool>_<hash>` | Any tool exposed by a connected MCP server. Requires approval unless a trusted user-scope server opts into auto-approval. The original tool name is used for the actual MCP request. |
| `lsp_diagnostics` | Return errors and warnings for a project file from the configured language server. |
| `lsp_symbols` | List symbols in a project file. |
| `lsp_definition` | Find the definition of a symbol at a file position. |
| `web_search` | Search the web through DuckDuckGo Lite. Requires permission. |
| `web_fetch` | Fetch a URL, extract readable text, and summarize or cache long pages. Requires permission. |

## Building and development

```sh
make build      # build out/GoWork
make run        # go run .
make test       # go test -v ./...
make venv       # create tools/Scripts/venv and install PDF dependencies
make init_db    # create and seed Db/gowork.db (requires sqlite3)
make clean_db   # remove Db/gowork.db
make clean      # remove out/ and the Python venv
```

The equivalent direct checks are:

```sh
gofmt -l .      # must print nothing
go build ./...
go vet ./...
go test ./...
```

`make build` writes the binary to `out/GoWork`. Copy it next to the `prompts/`,
`logo/`, `skills/`, and `tools/` folders, or use the installer, if you want to
run it outside a checkout.

## Data paths

- **Sessions and usage:** `Db/gowork.db` inside the project you open. The file
  is created on first run and is never removed by the installer.
- **Skills:** `$GOWORK_CONFIG_DIR/skills/<name>/SKILL.md`. The default config
  directory is the platform user-config directory plus `GoWork`:
  `~/.config/GoWork` on Linux, `~/Library/Application Support/GoWork` on macOS,
  and `%AppData%\GoWork` on Windows. Each installed skill also gets a
  `provenance.json` recording the sanitized source, ref, and content hash.
  Existing user-modified copies are never overwritten. The old
  `<exe>/.GoWork/skills` layout is copied into this directory on startup when
  files are missing.
- **MCP and LSP configuration:** `$GOWORK_CONFIG_DIR/.mcp.json` and
  `$GOWORK_CONFIG_DIR/.lsp.json` (for example `~/.config/GoWork/.mcp.json`).
  A project-scope `<project>/.mcp.json` is merged in when present; project
  entries override user entries and are never auto-approved. The Tools tab,
  `/lsp`, and the install tools write configuration atomically with private
  permissions.
- **Bundled assets:** `prompts/`, `logo/`, `skills/`, and `tools/Scripts/` next
  to the executable. Source checkouts fall back to the repository root and
  then the current directory, so `go run .` keeps working.
- **Fetched pages:** `.agent-cache/fetch/` in the project root when a long web
  page is saved for later reading.
- **PDF parser:** `tools/Scripts/read_pdf.py` and its optional
  `tools/Scripts/venv` are resolved from the app directory next to the
  executable, then from the system Python. The project's own parser script and
  venv are only used when `GOWORK_ALLOW_PROJECT_PDF_SCRIPTS=1` is set.

### PDF support

PDF parsing is optional. Install one of the backends:

```sh
# lightweight, no GPU required
pip install pymupdf4llm
```

```sh
# higher accuracy for OCR, tables, and equations; much larger
pip install marker-pdf
```

For a source checkout, `make venv` creates `tools/Scripts/venv` and installs
the lightweight backend from `tools/Scripts/requirements.txt`. For an
installed copy, create a venv at `<install directory>/tools/Scripts/venv` or
install the package into the system Python that GoWork finds on PATH.

The parser script and venv inside the project being read are not used by
default: they are code controlled by that project. To trust them anyway, set
`GOWORK_ALLOW_PROJECT_PDF_SCRIPTS=1`.

### Bash environment

Every `bash` call is shown in the permission box with its command preview
before it runs. The child shell receives a scrubbed environment: variables
whose names look like credentials (API keys, tokens, passwords, secrets) are
removed, so running `env` does not print the agent's provider keys. The
scrubbing is name-based and is not a security boundary; the approved command
can still read files the user account can access, including a project `.env`.

## Roadmap

- Spreadsheet and document editing beyond plain text.
- Image understanding for models that support it.
