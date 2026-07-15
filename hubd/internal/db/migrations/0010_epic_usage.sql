CREATE TABLE epic_usage (
  id            TEXT PRIMARY KEY,
  project_id    TEXT NOT NULL,
  project_name  TEXT NOT NULL,
  repo          TEXT NOT NULL,
  issue_number  INTEGER NOT NULL,
  attempt       INTEGER NOT NULL,
  stage         TEXT NOT NULL,
  captured_at   TEXT NOT NULL,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  input_tokens        INTEGER NOT NULL,
  output_tokens       INTEGER NOT NULL,
  cache_read_tokens   INTEGER NOT NULL,
  cache_write_tokens  INTEGER NOT NULL,
  UNIQUE(project_id, issue_number, attempt, stage, captured_at, provider, model)
);
CREATE INDEX idx_epic_usage_project ON epic_usage(project_id);
CREATE INDEX idx_epic_usage_epic ON epic_usage(project_id, issue_number);
