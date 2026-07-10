CREATE TABLE projects (
  id               TEXT PRIMARY KEY,
  name             TEXT NOT NULL UNIQUE,
  repo             TEXT NOT NULL COLLATE NOCASE UNIQUE,
  server_id        TEXT NOT NULL REFERENCES servers(id),
  target           TEXT NOT NULL DEFAULT '',
  workdir          TEXT NOT NULL,
  base_branch      TEXT NOT NULL DEFAULT 'main',
  provider         TEXT NOT NULL DEFAULT 'claude',
  required_reviews TEXT NOT NULL DEFAULT '[]',
  max_parallel     INTEGER NOT NULL DEFAULT 1,
  paused           INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);

CREATE TABLE epics (
  id               TEXT PRIMARY KEY,
  project_id       TEXT NOT NULL REFERENCES projects(id),
  issue_number     INTEGER NOT NULL,
  title            TEXT NOT NULL DEFAULT '',
  labels           TEXT NOT NULL DEFAULT '[]',
  blocked_by       TEXT NOT NULL DEFAULT '[]',
  stage            TEXT NOT NULL DEFAULT 'queued',
  attempt          INTEGER NOT NULL DEFAULT 0,
  session_name     TEXT NOT NULL DEFAULT '',
  branch           TEXT NOT NULL DEFAULT '',
  pr_number        INTEGER NOT NULL DEFAULT 0,
  verdict          TEXT NOT NULL DEFAULT '',
  needs            TEXT NOT NULL DEFAULT '',
  issue_state      TEXT NOT NULL DEFAULT 'open',
  queued_at        TEXT NOT NULL,
  started_at       TEXT NOT NULL DEFAULT '',
  stage_updated_at TEXT NOT NULL,
  merged_at        TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  UNIQUE(project_id, issue_number)
);
CREATE INDEX idx_epics_project_stage ON epics(project_id, stage);

CREATE TABLE epic_events (
  id         TEXT PRIMARY KEY,
  epic_id    TEXT NOT NULL REFERENCES epics(id),
  from_stage TEXT NOT NULL,
  to_stage   TEXT NOT NULL,
  source     TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  ts         TEXT NOT NULL
);
CREATE INDEX idx_epic_events_epic ON epic_events(epic_id, ts);
