# GoWork

![GoWork logo](logo/logo.svg)

GoWork is a terminal AI coding agent for real project work. It runs a
streaming agent loop against cloud or local models, edits files through
guarded tools, and keeps every change reviewable before it lands.

## Features

- **Streaming agent loop.** Replies stream token by token. Independent tool
  calls run in parallel, their results go back to the model, and the loop
  continues until the task is finished. `MAX_AGENT_STEPS` caps a request at 40
  model turns by default, and one reply can request up to 10 tools.
- **Project-confined tools.** File operations are rooted at the project you
  open. Destructive file changes and web access require approval in the
  permission box, with an optional note for the model.
- **Reviewable changes.** Every edit is tracked as a diff. Accept or reject a
  single change, all changes in a file from its header row, or the whole batch.
- **Skills.** Reusable instruction sets live as `SKILL.md` files. Load the ones
  a session needs from the Skills tab; the model receives them through the
  `skill` tool.
- **Stats and session history.** Token usage and session messages are stored
  in SQLite. The Stats tab shows per-model usage, and `-S` restores a previous
  session with its messages and loaded skills.
- **Cloud and local models.** Google, Anthropic, Groq, OpenAI, OpenCode Go,
  Ollama, llama.cpp, and LM Studio are supported out of the box.
- **Terminal-first interface.** Tabs for code, skills, and stats, a todo
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

From a checkout, run the script directly:

```sh
scripts/install.sh
scripts/install.sh --update
scripts/install.sh --uninstall
```

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

```powershell
.\scripts\install.ps1 -Update
.\scripts\install.ps1 -Uninstall
```

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
- Runtime: `ripgrep` (`rg`) on PATH for `grep_search` and the project snapshot.
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
| `MAX_AGENT_STEPS` | Maximum model turns per request before the agent loop stops. | `40` |

`.env` is ignored by git, so keys stay out of the repository.

## Keybinds

### Global

| Key | Action |
| --- | --- |
| `ctrl+p` | Open the provider and model picker. |
| `ctrl+i` or `tab` while a turn is running | Interrupt the current generation or tool batch. |
| `tab` / `shift+tab` while idle | Next / previous tab. |
| `ctrl+l` | Open or close the changes list on the code tab. |
| `ctrl+c` | Quit. |

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
| `i` or `enter` | Focus the prompt bar. |
| `esc` | Leave the tab. |

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
| `/save` | Save the current session to the local database immediately. |
| `/new` | Clear the conversation context and start a new session record. |
| `/fork` | Fork the current context into a new session record. |
| `/compact` | Condense the conversation now. |
| `/context [path]` | Show estimated context tokens, or add a file (bounded to 32 KiB) to the conversation. |
| `/coffee` | Play the coffee-break animation; `esc` or `ctrl+c` stops it. |
| `/lsp [list\|configure <server>]` | List or configure language servers (reports none configured until LSP wiring lands). |

## Tools

These are the exact tool names the model can call:

| Tool | Purpose |
| --- | --- |
| `read_file` | Read a text file by line range. PDFs are converted to Markdown; images are refused because the provider layer is text-only. |
| `write_file` | Replace an exact, unique text snippet in an existing file. |
| `edit_file` | Replace an inclusive, 1-indexed line range in an existing file. |
| `create_file_tool` | Create a new file with complete, non-empty content; a path ending in `/` creates a directory. |
| `move_file` | Move or rename a file. |
| `delete_file` | Delete a file and remove its parent directory if that directory becomes empty. |
| `file_search` | Find files by name or path pattern. |
| `list_directory` | List a directory's immediate entries. |
| `grep_search` | Search file contents with ripgrep, respecting `.gitignore` and `.agentignore`. |
| `bash` | Run a shell command inside the project. Network, download, admin, and global-install commands are blocked. Long commands can move to the background. |
| `todo_list` | Maintain the session's short task list. |
| `questions_tool` | Ask up to 7 questions with up to 3 options each and pause the turn for the answer. |
| `skill` | Load one of the session's loaded skills. |
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
go build ./...
go vet ./...
go test ./...
```

`make build` writes the binary to `out/GoWork`. Copy it next to the `prompts/`,
`logo/`, and `tools/` folders, or use the installer, if you want to run it
outside a checkout.

## Data paths

- **Sessions and usage:** `Db/gowork.db` inside the project you open. The file
  is created on first run and is never removed by the installer.
- **Skills:** `.GoWork/skills/<name>/SKILL.md` next to the installed
  executable. Project files are not used for GoWork's own skill storage.
- **Bundled assets:** `prompts/`, `logo/`, and `tools/Scripts/` next to the
  executable. Source checkouts fall back to the repository root and then the
  current directory, so `go run .` keeps working.
- **Fetched pages:** `.agent-cache/fetch/` in the project root when a long web
  page is saved for later reading.
- **PDF parser:** `tools/Scripts/read_pdf.py` and its optional
  `tools/Scripts/venv` are resolved next to the executable first, then in the
  project, then in the current directory.

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

## Roadmap

- Slash commands and an in-app command palette.
- Spreadsheet and document editing beyond plain text.
- Image understanding for models that support it.
