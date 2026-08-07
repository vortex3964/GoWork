// DESC: tool for the ai to create questionnaires for the user
// to resolve questions it has design decisions and scope

package questionnairetool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	questionnaire "GoWork/Tui/Components/Questionnaire"
	"GoWork/tools"
)

const ToolName = "questions_tool"

const maxQuestions = 7

const maxOptions = 3

type QuestionInput struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

type Input struct {
	Questions []QuestionInput `json:"questions"`
}

type Tool struct{}

// NOTE: since were converting *tool to tools.AgentTool were forcing this to follow the interface
func New() tools.AgentTool { return &Tool{} }

func (t *Tool) Name() string { return ToolName }

func (t *Tool) Description() string {
	return `Asks the user a questionnaire when you need answers only they can give: design decisions, scope, preferences, anything your tools and the code can't tell you. The turn pauses until the user answers, then the answers come back as this tool's result.

Rules:
- At most 7 questions per questionnaire; split larger question sets into multiple calls.
- Up to 3 answer options per question. The user always gets a "type your own answer" row, so never add an "other" option yourself.
- A question with no options is a free text answer.
- Ask the fewest questions you need. Read code and docs first, and don't use this to ask permission for tool actions.`
}

func (t *Tool) Kind() tools.Kind { return tools.KindAllowed }

func (t *Tool) InputSchema() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    maxQuestions,
				"description": "The questions to ask the user, max " + strconv.Itoa(maxQuestions) + ".",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"question": map[string]any{
							"type":        "string",
							"description": "The question you need the user to answer.",
						},
						"options": map[string]any{
							"type":        "array",
							"maxItems":    maxOptions,
							"description": "Answer choices, max " + strconv.Itoa(maxOptions) + ".",
							"items":       map[string]any{"type": "string"},
						},
					},
					"required":             []string{"question"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"questions"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

func (t *Tool) Run(ctx context.Context, args tools.DispatchArgs, rawInput json.RawMessage) (tools.ToolResult, error) {
	var input Input
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return tools.ToolResult{}, fmt.Errorf("questions_tool: invalid input: %w", err)
	}
	if len(input.Questions) == 0 {
		return tools.Errf("questions_tool: at least one question is required"), nil
	}
	if len(input.Questions) > maxQuestions {
		return tools.Errf("questions_tool: at most %d questions per questionnaire (got %d); split your questions into multiple batches", maxQuestions, len(input.Questions)), nil
	}
	if questionnaire.Active() != nil {
		return tools.Errf("questions_tool: another questionnaire is already waiting for answers"), nil
	}

	qs := make([]questionnaire.Question, 0, len(input.Questions))
	for i, qi := range input.Questions {
		if strings.TrimSpace(qi.Question) == "" {
			return tools.Errf("questions_tool: question %d has no text", i+1), nil
		}
		if len(qi.Options) > maxOptions {
			return tools.Errf("questions_tool: question %d has more than %d options", i+1, maxOptions), nil
		}
		opts := make([]string, 0, len(qi.Options))
		for _, o := range qi.Options {
			if strings.TrimSpace(o) != "" {
				opts = append(opts, o)
			}
		}
		qs = append(qs, questionnaire.Question{Question: strings.TrimSpace(qi.Question), Answers: opts})
	}

	questionnaire.SetActive(qs)
	return tools.Ok(fmt.Sprintf("asked the user %d question(s) - the turn resumes when they answer", len(qs))), nil
}

// FormatAnswers renders the user's answers as the tool result the model reads
func FormatAnswers(qs []questionnaire.Question, answers []string) string {
	var b strings.Builder
	for i, q := range qs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Q%d: %s\n", i+1, q.Question)
		ans := "skipped"
		if i < len(answers) && strings.TrimSpace(answers[i]) != "" {
			ans = answers[i]
		}
		b.WriteString("Answer: " + ans)
	}
	return b.String()
}

// FormatCancelled is the tool result when the user dismisses the questionnaire
func FormatCancelled() string {
	return "[cancelled by user]"
}
