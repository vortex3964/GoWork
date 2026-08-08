-- GoWork test data.
-- Run this against Db/gowork.db after the app has created the schema once
-- (the app auto-creates tables on open):
--
--   sqlite3 Db/gowork.db < Db/insert.sql
--
-- Timestamps are generated relative to 'now' at seed time, so the seed
-- always covers the full 30-day sliding window: one usage row on each of
-- the last 30 days (a few days carry a second model so the stacked bars
-- show per-model colors), tool_call counts sprinkled through the week,
-- and session/message rows restored on `GoWork -S <id>`.

-- -- sessions (one app run = one session; only the last 5 are kept) --
INSERT INTO sessions (id, started_at, ended_at, model, provider, total_tokens, message_count) VALUES
  (1, strftime('%Y-%m-%dT12:05:00Z','now','-29 days'), strftime('%Y-%m-%dT16:40:00Z','now','-24 days'), 'qwen2.5-coder:14b', 'ollama', 182318, 12),
  (2, strftime('%Y-%m-%dT09:30:00Z','now','-23 days'), strftime('%Y-%m-%dT18:45:00Z','now','-15 days'), 'gemini-2.5-flash', 'google', 228799, 14),
  (3, strftime('%Y-%m-%dT10:15:00Z','now','-14 days'), strftime('%Y-%m-%dT15:20:00Z','now','-6 days'), 'gemini-2.5-pro', 'google', 270288, 11),
  (4, strftime('%Y-%m-%dT11:00:00Z','now','-5 days'), strftime('%Y-%m-%dT17:30:00Z','now','-1 days'), 'claude-3-7-sonnet', 'anthropic', 118894, 9),
  (5, strftime('%Y-%m-%dT08:45:00Z','now','-0 days'), strftime('%Y-%m-%dT13:10:00Z','now','-0 days'), 'gpt-4o', 'openai', 34053, 6);

-- -- usage (one row per day over the last 30 days) --
INSERT INTO usage (id, session_id, model, provider, prompt_tokens, completion_tokens, total_tokens, tool_calls, created_at) VALUES
  (1, 5, 'gpt-4o', 'openai', 8425, 6587, 15012, 1, strftime('%Y-%m-%dT10:00:00Z','now','-0 days')),
  (2, 5, 'gpt-4o', 'openai', 6262, 3096, 9358, 5, strftime('%Y-%m-%dT10:00:00Z','now','-0 days')),
  (3, 4, 'gpt-4o', 'openai', 15348, 10001, 25349, 0, strftime('%Y-%m-%dT13:11:00Z','now','-1 days')),
  (4, 4, 'claude-3-7-sonnet', 'anthropic', 8532, 5091, 13623, 0, strftime('%Y-%m-%dT12:22:00Z','now','-2 days')),
  (5, 4, 'claude-3-7-sonnet', 'anthropic', 8096, 5127, 13223, 1, strftime('%Y-%m-%dT11:33:00Z','now','-3 days')),
  (6, 4, 'qwen2.5-coder:14b', 'ollama', 4284, 1928, 6212, 1, strftime('%Y-%m-%dT11:33:00Z','now','-3 days')),
  (7, 4, 'qwen2.5-coder:14b', 'ollama', 9765, 7384, 17149, 1, strftime('%Y-%m-%dT10:44:00Z','now','-4 days')),
  (8, 4, 'gpt-4o-mini', 'openai', 4891, 4458, 9349, 0, strftime('%Y-%m-%dT13:05:00Z','now','-5 days')),
  (9, 3, 'gpt-4o-mini', 'openai', 14595, 11187, 25782, 0, strftime('%Y-%m-%dT12:16:00Z','now','-6 days')),
  (10, 3, 'gemini-2.5-flash', 'google', 12521, 11050, 23571, 5, strftime('%Y-%m-%dT11:27:00Z','now','-7 days')),
  (11, 3, 'gpt-4o', 'openai', 13810, 10279, 24089, 3, strftime('%Y-%m-%dT10:38:00Z','now','-8 days')),
  (12, 3, 'gpt-4o-mini', 'openai', 4234, 4045, 8279, 1, strftime('%Y-%m-%dT13:49:00Z','now','-9 days')),
  (13, 3, 'claude-3-7-sonnet', 'anthropic', 11554, 3928, 15482, 5, strftime('%Y-%m-%dT13:49:00Z','now','-9 days')),
  (14, 3, 'gpt-4o', 'openai', 10510, 7945, 18455, 3, strftime('%Y-%m-%dT12:10:00Z','now','-10 days')),
  (15, 3, 'gpt-4o-mini', 'openai', 6712, 4617, 11329, 1, strftime('%Y-%m-%dT11:21:00Z','now','-11 days')),
  (16, 3, 'gemini-2.5-pro', 'google', 14103, 13131, 27234, 3, strftime('%Y-%m-%dT10:32:00Z','now','-12 days')),
  (17, 3, 'claude-3-7-sonnet', 'anthropic', 6989, 4365, 11354, 1, strftime('%Y-%m-%dT13:43:00Z','now','-13 days')),
  (18, 3, 'claude-3-7-sonnet', 'anthropic', 4364, 3468, 7832, 0, strftime('%Y-%m-%dT12:04:00Z','now','-14 days')),
  (19, 3, 'gemini-2.5-pro', 'google', 9806, 6530, 16336, 0, strftime('%Y-%m-%dT12:04:00Z','now','-14 days')),
  (20, 2, 'gemini-2.5-pro', 'google', 8608, 4359, 12967, 2, strftime('%Y-%m-%dT11:15:00Z','now','-15 days')),
  (21, 2, 'gemini-2.5-flash', 'google', 6047, 4634, 10681, 1, strftime('%Y-%m-%dT10:26:00Z','now','-16 days')),
  (22, 2, 'gemini-2.5-pro', 'google', 15271, 9884, 25155, 3, strftime('%Y-%m-%dT13:37:00Z','now','-17 days')),
  (23, 2, 'gpt-4o-mini', 'openai', 9880, 3306, 13186, 1, strftime('%Y-%m-%dT12:48:00Z','now','-18 days')),
  (24, 2, 'llama3.1:8b', 'ollama', 6185, 2793, 8978, 5, strftime('%Y-%m-%dT11:09:00Z','now','-19 days')),
  (25, 2, 'claude-3-7-sonnet', 'anthropic', 14342, 12218, 26560, 3, strftime('%Y-%m-%dT10:20:00Z','now','-20 days')),
  (26, 2, 'gpt-4o', 'openai', 12008, 6496, 18504, 2, strftime('%Y-%m-%dT13:31:00Z','now','-21 days')),
  (27, 2, 'gpt-4o-mini', 'openai', 10575, 3663, 14238, 5, strftime('%Y-%m-%dT13:31:00Z','now','-21 days')),
  (28, 2, 'gpt-4o', 'openai', 6538, 3215, 9753, 2, strftime('%Y-%m-%dT12:42:00Z','now','-22 days')),
  (29, 2, 'gemini-2.5-pro', 'google', 15797, 11206, 27003, 1, strftime('%Y-%m-%dT11:03:00Z','now','-23 days')),
  (30, 1, 'claude-3-7-sonnet', 'anthropic', 10450, 10417, 20867, 4, strftime('%Y-%m-%dT10:14:00Z','now','-24 days')),
  (31, 1, 'gemini-2.5-pro', 'google', 15469, 6934, 22403, 2, strftime('%Y-%m-%dT13:25:00Z','now','-25 days')),
  (32, 1, 'gpt-4o', 'openai', 15220, 11269, 26489, 3, strftime('%Y-%m-%dT12:36:00Z','now','-26 days')),
  (33, 1, 'claude-3-7-sonnet', 'anthropic', 12605, 5647, 18252, 3, strftime('%Y-%m-%dT11:47:00Z','now','-27 days')),
  (34, 1, 'claude-3-7-sonnet', 'anthropic', 17057, 6321, 23378, 3, strftime('%Y-%m-%dT11:47:00Z','now','-27 days')),
  (35, 1, 'llama3.1:8b', 'ollama', 3504, 3134, 6638, 2, strftime('%Y-%m-%dT10:08:00Z','now','-28 days')),
  (36, 1, 'gemini-2.5-pro', 'google', 7123, 6723, 13846, 5, strftime('%Y-%m-%dT13:19:00Z','now','-29 days'));

-- -- messages --
INSERT INTO messages (session_id, role, content, tool_call_id, tool_calls_json, created_at) VALUES
  (1, 'user', 'Refactor the retry logic in provider.go and add tests', '', '', strftime('%Y-%m-%dT12:05:00Z','now','-29 days')),
  (1, 'assistant', '', 'c1', '[{"ToolCallId":"c1","ToolName":"read_file"}]', strftime('%Y-%m-%dT12:20:00Z','now','-29 days')),
  (1, 'tool', 'Read provider.go (1,024 lines).', 'c1', '', strftime('%Y-%m-%dT12:21:00Z','now','-29 days')),
  (1, 'assistant', 'Wrapped retries in a retryingProvider with jittered backoff.', '', '', strftime('%Y-%m-%dT12:40:00Z','now','-29 days')),
  (2, 'user', 'Explain how the tiktoken estimate is wired up', '', '', strftime('%Y-%m-%dT09:30:00Z','now','-23 days')),
  (2, 'assistant', 'It runs tiktoken_go with the gpt-4 encoding when the', '', '', strftime('%Y-%m-%dT09:45:00Z','now','-23 days')),
  (2, 'user', 'Add a filter input to the stats page', '', '', strftime('%Y-%m-%dT14:02:00Z','now','-16 days')),
  (2, 'assistant', '', '', '{"ToolCallId":"c2","ToolName":"edit_file"}', strftime('%Y-%m-%dT14:30:00Z','now','-16 days')),
  (2, 'tool', 'Edited Tui/Components/Stats/stats_screen.go.', 'c2', '', strftime('%Y-%m-%dT14:31:00Z','now','-16 days')),
  (3, 'user', 'Compare gemini 2.5 to gpt-4o on long refactors', '', '', strftime('%Y-%m-%dT10:15:00Z','now','-14 days')),
  (4, 'user', 'Extract the tool dispatcher into its own package', '', '', strftime('%Y-%m-%dT11:00:00Z','now','-5 days')),
  (4, 'assistant', '', '', '{"ToolCallId":"c3","ToolName":"create_file"}', strftime('%Y-%m-%dT11:20:00Z','now','-5 days')),
  (4, 'tool', 'Wrote tools/tool.go.', 'c3', '', strftime('%Y-%m-%dT11:21:00Z','now','-5 days')),
  (5, 'user', 'Polish the stats tab and run the test suite', '', '', strftime('%Y-%m-%dT08:45:00Z','now','-0 days'));

