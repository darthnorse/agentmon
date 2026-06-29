CREATE TABLE push_subscriptions (
  principal_id TEXT NOT NULL,
  endpoint     TEXT NOT NULL,
  p256dh       TEXT NOT NULL,
  auth         TEXT NOT NULL,
  user_agent   TEXT,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (endpoint)
);
CREATE INDEX idx_push_principal ON push_subscriptions(principal_id);

CREATE TABLE push_vapid (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  public_key  TEXT NOT NULL,
  private_key TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
