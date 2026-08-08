package db

import (
	"fmt"
	"time"
)

// UsageRecord is one model generation (a streamed answer or a compaction
// call). Kept in the store's in-memory buffer during the run and inserted on
// exit.
type UsageRecord struct {
	Model            string
	Provider         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ToolCalls        int
	CreatedAt        time.Time
}

// ModelStat is the per-model aggregate shown by the stats screen. Only usage
// data lives here - no session-level rows are surfaced to the UI.
type ModelStat struct {
	Model             string
	Provider          string
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	Calls             int
	ToolCalls         int
	FirstUsed         time.Time
	LastUsed          time.Time
}

// DailyBucket is one calendar day's per-model token totals and tool-call
// counts, used to build the stacked daily usage chart.
type DailyBucket struct {
	Day     string // "2006-01-02", in local time
	Tokens  map[string]int
	Tools   map[string]int
	Total   int
	ToolCalls int
}

// DailyStats returns one bucket per calendar day over the last 30 days,
// oldest first, in local time. Days with no usage at all are omitted from
// the map entirely (callers can tell an empty day apart from a
// zero-token day this way) - the caller decides how to render gaps.
// Each bucket also carries the day's tool-call count, per model, so the
// chart can mark days that used tools. Like Stats, the live session's
// buffered records are folded in so the current day's bar reflects usage
// before the final flush.
func (s *Store) DailyStats() ([]DailyBucket, error) {
	cutoff := time.Now().Add(-retentionWindow).Format(time.RFC3339)
	rows, err := s.hnd.Query(`
		SELECT created_at, model, SUM(total_tokens), SUM(tool_calls)
		FROM usage
		WHERE created_at >= ?
		GROUP BY model, substr(created_at, 1, 10)
		ORDER BY created_at ASC`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("db: daily stats query: %w", err)
	}
	defer rows.Close()

	by := map[string]*DailyBucket{} // keyed by "2006-01-02" in local time
	for rows.Next() {
		var createdAt, model string
		var tokens, tools int
		if err := rows.Scan(&createdAt, &model, &tokens, &tools); err != nil {
			return nil, fmt.Errorf("db: daily stats scan: %w", err)
		}
		if model == "" || tokens <= 0 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			continue
		}
		day := ts.Local().Format("2006-01-02")
		b, ok := by[day]
		if !ok {
			b = &DailyBucket{Day: day, Tokens: map[string]int{}, Tools: map[string]int{}}
			by[day] = b
		}
		b.Tokens[model] += tokens
		b.Total += tokens
		b.Tools[model] += tools
		b.ToolCalls += tools
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: daily stats iteration: %w", err)
	}

	now := time.Now()
	for _, r := range s.pendingUsage {
		if r.Model == "" || r.TotalTokens <= 0 || now.Sub(r.CreatedAt) > retentionWindow {
			continue
		}
		day := r.CreatedAt.Local().Format("2006-01-02")
		b, ok := by[day]
		if !ok {
			b = &DailyBucket{Day: day, Tokens: map[string]int{}, Tools: map[string]int{}}
			by[day] = b
		}
		b.Tokens[r.Model] += r.TotalTokens
		b.Total += r.TotalTokens
		b.Tools[r.Model] += r.ToolCalls
		b.ToolCalls += r.ToolCalls
	}

	out := make([]DailyBucket, 0, len(by))
	for _, b := range by {
		out = append(out, *b)
	}
	sortDailyBuckets(out)
	return out, nil
}

func sortDailyBuckets(buckets []DailyBucket) {
	for i := 1; i < len(buckets); i++ {
		for j := i; j > 0 && buckets[j].Day < buckets[j-1].Day; j-- {
			buckets[j], buckets[j-1] = buckets[j-1], buckets[j]
		}
	}
}

// retentionWindow is the sliding retention window for usage rows.
const retentionWindow = 30 * 24 * time.Hour

// RecordUsage buffers a finished generation for the current session. Calls
// with zero total usage are ignored (a cancelled/empty turn records nothing).
func (s *Store) RecordUsage(model, provider string, prompt, completion, total, toolCalls int, when time.Time) {
	if total <= 0 {
		return
	}
	if s.currentModl == "" {
		s.currentModl = model
		s.currentProv = provider
	}
	s.pendingUsage = append(s.pendingUsage, UsageRecord{
		Model:            model,
		Provider:         provider,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		ToolCalls:        toolCalls,
		CreatedAt:        when,
	})
}

// Stats aggregates usage over the last 30 days, per model, most-used first.
// The live session's buffered records are folded into the query results so
// the graph/list stay current before anything is flushed to disk.
func (s *Store) Stats() ([]ModelStat, error) {
	cutoff := time.Now().Add(-retentionWindow).Format(time.RFC3339)
	rows, err := s.hnd.Query(`
		SELECT model,
		       COALESCE(NULLIF(MAX(provider), ''), ''),
		       SUM(prompt_tokens),
		       SUM(completion_tokens),
		       SUM(total_tokens),
		       COUNT(*),
		       SUM(tool_calls),
		       MIN(created_at),
		       MAX(created_at)
		FROM usage
		WHERE created_at >= ?
		GROUP BY model
		ORDER BY SUM(total_tokens) DESC`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("db: stats query: %w", err)
	}
	defer rows.Close()

	by := map[string]*ModelStat{}
	for rows.Next() {
		var (
			model, provider, firstS, lastS string
			prompt, completion, total      int
			calls, tools                   int
		)
		if err := rows.Scan(&model, &provider, &prompt, &completion, &total, &calls, &tools, &firstS, &lastS); err != nil {
			return nil, fmt.Errorf("db: stats scan: %w", err)
		}
		if model == "" {
			continue
		}
		agg, ok := by[model]
		if !ok {
			agg = &ModelStat{Model: model}
			by[model] = agg
		}
		if provider != "" {
			agg.Provider = provider
		}
		agg.PromptTokens += prompt
		agg.CompletionTokens += completion
		agg.TotalTokens += total
		agg.Calls += calls
		agg.ToolCalls += tools
		first, _ := time.Parse(time.RFC3339, firstS)
		last, _ := time.Parse(time.RFC3339, lastS)
		if agg.FirstUsed.IsZero() || first.Before(agg.FirstUsed) {
			agg.FirstUsed = first
		}
		if last.After(agg.LastUsed) {
			agg.LastUsed = last
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: stats iteration: %w", err)
	}

	// fold the live (not yet flushed) records in so the current session's
	// usage shows up before the final flush.
	now := time.Now()
	for _, r := range s.pendingUsage {
		if r.Model == "" || now.Sub(r.CreatedAt) > retentionWindow {
			continue
		}
		agg, ok := by[r.Model]
		if !ok {
			agg = &ModelStat{Model: r.Model}
			by[r.Model] = agg
		}
		if r.Provider != "" {
			agg.Provider = r.Provider
		}
		agg.PromptTokens += r.PromptTokens
		agg.CompletionTokens += r.CompletionTokens
		agg.TotalTokens += r.TotalTokens
		agg.Calls++
		agg.ToolCalls += r.ToolCalls
		if agg.FirstUsed.IsZero() || r.CreatedAt.Before(agg.FirstUsed) {
			agg.FirstUsed = r.CreatedAt
		}
		if r.CreatedAt.After(agg.LastUsed) {
			agg.LastUsed = r.CreatedAt
		}
	}

	out := make([]ModelStat, 0, len(by))
	for _, agg := range by {
		out = append(out, *agg)
	}
	sortModelStats(out)
	return out, nil
}

func sortModelStats(stats []ModelStat) {
	for i := 1; i < len(stats); i++ {
		for j := i; j > 0; j-- {
			if stats[j].TotalTokens > stats[j-1].TotalTokens {
				stats[j], stats[j-1] = stats[j-1], stats[j]
				continue
			}
			if stats[j].TotalTokens == stats[j-1].TotalTokens && stats[j].Model < stats[j-1].Model {
				stats[j], stats[j-1] = stats[j-1], stats[j]
			}
		}
	}
}
