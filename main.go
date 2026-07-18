package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"github.com/joho/godotenv"

	"GoWork/Tui/Components/MessageArea"
	"GoWork/Tui/Components/Promptbar"
	"GoWork/Tui/Components/Skills"
	"GoWork/Tui/Components/Stats"
	"GoWork/Tui/Components/Tabs"

	"GoWork/providers"
)

const topBarHeight = 1

const spinnerHeight = 1

//go:embed logo/logo.txt
var logoRaw string

var ansiEscapePattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleWidth measures a line's on-screen width, ignoring ANSI color
// codes (naive len() would count those escape bytes and throw off any
// centering math).
func visibleWidth(s string) int {
	return len([]rune(ansiEscapePattern.ReplaceAllString(s, "")))
}

// logoLines/logoWidth/logoHeight describe the full box-art logo, computed
// once at startup, so we know both how to center it and how much room it
// needs before deciding it no longer fits.
var logoLines = strings.Split(strings.TrimRight(logoRaw, "\n"), "\n")
var logoHeight = len(logoLines)
var logoWidth = func() int {
	max := 0
	for _, l := range logoLines {
		if w := visibleWidth(l); w > max {
			max = w
		}
	}
	return max
}()

// logoCompact is the fallback wordmark for when the terminal is too
// narrow or too short for the full box art.
const logoCompact = "\x1b[38;2;255;243;200mGoWork\x1b[0m"

// centerLine pads a (possibly ANSI-colored) line with leading spaces so
// it's centered within the given width.
func centerLine(s string, width int) string {
	pad := (width - visibleWidth(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

// welcomeLogoHeight decides, for the current window size, how many rows
// the welcome-screen logo block will occupy: the full art if there's
// room, a one-line wordmark if there's not quite enough, or nothing at
// all on a really cramped terminal. Both View() and promptTop() call
// this so the prompt bar always sits directly under whatever actually
// got drawn.
func welcomeLogoHeight(winWidth, winHeight int) int {
	switch {
	case winWidth >= logoWidth+4 && winHeight >= logoHeight+promptbar.Height+3:
		return logoHeight
	case winWidth >= 12:
		return 1 // compact single-line wordmark
	default:
		return 0 // nothing fits
	}
}

// renderLogo renders whichever logo variant welcomeLogoHeight picked,
// centered horizontally for the current window width.
func renderLogo(winWidth, winHeight int) string {
	switch welcomeLogoHeight(winWidth, winHeight) {
	case logoHeight:
		lines := make([]string, len(logoLines))
		for i, l := range logoLines {
			lines[i] = centerLine(l, winWidth)
		}
		return strings.Join(lines, "\n")
	case 1:
		return centerLine(logoCompact, winWidth)
	default:
		return ""
	}
}

// to catch errors in case the api call for the ai fails
type aiResponseMsg struct {
	content string
	err     error
}

type aiSelect struct {
	provider string
	key string
	model string
}

type model struct {
	tabs         tabs.Model
	stats        stats.Model
	skills       skills.Model
	prompt       promptbar.Model
	message_area messagearea.Model
	spinner spinner.Model

	// prompt mode
	prompt_mode bool

	// size of the window
	winWidth  int
	winHeight int

	//ai related
	model providers.Provider
	context []providers.Message
	aiThink bool
}

func initialModel(provider providers.Provider) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model{
		tabs:         tabs.New("code", "skills", "stats"),
		prompt:       promptbar.New(),
		message_area: messagearea.New(),
		prompt_mode:  false, // dont start in prompt mode
		spinner: sp,
		context: []providers.Message{},
		model: provider,
		aiThink: false,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

// tea.Cmd that actually calls the AI provider - runs off the main update loop
func generateCmd(p providers.Provider, prompt string, messages []providers.Message) tea.Cmd {
	return func() tea.Msg {
		//TODO: swap context.Background() for something cancelable (e.g. tied
		//to a "stop generating" keybind) once that's wired up.
		result, err := p.Generate(context.Background(), messages)
		if err != nil {
			return aiResponseMsg{err: err}
		}
		return aiResponseMsg{content: result.Content}
	}
}

// hasMessages reports whether we're in "chat mode" (at least one message
// has been sent) as opposed to the empty welcome screen.
func (m model) hasMessages() bool {
	return m.message_area.GetSize() > 0
}

// promptTop is the row the prompt bar starts on. In chat mode it's pinned
// to the bottom of the window; on the welcome screen (no messages yet)
// there's no message area to pin against, so it sits directly under the
// logo instead.
func (m model) promptTop() int {
	if !m.hasMessages() {
		h := welcomeLogoHeight(m.winWidth, m.winHeight)
		if h == 0 {
			return topBarHeight
		}
		return topBarHeight + h + 1 // +1 blank line under the logo
	}
	return m.winHeight - promptbar.Height
}

// applyLayout sizes every child component for the current window size and
// current mode (welcome vs. chat). It's called on resize, and again right
// when the first message is sent, since that's the other event that flips
// which mode we're in.
func (m *model) applyLayout() {
	m.tabs.SetSize(m.winWidth)
	m.prompt.SetWidth(m.winWidth)

	contentHeight := m.winHeight - topBarHeight
	if contentHeight < 0 {
		contentHeight = 0
	}
	m.stats.SetSize(m.winWidth, contentHeight)
	m.skills.SetSize(m.winWidth, contentHeight)

	if !m.hasMessages() {
		// Welcome screen: no message area reserved at all.
		m.message_area.SetSize(m.winWidth, 0)
		return
	}

	// message area fills everything between the tabs and the prompt
	// bar, minus the reserved spinner row.
	msgAreaHeight := m.winHeight - topBarHeight - spinnerHeight - promptbar.Height
	if msgAreaHeight < 0 {
		msgAreaHeight = 0
	}
	m.message_area.SetSize(m.winWidth, msgAreaHeight)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.winWidth = msg.Width
		m.winHeight = msg.Height
		m.applyLayout()
		return m, nil
	case tea.KeyPressMsg:
		msg_str := msg.String()
		
		if msg_str == "ctrl+c"{
			return m , tea.Quit
		}

		if m.prompt_mode {
			switch msg_str {
			case "esc":
				m.prompt.Blur()
				m.prompt_mode = false
				return m, nil
			case "shift+enter", "ctrl+j":
				m.prompt.InsertNewline()
				return m, nil
			case "enter":
				// Submit whatever's in the prompt bar as a user message,
				// then clear it and stay in prompt mode for the next one.
				if val := m.prompt.Value(); val != "" {
					m.aiThink = true
					m.message_area.AppendMessage(val, true)
					m.context = append(m.context, providers.Message{Role:"user",Content: val})
					m.prompt.Reset()
					m.applyLayout() // first message just switched us from welcome -> chat mode
					cmds = append(cmds, generateCmd(m.model, val , m.context))
					cmds = append(cmds, m.spinner.Tick)
				}
				return m, tea.Batch(cmds...)
			}
			var cmd tea.Cmd
			m.prompt, cmd = m.prompt.Update(msg)
			return m, cmd
		} else {
			switch msg_str {
			case "tab":
				m.tabs.Next()
				return m, nil
			case "shift+tab":
				m.tabs.Prev()
				return m, nil
			case "enter":
				m.prompt_mode = true
				return m, m.prompt.Focus()
			}
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			// Tabs occupy row 0 only (topBarHeight == 1). The prompt bar
			// is pinned to the bottom of the screen, so it owns the last
			// promptbar.Height rows instead but only on the "code" tab,
			// since that's the only screen it's rendered on. Once the
			// message area needs its own click handling (e.g. selecting
			// a message), this is where its Y range gets checked too.
			switch {
			case msg.Y < topBarHeight:
				m.tabs.HandleClick(msg.X)
			case m.tabs.Active().Name == "code" && msg.Y >= m.promptTop():
				m.prompt_mode = true
				return m, m.prompt.Focus()
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		// Scrolling either box doesn't require focus — same as scrolling
		// a window you're not "in" in nvim.
		if m.tabs.Active().Name == "code" {
			promptTop := m.promptTop()
			switch {
			case msg.Y >= promptTop:
				switch msg.Button {
				case tea.MouseWheelUp:
					m.prompt.ScrollUp()
				case tea.MouseWheelDown:
					m.prompt.ScrollDown()
				}
			case m.hasMessages() && msg.Y >= topBarHeight:
				switch msg.Button {
				case tea.MouseWheelUp:
					m.message_area.ScrollUp()
				case tea.MouseWheelDown:
					m.message_area.ScrollDown()
				}
			}
		}
		return m, nil
	case spinner.TickMsg:
		// Only keep re-ticking while we're actually waiting on a response,
		// otherwise the spinner would spin forever in the background.
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.aiThink {
			return m, cmd
		}
		return m, nil
	case aiResponseMsg:
		m.aiThink = false
		if msg.err != nil {
			var perr *providers.ProviderError
			if errors.As(msg.err, &perr) {
				switch perr.Kind {
				case providers.ErrRateLimited:
					m.message_area.AppendMessage("Rate limited - try again in a moment.", false)
				case providers.ErrAuthFailed:
					m.message_area.AppendMessage("Auth failed - check your API key.", false)
				case providers.ErrContextExceeded:
					m.message_area.AppendMessage("Context window exceeded - try trimming history.", false)
				default:
					m.message_area.AppendMessage("Error: "+perr.Error(), false)
				}
			} else {
				m.message_area.AppendMessage("Error: "+msg.err.Error(), false)
			}
		} else {
			m.message_area.AppendMessage(msg.content, false)
			m.context = append(m.context, providers.Message{Role: "assistant", Content: msg.content})
		}
		return m, nil
	}
	var tabsCmd tea.Cmd
	m.tabs, tabsCmd = m.tabs.Update(msg)
	// The prompt bar's cursor blink runs on its own message loop
	// (cursor.BlinkMsg) that doesn't match any case above, so it has to
	// be forwarded here too or the cursor blinks once and then freezes.
	// The message area's viewport has the same needs (e.g. its own
	// internal key/mouse handling), so it gets forwarded the same way.
	var promptCmd tea.Cmd
	m.prompt, promptCmd = m.prompt.Update(msg)
	var msgAreaCmd tea.Cmd
	m.message_area, msgAreaCmd = m.message_area.Update(msg)
	return m, tea.Batch(tabsCmd, promptCmd, msgAreaCmd)
}

func (m model) View() tea.View {
	top := m.tabs.View()
	var content string
	switch m.tabs.Active().Name {
	case "stats":
		content = top + "\n" + m.stats.View()
	case "skills":
		content = top + "\n" + m.skills.View()
	default:
		content += top + "\n"
		if !m.hasMessages() {
			// Welcome screen: logo (sized to fit the window), then the
			// prompt right underneath it. No message area, no spinner
			// row — nothing to reserve space for until there's an
			// actual conversation.
			if logo := renderLogo(m.winWidth, m.winHeight); logo != "" {
				content += logo + "\n\n"
			}
			content += m.prompt.View()
		} else {
			content += m.message_area.View() + "\n"
			if m.aiThink {
				content += m.spinner.View() + " thinking...."
			}
			content += "\n"
			content += m.prompt.View()
		}
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // required in v2 for any mouse msg to arrive at all
	return v
}

func main() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Couldn't locate .env file:", err)
		os.Exit(1)
	}

	apiKey := os.Getenv("API_KEY")

	if apiKey == "" {
		fmt.Println("API_KEY is empty")
		os.Exit(1)
	}

	provider, err := providers.Select_provider("gemini-3.5-flash", apiKey)

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(provider))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Oof: %v\n", err)
		os.Exit(1)
	}
}
