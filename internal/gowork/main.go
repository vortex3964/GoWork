package gowork

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/joho/godotenv"

	"GoWork/providers"
)

// Main runs the application.
func Main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Fprintln(os.Stderr, "Run 'GoWork --help' for usage.")
		os.Exit(2)
	}
	if opts.ShowHelp {
		fmt.Print(usageText)
		return
	}
	if opts.ShowVersion {
		fmt.Printf("GoWork %s\n", AppVersion)
		return
	}

	// Resolve the project root before loading .env so the file comes from the
	// selected project rather than whatever directory launched the binary.
	root := opts.Root
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			fmt.Println("Warning: couldn't determine project root:", err)
			root = ""
		}
	} else if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	envPath := defaultEnvPath
	if opts.Root != "" {
		envPath = filepath.Join(root, defaultEnvPath)
	}
	if err := godotenv.Load(envPath); err != nil {
		// A missing .env isn't fatal: every provider can still be picked
		// interactively with ctrl+p (and local providers need no key at
		// all). Just warn and carry on with whatever the real environment
		// exports.
		fmt.Println("Warning: couldn't locate .env file:", err)
	}

	providerName := envOr("PROVIDER")
	if providerName == "" {
		providerName = "None"
	}

	modelName := envOr("model", "MODEL")
	if modelName == "" {
		modelName = "None"
	}

	apiKey := os.Getenv("API_KEY")

	var provider providers.Provider
	if providerName != "None" {
		p, err := providers.Select_provider(providerName, modelName, apiKey)
		if err != nil {
			// Don't kill the app - just start without a provider and
			// let the user pick one with ctrl+p.
			fmt.Println("Warning: couldn't initialize provider:", err)
			providerName = "None"
			modelName = "None"
		} else {
			provider = p
		}
	}

	// A session id restores a previous session: its message history is
	// replayed into the conversation and its loaded skills come back too.
	sessionID := opts.SessionID

	sys, err := inject_vars_in_sys_prompt(root)
	if err != nil {
		fmt.Println("Warning: couldn't load system prompt:", err)
	} else {
		providers.InitSystemPrompt(sys)
	}

	comp, err := readAsset(compaction_prompt_path)
	if err != nil {
		fmt.Println("Warning: couldn't load compaction prompt:", err)
	} else {
		providers.InitCompactionPrompt(string(comp))
	}

	m := initialModel(root, provider, modelName, providerName)
	// Local servers started from the picker are children of this process:
	// never leave one orphaned if the TUI exits (including error paths).
	if m.localServers != nil {
		defer m.localServers.StopAll()
	}

	if sessionID > 0 {
		m.resumeSession(sessionID)
	}

	// Save-on-exit: intercept the quit before the terminal tears down so the
	// current session's usage + messages land in the database.
	p := tea.NewProgram(m, tea.WithFilter(func(mdl tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.QuitMsg); ok {
			if app, ok := mdl.(model); ok {
				app.flushSession()
				if app.localServers != nil {
					app.localServers.StopAll()
				}
			}
		}
		return msg
	}))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Oof: %v\n", err)
		os.Exit(1)
	}
}
