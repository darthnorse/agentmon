-- 0002: reshape `servers` from the M0 static-config shape to the enrollment shape.
-- The data volume is fresh (nothing deployed), so we drop and recreate. This is the
-- first non-idempotent migration; migrate() now wraps each file in a transaction.
DROP TABLE IF EXISTS servers;
CREATE TABLE servers (
  id            TEXT PRIMARY KEY,                 -- default = hostname
  name          TEXT NOT NULL,                    -- display; default = hostname
  hostname      TEXT NOT NULL,
  url           TEXT NOT NULL,                    -- http://<lan-ip>:8377
  status        TEXT NOT NULL DEFAULT 'pending',  -- pending | active | revoked
  bearer        TEXT NOT NULL,                    -- hub→agent bearer (server-side secret)
  signing_key   TEXT NOT NULL,                    -- HMAC directive key (used by M4)
  labels        TEXT,
  os            TEXT,
  arch          TEXT,
  agent_version TEXT,
  last_seen_at  TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
