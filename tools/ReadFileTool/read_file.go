package readfiletool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"GoWork/tools"
)

type Input struct {
	Path    string `json:"path"`
	//depends dont know yet
}

type Tool struct {}

//NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface 
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Description() string {

}

func (t *Tool) InputSchema() tools.Schema {
	return tools.Schema{
	
	}
}

func (t *Tool) Kind() tools.Kind { return tools.KindRead }

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {

}
