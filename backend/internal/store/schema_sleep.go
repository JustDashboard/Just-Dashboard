package store

// sleepSchema holds scale-to-zero (deploy/sleep.go): one row per stretch an
// environment's live release spent asleep — its container stopped by design
// because nobody had visited for the plan's idle minutes — from the moment it
// was put to sleep until a request, a press of Wake now, or a run woke it.
// An open row (woke_at 0) is the environment asleep now, which is what the
// overview, the wake, the host alerts and the availability figure read; the
// closed ones are its history. Concatenated into `schema`, so the same rules
// hold: CREATE ... IF NOT EXISTS only, and a column added after it has
// shipped goes into addedColumns.
const sleepSchema = `
-- release_id and container_id are the release and container that were
-- stopped, so a wake starts exactly that one and a row left by a release that
-- is no longer live is closed rather than acted on. reason is the idle
-- decision in words, last_request_at the newest request the record held when
-- it was made (0 for none since the release went live). wake_trigger is
-- request, manual, run or a repair; wake_seconds how long the start took to
-- answer; wake_error why a wake did not bring it back.
CREATE TABLE IF NOT EXISTS deploy_sleep_spans (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  environment_id  INTEGER NOT NULL REFERENCES deploy_environments(id) ON DELETE CASCADE,
  release_id      INTEGER NOT NULL,
  container_id    TEXT NOT NULL DEFAULT '',
  idle_minutes    INTEGER NOT NULL DEFAULT 0,
  reason          TEXT NOT NULL DEFAULT '',
  last_request_at INTEGER NOT NULL DEFAULT 0,
  slept_at        INTEGER NOT NULL,
  woke_at         INTEGER NOT NULL DEFAULT 0,
  wake_trigger    TEXT NOT NULL DEFAULT '',
  wake_detail     TEXT NOT NULL DEFAULT '',
  wake_seconds    REAL NOT NULL DEFAULT 0,
  wake_error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_deploy_sleep_spans_environment
  ON deploy_sleep_spans(environment_id, id DESC);
-- At most one open stretch per environment: it is asleep or it is not.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deploy_sleep_spans_open
  ON deploy_sleep_spans(environment_id) WHERE woke_at = 0;
`
