package questionnairetool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	questionnaire "GoWork/Tui/Components/Questionnaire"
	"GoWork/tools"
)

func TestTool_Identity(t *testing.T) {
	tt := New()
	if tt.Name() != ToolName {
		t.Fatalf("Name() = %q, want %q", tt.Name(), ToolName)
	}
	if tt.Kind() != tools.KindAllowed {
		t.Fatalf("Kind() = %v, want KindAllowed", tt.Kind())
	}
	if strings.TrimSpace(tt.Description()) == "" {
		t.Fatal("Description() is empty")
	}
}

func TestRun_ValidInput_SetsActive(t *testing.T) {
	questionnaire.Clear()
	t.Cleanup(questionnaire.Clear)

	raw := json.RawMessage(`{"questions":[{"question":"pick a db","options":["postgres","mysql"]},{"question":"any notes?"}]}`)
	res, err := New().Run(context.Background(), tools.DispatchArgs{}, raw)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected ok result, got error: %s", res.Content)
	}

	active := questionnaire.Active()
	if len(active) != 2 {
		t.Fatalf("expected 2 active questions, got %d", len(active))
	}
	if active[0].Question != "pick a db" || len(active[0].Answers) != 2 || active[0].Answers[1] != "mysql" {
		t.Fatalf("unexpected question data: %+v", active[0])
	}
	if active[1].Question != "any notes?" || len(active[1].Answers) != 0 {
		t.Fatalf("unexpected free-text question: %+v", active[1])
	}
}

func TestRun_RejectsSecondActiveQuestionnaire(t *testing.T) {
	questionnaire.Clear()
	t.Cleanup(questionnaire.Clear)

	raw := json.RawMessage(`{"questions":[{"question":"a?"}]}`)
	tt := New()
	tt.Run(context.Background(), tools.DispatchArgs{}, raw)

	res, _ := tt.Run(context.Background(), tools.DispatchArgs{}, raw)
	if !res.IsError {
		t.Fatalf("expected error for a second questionnaire, got: %s", res.Content)
	}
}

func TestRun_TooManyQuestions_Errors(t *testing.T) {
	questionnaire.Clear()
	t.Cleanup(questionnaire.Clear)

	var qs []map[string]any
	for i := 0; i < maxQuestions+1; i++ {
		qs = append(qs, map[string]any{"question": "q"})
	}
	raw, _ := json.Marshal(map[string]any{"questions": qs})

	res, err := New().Run(context.Background(), tools.DispatchArgs{}, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for too many questions")
	}
	if questionnaire.Active() != nil {
		t.Fatal("failed questionnaire must not become active")
	}
}

func TestRun_TooManyOptions_Errors(t *testing.T) {
	questionnaire.Clear()
	t.Cleanup(questionnaire.Clear)

	raw := json.RawMessage(`{"questions":[{"question":"q","options":["a","b","c","d"]}]}`)
	res, _ := New().Run(context.Background(), tools.DispatchArgs{}, raw)
	if !res.IsError {
		t.Fatal("expected an error result for 4 options")
	}
	if questionnaire.Active() != nil {
		t.Fatal("failed questionnaire must not become active")
	}
}

func TestRun_MissingQuestions_Errors(t *testing.T) {
	questionnaire.Clear()
	t.Cleanup(questionnaire.Clear)

	res, err := New().Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for empty input")
	}
}

func TestRun_BlankQuestion_Errors(t *testing.T) {
	questionnaire.Clear()
	t.Cleanup(questionnaire.Clear)

	raw := json.RawMessage(`{"questions":[{"question":"   "}]}`)
	res, _ := New().Run(context.Background(), tools.DispatchArgs{}, raw)
	if !res.IsError {
		t.Fatal("expected an error result for a blank question")
	}
}

func TestRun_InvalidJSON_ReturnsError(t *testing.T) {
	questionnaire.Clear()
	t.Cleanup(questionnaire.Clear)

	_, err := New().Run(context.Background(), tools.DispatchArgs{}, json.RawMessage(`{nope`))
	if err == nil {
		t.Fatal("expected a Go error for invalid JSON")
	}
}

func TestInputSchema_EncodesLimits(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(New().InputSchema(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props := schema["properties"].(map[string]any)
	qs := props["questions"].(map[string]any)
	if qs["maxItems"].(float64) != maxQuestions {
		t.Fatalf("questions maxItems = %v, want %d", qs["maxItems"], maxQuestions)
	}
	if qs["minItems"].(float64) != 1 {
		t.Fatalf("questions minItems = %v, want 1", qs["minItems"])
	}
	item := qs["items"].(map[string]any)
	opts := item["properties"].(map[string]any)["options"].(map[string]any)
	if opts["maxItems"].(float64) != maxOptions {
		t.Fatalf("options maxItems = %v, want %d", opts["maxItems"], maxOptions)
	}
}

func TestFormatAnswers(t *testing.T) {
	qs := []questionnaire.Question{
		{Question: "pick a db", Answers: []string{"postgres", "mysql"}},
		{Question: "any notes?"},
	}
	got := FormatAnswers(qs, []string{"mysql", "keep it simple"})
	want := "Q1: pick a db\nAnswer: mysql\nQ2: any notes?\nAnswer: keep it simple"
	if got != want {
		t.Fatalf("FormatAnswers:\n got %q\nwant %q", got, want)
	}
}

func TestFormatCancelled(t *testing.T) {
	if got := FormatCancelled(); got != "[cancelled by user]" {
		t.Fatalf("FormatCancelled() = %q", got)
	}
}
