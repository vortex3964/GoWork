package gowork

import (
	"fmt"
	"strconv"
	"strings"
)

// AppVersion is the version reported by --version. The root command copies
// its own appVersion here at startup so -X main.appVersion keeps working.
var AppVersion = "dev"

const usageText = `GoWork - AI agent CLI

Usage:
  GoWork [options] [project path]

Arguments:
  project path          Project directory to open (defaults to the current directory)

Options:
  -S, --session <id>    Resume a previous session by id
  -h, --help            Show this help and exit
  -v, --version         Show the version and exit
`

type cliOptions struct {
	Root        string
	SessionID   int64
	ShowHelp    bool
	ShowVersion bool
}

// parseArgs parses the arguments after the program name. The first positional
// argument is the project root; unknown flags and extra positionals are
// errors rather than being silently ignored.
func parseArgs(args []string) (cliOptions, error) {
	var opts cliOptions
	positionalOnly := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if positionalOnly {
			if err := opts.setRoot(arg); err != nil {
				return opts, err
			}
			continue
		}
		switch {
		case arg == "--":
			positionalOnly = true
		case arg == "-h" || arg == "--help":
			opts.ShowHelp = true
		case arg == "-v" || arg == "--version":
			opts.ShowVersion = true
		case arg == "-S" || arg == "--session":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("option %s requires a session id", arg)
			}
			i++
			id, err := parseSessionID(args[i])
			if err != nil {
				return opts, err
			}
			opts.SessionID = id
		case strings.HasPrefix(arg, "--session="):
			id, err := parseSessionID(strings.TrimPrefix(arg, "--session="))
			if err != nil {
				return opts, err
			}
			opts.SessionID = id
		case strings.HasPrefix(arg, "-") && arg != "-":
			return opts, fmt.Errorf("unknown flag: %s", arg)
		default:
			if err := opts.setRoot(arg); err != nil {
				return opts, err
			}
		}
	}
	return opts, nil
}

func (o *cliOptions) setRoot(path string) error {
	if o.Root != "" {
		return fmt.Errorf("unexpected argument: %s", path)
	}
	o.Root = path
	return nil
}

func parseSessionID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid session id %q (expected a positive integer)", raw)
	}
	return id, nil
}
