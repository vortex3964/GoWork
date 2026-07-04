package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"

	"github.com/joho/godotenv"

	"GoWork/providers"
	"GoWork/tools"
)

// to catch errors in case the api call for the ai fails
type aiResponseMsg struct {
    content string
    err     error
}

type model struct {
	//the conversation between ai and user (will probably change cause of the future features require it to be more functional)
	messages []string

	//the ais context and the ai object itself
	model providers.Provider
	context []providers.Message

	prompt_area textarea.Model
	discusion_area viewport.Model
	
	//spinner for when the ai is thinking or doing work in general
	spinner spinner.Model
	
	//maybe in the future we will use an enum but for now we will use this to block multiple prompts
	blocking bool

	err error
}

func initModel(provider providers.Provider) model{
	ta := textarea.New()
	ta.Placeholder = "Enter prompt..."
	ta.Focus()
	//NOTE:no limit for the users prompt
	//contemplate if this is ok
	ta.CharLimit = 0
	
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SetContent("")

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return model {
		messages: []string{},
		model: provider,
		context: []providers.Message{},
		prompt_area: ta,
		discusion_area: vp,
		spinner: sp,
		blocking: false,
		err: nil,
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

// tea.Cmd that actually calls the AI provider — runs off the main update loop
func generateCmd(p providers.Provider, prompt string, context []providers.Message) tea.Cmd {
	return func() tea.Msg {
		resp, err := p.Generate(prompt, context)
		if err != nil {
			return aiResponseMsg{content: resp , err: err}
		}
		return aiResponseMsg{content: resp}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

		case tea.WindowSizeMsg:
			m.discusion_area.SetWidth(msg.Width)
			m.discusion_area.SetHeight(msg.Height - m.prompt_area.Height() - 2)
			m.prompt_area.SetWidth(msg.Width)

		case tea.KeyPressMsg:
			switch msg.String() {
				case "ctrl+c", "esc":
					//providers.ExportContext(m.context)
					return m, tea.Quit
				
				//enter enters and new line keys change lines
				case "enter": 
					if !m.blocking {
						prompt := m.prompt_area.Value()
						if prompt != "" {
							m.blocking = true
							m.messages = append(m.messages, "User: "+prompt)
							m.context = append(m.context, providers.Message{Role: "user", Content: prompt})
							m.discusion_area.SetContent(joinMessages(m.messages))
							m.discusion_area.GotoBottom()
							m.prompt_area.Reset()

							cmds = append(cmds, generateCmd(m.model, prompt, m.context))
							cmds = append(cmds, m.spinner.Tick)
						}
					}
					return m, tea.Batch(cmds...)
			}

		case aiResponseMsg:
			m.blocking = false
			if msg.err != nil {
				m.err = msg.err
			} else {
				m.messages = append(m.messages, "AI: "+msg.content)
				m.context = append(m.context, providers.Message{Role: "assistant", Content: msg.content})
				m.discusion_area.SetContent(joinMessages(m.messages))
				m.discusion_area.GotoBottom()
			}

		case spinner.TickMsg:
			if m.blocking {
				var cmd tea.Cmd
				m.spinner, cmd = m.spinner.Update(msg)
				cmds = append(cmds, cmd)
			}

	}

	// forward to child components
	var cmd tea.Cmd
	m.prompt_area, cmd = m.prompt_area.Update(msg)
	cmds = append(cmds, cmd)

	m.discusion_area, cmd = m.discusion_area.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}


func (m model) View() tea.View {
	view := m.discusion_area.View()

	if m.blocking {
		view += "\n" + m.spinner.View() + " thinking..."
	}

	view += "\n" + m.prompt_area.View()

	if m.err != nil {
		view += "\n" + fmt.Sprintf("error: %v", m.err)
	}

	return tea.NewView(view)
}

func joinMessages(messages []string) string {
	out := ""
	for _, msg := range messages {
		out += msg + "\n\n"
	}
	return out
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

	cwd , err := os.Getwd()
	if err != nil {
		fmt.Println("couldnt get working directory:",err)
		os.Exit(1)
	}
	
	//IMPORTANT: we set the variable in tools to our project root to guard againt ai accessing dirs it shouldnt
	tools.ProjectRoot = cwd

	provider, err := providers.Select_provider("gemini-3.5-flash", apiKey)

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	p := tea.NewProgram(initModel(provider))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Oof: %v\n", err)
		os.Exit(1)
	}
}
