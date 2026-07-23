package providerselect

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
	"GoWork/providers"
)

type step int

const (
	stepProvider step = iota
	stepLocal
	stepAPIKeyChoice
	stepAPIKeyInput
	stepModels
	stepLoading
)

const (
	floatWidth  = 56
	floatHeight = 18
)

// SelectedMsg is emitted when the user finishes the flow.
type SelectedMsg struct {
	Provider    string
	ModelID     string
	APIKey      string // empty if they reused the existing .env key
	WroteNewKey bool   // true when APIKey should be persisted
}

// CancelledMsg is emitted when the user backs out of the first step.
type CancelledMsg struct{}

type modelsLoadedMsg struct {
	loadID int
	models []providers.ModelInfo
	err    error
}

type item struct {
	title string
	desc  string
	id    string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

// Model is the multi-step provider/model picker overlay.
type Model struct {
	step step

	providers      []string
	localProviders []string
	envAPIKey      string

	draftProvider string
	draftKey      string
	wroteNewKey   bool
	isLocal       bool

	// loadID increments on every ListModels request so late responses
	// from a cancelled/undone load are ignored.
	loadID int

	list     list.Model
	keyInput textinput.Model
	errMsg   string

	// terminal area the float is placed into (usually full window)
	termWidth  int
	termHeight int
}

func New(cloudProviders, localProviders []string, envAPIKey string) Model {
	ti := textinput.New()
	ti.Placeholder = "sk-..."
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 512
	ti.Prompt = "> "

	m := Model{
		step:           stepProvider,
		providers:      cloudProviders,
		localProviders: localProviders,
		envAPIKey:      envAPIKey,
		keyInput:       ti,
	}
	m.list = m.newList("Select provider", stringItems(cloudProviders))
	return m
}

func stringItems(names []string) []list.Item {
	items := make([]list.Item, 0, len(names))
	for _, n := range names {
		items = append(items, item{title: n, id: n})
	}
	return items
}

func modelItems(models []providers.ModelInfo) []list.Item {
	items := make([]list.Item, 0, len(models))
	for _, m := range models {
		id := m.ID
		if id == "" {
			continue
		}
		title := id
		desc := ""
		if m.DisplayName != "" && m.DisplayName != id {
			title = m.DisplayName
			desc = id
		}
		if m.ContextWindow > 0 {
			if desc != "" {
				desc += " · "
			}
			desc += fmt.Sprintf("%d ctx", m.ContextWindow)
		}
		items = append(items, item{title: title, desc: desc, id: id})
	}
	return items
}

func (m Model) dialogWidth() int {
	w := floatWidth
	if m.termWidth > 0 && m.termWidth-4 < w {
		w = max(24, m.termWidth-4)
	}
	return w
}

func (m Model) dialogHeight() int {
	h := floatHeight
	if m.termHeight > 0 && m.termHeight-2 < h {
		h = max(10, m.termHeight-2)
	}
	return h
}

func (m Model) listHeight() int {
	// title + help + padding/border leave ~6 rows for chrome inside the float
	h := m.dialogHeight() - 8
	if h < 4 {
		h = 4
	}
	return h
}

func (m *Model) newList(title string, items []list.Item) list.Model {
	showDesc := false
	for _, it := range items {
		if i, ok := it.(item); ok && i.desc != "" {
			showDesc = true
			break
		}
	}
	delegate := panelDelegate{showDesc: showDesc}

	innerW := max(1, m.dialogWidth()-6)
	l := list.New(items, delegate, innerW, m.listHeight())
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()
	l.Styles = panelListStyles()
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(style.Background).
		Background(style.Info).
		Padding(0, 1).
		Bold(true)
	return l
}

// panelDelegate paints every row to the full list width so selection
// chrome doesn't leave a narrow strip. Background matches the app bg.
type panelDelegate struct {
	showDesc bool
}

func (d panelDelegate) Height() int {
	if d.showDesc {
		return 2
	}
	return 1
}

func (d panelDelegate) Spacing() int { return 0 }

func (d panelDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d panelDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}

	selected := index == m.Index()
	row := lipgloss.NewStyle().
		Foreground(style.Text).
		Background(style.Background).
		PaddingLeft(2).
		Width(max(1, m.Width()))

	if selected {
		row = lipgloss.NewStyle().
			Foreground(style.Info).
			Background(style.Background).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(style.Info).
			PaddingLeft(1).
			Bold(true).
			Width(max(1, m.Width()))
	}

	line := row.Render(it.title)
	if d.showDesc && it.desc != "" {
		descStyle := lipgloss.NewStyle().
			Foreground(style.Muted).
			Background(style.Background).
			PaddingLeft(2).
			Width(max(1, m.Width()))
		if selected {
			descStyle = lipgloss.NewStyle().
				Foreground(style.Special).
				Background(style.Background).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(style.Info).
				PaddingLeft(1).
				Width(max(1, m.Width()))
		}
		line += "\n" + descStyle.Render(it.desc)
	}
	fmt.Fprint(w, line)
}

func panelListStyles() list.Styles {
	s := list.DefaultStyles(true)
	// No per-chrome backgrounds — the dialog shell owns the fill.
	s.TitleBar = lipgloss.NewStyle().Padding(0, 0, 1, 0)
	s.StatusBar = lipgloss.NewStyle().Foreground(style.Muted)
	s.StatusEmpty = lipgloss.NewStyle().Foreground(style.Muted)
	s.NoItems = lipgloss.NewStyle().Foreground(style.Muted).PaddingLeft(2)
	s.PaginationStyle = lipgloss.NewStyle().Foreground(style.Muted)
	s.HelpStyle = lipgloss.NewStyle()
	s.Filter = textinput.DefaultStyles(true)
	s.Filter.Focused.Text = lipgloss.NewStyle().Foreground(style.Text)
	s.Filter.Focused.Prompt = lipgloss.NewStyle().Foreground(style.Info)
	s.Filter.Focused.Placeholder = lipgloss.NewStyle().Foreground(style.Muted)
	s.Filter.Blurred.Text = lipgloss.NewStyle().Foreground(style.Text)
	s.Filter.Blurred.Prompt = lipgloss.NewStyle().Foreground(style.Info)
	s.Filter.Blurred.Placeholder = lipgloss.NewStyle().Foreground(style.Muted)
	s.Filter.Cursor.Color = style.Info
	return s
}

func (m *Model) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
	m.list.SetSize(max(1, m.dialogWidth()-6), m.listHeight())
	m.keyInput.SetWidth(max(10, m.dialogWidth()-8))
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case modelsLoadedMsg:
		if msg.loadID != m.loadID || m.step != stepLoading {
			return m, nil
		}
		m.step = stepModels
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.list = m.newList("Models (failed to load)", nil)
			return m, nil
		}
		items := modelItems(msg.models)
		if len(items) == 0 {
			m.errMsg = "no models returned"
		} else {
			m.errMsg = ""
		}
		m.list = m.newList("Select model", items)
		return m, nil

	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "ctrl+c":
			return m, func() tea.Msg { return CancelledMsg{} }
		case "esc":
			return m.undo()
		}

		if m.step == stepAPIKeyInput {
			if key == "enter" {
				val := strings.TrimSpace(m.keyInput.Value())
				if val == "" {
					m.errMsg = "API key cannot be empty"
					return m, nil
				}
				m.draftKey = val
				m.wroteNewKey = true
				m.errMsg = ""
				return m, m.startLoadingModels()
			}
			var cmd tea.Cmd
			m.keyInput, cmd = m.keyInput.Update(msg)
			return m, cmd
		}

		if m.step == stepLoading {
			return m, nil
		}

		if key == "enter" {
			return m.confirmSelection()
		}
	}

	if m.step == stepAPIKeyInput {
		var cmd tea.Cmd
		m.keyInput, cmd = m.keyInput.Update(msg)
		return m, cmd
	}

	if m.step == stepLoading {
		return m, nil
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) undo() (Model, tea.Cmd) {
	m.errMsg = ""
	switch m.step {
	case stepProvider:
		return m, func() tea.Msg { return CancelledMsg{} }
	case stepLocal:
		m.step = stepProvider
		m.draftProvider = ""
		m.isLocal = false
		m.list = m.newList("Select provider", stringItems(m.providers))
		return m, nil
	case stepAPIKeyChoice:
		m.step = stepProvider
		m.draftProvider = ""
		m.draftKey = ""
		m.wroteNewKey = false
		m.list = m.newList("Select provider", stringItems(m.providers))
		return m, nil
	case stepAPIKeyInput:
		m.wroteNewKey = false
		m.draftKey = ""
		m.keyInput.SetValue("")
		if m.envAPIKey != "" {
			m.step = stepAPIKeyChoice
			m.list = m.newList("API key", []list.Item{
				item{title: "Use existing .env API key", id: "env"},
				item{title: "Enter a new API key", id: "new"},
			})
			return m, nil
		}
		m.step = stepProvider
		m.draftProvider = ""
		m.list = m.newList("Select provider", stringItems(m.providers))
		return m, nil
	case stepModels, stepLoading:
		// Invalidate any in-flight ListModels so a late response can't
		// yank the UI back into the model list after the user backed out.
		m.loadID++
		if m.isLocal {
			m.step = stepLocal
			m.list = m.newList("Select local provider", stringItems(m.localProviders))
			return m, nil
		}
		if m.envAPIKey != "" {
			m.step = stepAPIKeyChoice
			m.wroteNewKey = false
			m.draftKey = ""
			m.list = m.newList("API key", []list.Item{
				item{title: "Use existing .env API key", id: "env"},
				item{title: "Enter a new API key", id: "new"},
			})
			return m, nil
		}
		m.step = stepAPIKeyInput
		m.wroteNewKey = false
		m.draftKey = ""
		m.keyInput.SetValue("")
		return m, m.keyInput.Focus()
	default:
		return m, func() tea.Msg { return CancelledMsg{} }
	}
}

func (m Model) confirmSelection() (Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, nil
	}

	switch m.step {
	case stepProvider:
		if sel.id == "local" {
			m.isLocal = true
			m.step = stepLocal
			m.list = m.newList("Select local provider", stringItems(m.localProviders))
			return m, nil
		}
		m.isLocal = false
		m.draftProvider = sel.id
		if m.envAPIKey != "" {
			m.step = stepAPIKeyChoice
			m.list = m.newList("API key", []list.Item{
				item{title: "Use existing .env API key", id: "env"},
				item{title: "Enter a new API key", id: "new"},
			})
			return m, nil
		}
		m.step = stepAPIKeyInput
		return m, m.keyInput.Focus()

	case stepLocal:
		m.draftProvider = sel.id
		m.isLocal = true
		m.draftKey = ""
		m.wroteNewKey = false
		return m, m.startLoadingModels()

	case stepAPIKeyChoice:
		if sel.id == "env" {
			m.draftKey = m.envAPIKey
			m.wroteNewKey = false
			return m, m.startLoadingModels()
		}
		m.step = stepAPIKeyInput
		m.keyInput.SetValue("")
		return m, m.keyInput.Focus()

	case stepModels:
		provider := m.draftProvider
		modelID := sel.id
		key := m.draftKey
		wrote := m.wroteNewKey
		return m, func() tea.Msg {
			return SelectedMsg{
				Provider:    provider,
				ModelID:     modelID,
				APIKey:      key,
				WroteNewKey: wrote,
			}
		}
	}
	return m, nil
}

func (m *Model) startLoadingModels() tea.Cmd {
	m.step = stepLoading
	m.errMsg = ""
	m.loadID++
	loadID := m.loadID
	provider := m.draftProvider
	key := m.draftKey
	return func() tea.Msg {
		p, err := providers.NewForListing(provider, key)
		if err != nil {
			return modelsLoadedMsg{loadID: loadID, err: err}
		}
		models, err := p.ListModels(context.Background())
		return modelsLoadedMsg{loadID: loadID, models: models, err: err}
	}
}

func (m Model) renderDialog() string {
	innerW := max(1, m.dialogWidth()-6)

	title := lipgloss.NewStyle().
		Foreground(style.Info).
		Bold(true).
		Render("Provider & model")
	help := lipgloss.NewStyle().
		Foreground(style.Muted).
		Render("enter · esc back · / filter · ctrl+c cancel")

	var body string
	switch m.step {
	case stepLoading:
		label := "Loading models"
		if m.draftProvider != "" {
			label += " from " + m.draftProvider
		}
		body = lipgloss.NewStyle().Foreground(style.Info).Render(label + "…")
	case stepAPIKeyInput:
		hint := lipgloss.NewStyle().Foreground(style.Text).Render("Enter API key for " + m.draftProvider)
		body = hint + "\n\n" + m.keyInput.View()
	default:
		body = m.list.View()
	}

	errLine := ""
	if m.errMsg != "" {
		errLine = "\n" + lipgloss.NewStyle().
			Foreground(style.Danger).
			Width(innerW).
			Render(m.errMsg)
	}

	content := title + "\n" + help + "\n\n" + body + errLine

	// Same Background as the app terminal bg — accent is Info blue.
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Info).
		Foreground(style.Text).
		Background(style.Background).
		Padding(1, 2).
		Width(m.dialogWidth()).
		Height(m.dialogHeight()).
		Render(content)
}

// View returns just the floating dialog (no placement). Prefer Overlay.
func (m Model) View() string {
	return m.renderDialog()
}

// Overlay draws the floating dialog centered on top of bg.
func (m Model) Overlay(bg string) string {
	dialog := m.renderDialog()
	dw := lipgloss.Width(dialog)
	dh := lipgloss.Height(dialog)
	w := m.termWidth
	h := m.termHeight
	if w <= 0 {
		w = lipgloss.Width(bg)
	}
	if h <= 0 {
		h = lipgloss.Height(bg)
	}
	x := max(0, (w-dw)/2)
	y := max(0, (h-dh)/2)

	base := lipgloss.NewLayer(bg).X(0).Y(0).Z(0)
	float := lipgloss.NewLayer(dialog).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(base, float).Render()
}
