ALTER TABLE epics ADD COLUMN reaped_at TEXT NOT NULL DEFAULT '';
-- Pre-existing merged epics are considered already reaped: their runner sessions
-- are long gone and the new per-tick reap loop must only act on FUTURE merges, not
-- sweep historical worktrees on the first boot after this migration.
UPDATE epics SET reaped_at = datetime('now') WHERE stage = 'merged';
