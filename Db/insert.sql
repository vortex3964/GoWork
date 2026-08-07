-- GoWork test data.
-- Run this against Db/gowork.db after the app has created the schema once
-- (the app auto-creates tables on open):
--
--   sqlite3 Db/gowork.db < Db/insert.sql
--
-- All timestamps are ISO-8601 (UTC). They sit inside the 30-day sliding
-- window so the stats graph and model list fill up immediately. The two
-- rows at the bottom are deliberately older than 30 days to prove the
-- retention cleanup drops them on the next save.

-- ── sessions (one app run = one session; only the last 5 are kept) ──
INSERT INTO sessions (id, started_at, ended_at, model, provider, total_tokens, message_count) VALUES
  (1, '2026-07-25T09:12:33Z', '2026-07-25T09:41:02Z', 'gemini-2.5-pro',      'google',    45218, 9),
  (2, '2026-07-29T14:03:11Z', '2026-07-29T15:00:05Z', 'claude-3-7-sonnet', 'anthropic', 38100, 12),
  (3, '2026-08-01T08:20:47Z', '2026-08-01T09:11:30Z', 'gpt-4o',             'openAi',    18950, 6),
  (4, '2026-08-03T16:05:22Z', '2026-08-03T16:58:41Z', 'qwen2.5-coder:14b', 'ollama',     8900, 7),
  (5, '2026-08-05T11:05:59Z', '2026-08-05T12:02:15Z', 'llama3.1:8b',        'ollama',    4200, 4),
  (6, '2026-06-10T10:00:00Z', '2026-06-10T10:40:00Z', 'gpt-4o',             'openAi',    9999, 5),
  (7, '2026-05-20T18:00:00Z', '2026-05-20T18:30:00Z', 'gemini-2.0-flash',   'google',    8888, 3);

-- ── usage (per-generation rows; retention: last 30 days) ──
INSERT INTO usage (id, session_id, model, provider, prompt_tokens, completion_tokens, total_tokens, tool_calls, created_at) VALUES
  (1,  1, 'gemini-2.5-pro',      'google',    12000, 5400, 17400, 4, '2026-07-25T09:20:00Z'),
  (2,  1, 'gemini-2.5-pro',      'google',     4000, 2000,  6000, 1, '2026-07-25T09:40:00Z'),
  (3,  1, 'gemini-2.5-pro',      'google',     4200, 1800,  6000, 2, '2026-07-25T09:55:00Z'),
  (4,  2, 'claude-3-7-sonnet',   'anthropic',  8000, 4100, 12100, 3, '2026-07-29T14:50:00Z'),
  (5,  2, 'claude-3-7-sonnet',   'anthropic',  5000, 3000,  8000, 2, '2026-07-29T14:55:00Z'),
  (6,  2, 'claude-3-7-sonnet',   'anthropic',  9000, 4000, 13000, 5, '2026-07-29T15:00:00Z'),
  (7,  2, 'claude-3-7-sonnet',   'anthropic',  3000, 2000,  5000, 0, '2026-07-29T15:00:01Z'),
  (8,  3, 'gpt-4o',              'openai',    11000, 4950, 15950, 2, '2026-08-01T08:50:00Z'),
  (9,  3, 'gpt-4o',              'openai',     5000, 1000,  6000, 1, '2026-08-01T09:10:00Z'),
  (10, 3, 'mistral-large',       'ollama',     2500,  600,  3100, 0, '2026-08-01T09:11:00Z'),
  (11, 4, 'qwen2.5-coder:14b',   'ollama',     4000, 2200,  6200, 3, '2026-08-03T16:20:00Z'),
  (12, 4, 'qwen2.5-coder:14b',   'ollama',     2000,  700,  2700, 1, '2026-08-03T16:55:00Z'),
  (13, 5, 'llama3.1:8b',         'ollama',     2000,  800,  2800, 0, '2026-08-05T11:30:00Z'),
  (14, 5, 'llama3.1:8b',         'ollama',      900,   500,  1400, 1, '2026-08-05T12:00:00Z'),
  (15, 6, 'gpt-4o',              'openai',    7000,  2999,  9999, 3, '2026-06-10T10:10:00Z'),
  (16, 7, 'gemini-2.0-flash',    'google',    5000,  3888,  8888, 2, '2026-05-20T18:10:00Z');

-- ── messages ──
INSERT INTO messages (session_id, role, content, tool_call_id, tool_calls_json, created_at) VALUES
  (1, 'user',      'Refactor the retry logic in provider.go and add tests', '', '', '2026-07-25T09:12:33Z'),
  (1, 'assistant', '', '', '[{"Tool_call_id":"c1","Tool_name":"read_file"}]', '2026-07-25T09:20:00Z'),
  (1, 'tool',      'provider.go: retryingProvider now.', 'c1', '', '2026-07-25T09:20:01Z'),
  (1, 'assistant', 'Done - wrapped retries in a retryingProvider and added a backoff test.', '', '', '2026-07-25T09:40:00Z'),
  (2, 'user',      'Explain how the tiktoken estimate works', '', '', '2026-07-29T14:40:11Z'),
  (2, 'assistant', 'It runs tiktoken_go with the gpt-4 encoding...', '', '', '2026-07-29T14:50:00Z'),
  (3, 'user',      'Add a filter input to the stats page', '', '', '2026-08-01T08:20:47Z'),
  (3, 'assistant', 'Implemented a / filter matching the changes list.', '', '', '2026-08-01T09:11:00Z');