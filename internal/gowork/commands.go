package gowork

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	coffee "GoWork/Tui/Components/Coffee"
	"GoWork/Tui/Components/CommandPalette"
	dialog "GoWork/Tui/Components/Dialog"
	"GoWork/providers"
)

func fetchModelInfoCmd(p providers.Provider, model string, gens ...uint64) tea.Cmd {
	var gen uint64
	if len(gens) > 0 {
		gen = gens[0]
	}
	return func() tea.Msg {
		info, err := p.Info(context.Background(), model)
		return modelInfoMsg{info: info, err: err, gen: gen, model: model}
	}
}

// toolSupportWarningCmd opens the centered modal when the selected model can't
// call tools. No-op if the provider/model is "None" (i.e. nothing was picked).
func toolSupportWarningCmd(modelName, providerName string) tea.Cmd {
	if providerName == "" || modelName == "" ||
		strings.EqualFold(providerName, "None") || strings.EqualFold(modelName, "None") {
		return nil
	}
	if providers.ModelSupportsTools(providerName, modelName) {
		return nil
	}
	return func() tea.Msg {
		return dialog.ShowMsg{
			Title:   "Tool calling unsupported",
			Message: fmt.Sprintf("%s doesn't support tool calling.", modelName),
			Buttons: []string{"OK"},
		}
	}
}

// maxContextFileBytes bounds how much of an external file /context injects
// into the conversation so one huge file can't blow the window.
const maxContextFileBytes = 32 * 1024

// LSPServer is one language server as reported by the injected manager.
type LSPServer struct {
	Name     string
	Language string
	Status   string
}

// LSPManager is the small seam the /lsp command uses. The real LSP package
// can implement it later without touching the command layer.
type LSPManager interface {
	Servers() []LSPServer
	Configure(name string) error
}

// noopLSPManager covers tests and callers that build a model without the
// runtime; it reports no servers and a clear error for configure attempts.
type noopLSPManager struct{}

func (noopLSPManager) Servers() []LSPServer { return nil }

func (noopLSPManager) Configure(name string) error {
	return fmt.Errorf("LSP support is unavailable in this session")
}

// slashCommandSpecs is the single registry shared by the palette and /help.
var slashCommandSpecs = []commandpalette.Command{
	{Name: "help", Usage: "/help", Description: "Show keybinds and slash commands"},
	{Name: "model", Usage: "/model", Description: "Pick the provider and model"},
	{Name: "stats", Usage: "/stats", Description: "Open the usage dashboard"},
	{Name: "skills", Usage: "/skills", Description: "Open the skills tab"},
	{Name: "tools", Usage: "/tools", Description: "Open the tools tab (skills, MCP, LSP)"},
	{Name: "save", Usage: "/save", Description: "Save the current session now"},
	{Name: "new", Usage: "/new", Description: "Clear context and start a new session"},
	{Name: "fork", Usage: "/fork", Description: "Fork the current context into a new session"},
	{Name: "compact", Usage: "/compact", Description: "Condense the conversation now"},
	{Name: "context", Usage: "/context [path]", Description: "Show context usage or add a file"},
	{Name: "coffee", Usage: "/coffee", Description: "Take a short coffee break"},
	{Name: "lsp", Usage: "/lsp [configure name]", Description: "List or configure language servers"},
}

// slashCommands returns a copy of the command registry for the prompt bar.
func slashCommands() []commandpalette.Command {
	return append([]commandpalette.Command(nil), slashCommandSpecs...)
}

// parseSlashCommand splits a prompt line such as `/context "docs/a b.md"`
// into a lowercased command name and arguments. Quotes and backslash escapes
// are supported so paths with spaces work.
func parseSlashCommand(input string) (name string, args []string, err error) {
	s := strings.TrimSpace(input)
	if !strings.HasPrefix(s, "/") {
		return "", nil, fmt.Errorf("not a slash command")
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, "/"))
	if rest == "" {
		return "", nil, nil
	}
	parts, err := splitCommandArgs(rest)
	if err != nil {
		return "", nil, err
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	return strings.ToLower(parts[0]), parts[1:], nil
}

func splitCommandArgs(s string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		quote   rune
		escaped bool
		started bool
	)
	flush := func() {
		if started {
			args = append(args, current.String())
			current.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			started = true
		case r == '\\':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			started = true
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return args, nil
}

func (m *model) commandBusy() bool {
	return m.AiThink != nil && *m.AiThink
}

// runSlashCommand executes one command line. Model mutations land immediately;
// the returned command handles work that must leave the update loop.
func (m *model) runSlashCommand(input string) tea.Cmd {
	name, args, err := parseSlashCommand(input)
	if err != nil {
		m.MessageArea.AppendMessage(">**ERROR** "+err.Error(), false)
		return nil
	}
	if name == "" {
		m.MessageArea.AppendMessage(">**INFO** Type a command after the slash, or press tab to pick one.", false)
		return nil
	}

	switch name {
	case "help":
		return func() tea.Msg {
			return dialog.ShowMsg{
				Title:   "Help",
				Message: helpText(),
				Buttons: []string{"OK"},
			}
		}
	case "model":
		if m.commandBusy() {
			m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before switching providers.", false)
			return nil
		}
		return m.openProviderSelect()
	case "stats":
		return m.selectTabCmd("stats")
	case "skills":
		return m.selectTabCmd("skills")
	case "tools":
		return m.selectTabCmd("tools")
	case "save":
		if m.commandBusy() {
			m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before saving.", false)
			return nil
		}
		// /save is a checkpoint, not a session boundary: persist the current
		// history and usage without sealing the row, starting a new one, or
		// touching the token counters. The final save on exit rewrites the
		// same snapshot, so nothing is duplicated.
		if err := m.saveSessionSnapshot(); err != nil {
			m.MessageArea.AppendMessage(">**ERROR** Could not save session: "+err.Error(), false)
			return nil
		}
		m.MessageArea.AppendMessage(">**INFO** Session saved to the local database.", false)
		return nil
	case "new":
		if m.commandBusy() {
			m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before starting a new session.", false)
			return nil
		}
		if err := m.snapshotSession(); err != nil {
			m.MessageArea.AppendMessage(">**ERROR** Could not start a new session: "+err.Error(), false)
			return nil
		}
		m.Context = nil
		m.status.sessionTokens = 0
		m.status.lastPromptTokens = 0
		m.historyIdx = 0
		m.MessageArea.AppendMessage(">**INFO** New session started - context cleared.", false)
		return nil
	case "fork":
		if m.commandBusy() {
			m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before forking.", false)
			return nil
		}
		if err := m.snapshotSession(); err != nil {
			m.MessageArea.AppendMessage(">**ERROR** Could not fork session: "+err.Error(), false)
			return nil
		}
		m.Context = append([]providers.Message(nil), m.Context...)
		m.status.sessionTokens = 0
		m.status.lastPromptTokens = providers.EstimateContextTokens(m.Context, m.status.modelID)
		m.MessageArea.AppendMessage(">**INFO** Forked the current context into a new session.", false)
		return nil
	case "compact":
		return m.startManualCompaction()
	case "context":
		return m.contextCommand(args)
	case "coffee":
		return m.Coffee.Start(coffee.Config{})
	case "lsp":
		return m.lspCommand(args)
	default:
		m.MessageArea.AppendMessage(
			fmt.Sprintf(">**ERROR** Unknown command /%s. Type /help to see the available commands.", name), false)
		return nil
	}
}

// saveSessionSnapshot persists the current session as a checkpoint without
// ending it. Used by /save: unlike snapshotSession it never opens a new row,
// so the running token counters and history stay attached to the same session.
func (m *model) saveSessionSnapshot() error {
	if m.Db == nil {
		return fmt.Errorf("database unavailable")
	}
	messages, _ := repairContext(m.Context)
	return m.Db.FlushSession(messages, m.status.sessionTokens)
}

// snapshotSession finalizes the current session (messages, usage and skills)
// and immediately opens a fresh row so the app keeps recording afterwards.
// Only explicit new-session actions (/new, /fork) use it.
func (m *model) snapshotSession() error {
	if m.Db == nil {
		return fmt.Errorf("database unavailable")
	}
	if err := m.Db.FinalizeSession(m.Context, m.status.sessionTokens); err != nil {
		return err
	}
	return m.Db.StartSession(m.status.modelID, m.status.providerName)
}

func (m *model) selectTabCmd(name string) tea.Cmd {
	for i, tab := range m.Tabs.Tabs {
		if tab.Name == name {
			m.Tabs.ActiveIdx = i
			m.syncModeToTab()
			return nil
		}
	}
	m.MessageArea.AppendMessage(">**ERROR** No such tab: "+name, false)
	return nil
}

func (m *model) startManualCompaction() tea.Cmd {
	if m.commandBusy() {
		m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before compacting.", false)
		return nil
	}
	if len(m.Context) == 0 {
		m.MessageArea.AppendMessage(">**INFO** There is no conversation context to compact yet.", false)
		return nil
	}
	var p providers.Provider
	if m.model != nil {
		p = m.model.Get()
	}
	if p == nil {
		m.MessageArea.AppendMessage(">**ERROR** No provider selected - press ctrl+p to pick one.", false)
		return nil
	}
	*m.AiThink = true
	m.MessageArea.AppendMessage(">**INFO** Condensing the conversation...", false)
	return tea.Batch(m.spinner.Tick, compactCmd(p, m.Context, m.status.contextWindow, m.status.modelID))
}

func (m *model) contextCommand(args []string) tea.Cmd {
	if len(args) == 0 {
		est := providers.EstimateContextTokens(m.Context, m.status.modelID)
		line := fmt.Sprintf(">**INFO** Estimated context: %d tokens", est)
		if w := m.status.contextWindow; w > 0 {
			pct := est * 100 / w
			if pct < 0 {
				pct = 0
			}
			line += fmt.Sprintf(" (%d%% of the %d-token window)", pct, w)
		}
		m.MessageArea.AppendMessage(line+".", false)
		return nil
	}
	if m.commandBusy() {
		m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before adding files to context.", false)
		return nil
	}
	arg := strings.Join(args, " ")
	rel, err := safeContextPath(arg)
	if err != nil {
		m.MessageArea.AppendMessage(">**ERROR** Could not read "+arg+": "+err.Error(), false)
		return nil
	}
	// Anchor the read at the project root: os.Root refuses absolute names,
	// ".." escapes and symlinks that point outside, so /context cannot be
	// used to pull an arbitrary host file into the conversation.
	root, err := os.OpenRoot(m.root)
	if err != nil {
		m.MessageArea.AppendMessage(">**ERROR** Could not read "+arg+": "+err.Error(), false)
		return nil
	}
	defer root.Close()
	content, truncated, err := readBoundedTextInRoot(root, rel, maxContextFileBytes)
	if err != nil {
		m.MessageArea.AppendMessage(">**ERROR** Could not read "+arg+": "+err.Error(), false)
		return nil
	}
	note := ""
	if truncated {
		note = fmt.Sprintf("\n\n[file truncated at %d bytes]", maxContextFileBytes)
	}
	m.Context = append(m.Context, providers.Message{
		Role:    "user",
		Content: "File added to context: " + arg + "\n\n" + content + note,
	})
	suffix := ""
	if truncated {
		suffix = ", truncated"
	}
	m.MessageArea.AppendMessage(
		fmt.Sprintf(">**INFO** Added %s to the conversation (%d bytes%s).", arg, len(content), suffix), false)
	return nil
}

// safeContextPath validates a /context argument and returns the project-
// relative name to hand to os.Root. Absolute paths and any ".." component are
// rejected up front, even when the cleaned path would stay inside the root:
// such arguments are almost always an attempt to reach outside the project,
// and os.Root's own protection should not be the only line of defence.
func safeContextPath(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(arg) || filepath.VolumeName(arg) != "" {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	for _, part := range strings.Split(filepath.ToSlash(arg), "/") {
		if part == ".." {
			return "", fmt.Errorf("path must stay inside the project")
		}
	}
	clean := filepath.Clean(arg)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay inside the project")
	}
	return clean, nil
}

func readBoundedText(path string, max int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	return readBoundedReader(f, max)
}

// readBoundedTextInRoot reads a project-relative file through an os.Root so
// the kernel-enforced root cannot be escaped by an absolute path, a ".."
// component, or a symlink pointing outside the project.
func readBoundedTextInRoot(root *os.Root, name string, max int) (string, bool, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	return readBoundedReader(f, max)
}

func readBoundedReader(r io.Reader, max int) (string, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(data) > max
	if truncated {
		data = data[:max]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", false, fmt.Errorf("binary file, not added")
	}
	return string(data), truncated, nil
}

func (m *model) lspCommand(args []string) tea.Cmd {
	mgr := m.LSP
	if mgr == nil {
		mgr = noopLSPManager{}
	}
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "configure":
			if len(args) < 2 {
				m.MessageArea.AppendMessage(">**INFO** Usage: /lsp configure <server>", false)
				return nil
			}
			if m.commandBusy() {
				m.MessageArea.AppendMessage(">**INFO** Wait for the current turn to finish before changing language servers.", false)
				return nil
			}
			if err := mgr.Configure(args[1]); err != nil {
				m.MessageArea.AppendMessage(">**ERROR** Could not configure LSP server "+args[1]+": "+err.Error(), false)
				return nil
			}
			if m.toolRuntime != nil {
				m.afterToolChange()
			}
			m.MessageArea.AppendMessage(">**INFO** Configured LSP server "+args[1]+".", false)
			return nil
		case "list":
			// fall through to the listing below
		default:
			m.MessageArea.AppendMessage(">**INFO** Usage: /lsp [list | configure <server>]", false)
			return nil
		}
	}
	servers := mgr.Servers()
	if len(servers) == 0 {
		m.MessageArea.AppendMessage(">**INFO** No language servers configured. Add one from the Tools tab (ctrl+t, then a).", false)
		return nil
	}
	var b strings.Builder
	b.WriteString(">**INFO** Language servers:\n")
	for _, s := range servers {
		fmt.Fprintf(&b, "\n- %s (%s): %s", s.Name, s.Language, s.Status)
	}
	m.MessageArea.AppendMessage(b.String(), false)
	return nil
}

func helpText() string {
	var b strings.Builder
	b.WriteString("Slash commands\n")
	for _, c := range slashCommandSpecs {
		fmt.Fprintf(&b, "  %-22s %s\n", c.Usage, c.Description)
	}
	b.WriteString(`
Keys
  enter                    send prompt / run command
  shift+enter, ctrl+j      newline
  ctrl+a                   copy prompt
  ctrl+y                   select/copy messages
  ctrl+u                   clear prompt
  ctrl+p                   provider/model picker
  ctrl+t                   tools tab (skills, MCP, LSP)
  ctrl+l                   changes list
  ctrl+shift+p             toggle plan (read-only) mode
  ctrl+i, tab (running)    interrupt the AI
  tab / shift+tab          switch tabs
  ctrl+c                   quit`)
	return b.String()
}
