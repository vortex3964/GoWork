package changeslist

import (
	"fmt"
	"io"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
	"GoWork/tools"
)

//keys that we will use for this component
const (
	KeyAccept = "ctrl+c"
	KeyReject = "ctrl+r"
	KeyFocus  = "tab"
)

//name of the file we will test relative path from the project route the entire app will work with relative paths to the root the user oppened the app in 
const test_file_path = "GoWork/tests/test_with_markers.go"

//same as message area
const padleft = 3

type Model struct {
	Changeslist list.Model
	Watch_list WatchList
	//will also have a file explorer model but skip for now
}

type item string

func (i item) FilterValue() string { return "" }

func New() Model {
	l := list.New([])
}
