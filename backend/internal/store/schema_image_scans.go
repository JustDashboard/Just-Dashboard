package store

// imageScanSchema holds the base-image vulnerability watch (deploy/image_scan.go):
// each release's latest scan and each environment's terms. Kept apart from
// `schema` so the block is read in one place; it is concatenated into
// `schema`, so the same rules hold: CREATE ... IF NOT EXISTS only, and a
// column one of these tables gains after it has shipped goes into
// addedColumns.
const imageScanSchema = `
-- One row per release: its latest scan, replaced by the next. A scan asked
-- for and not yet run is 'queued'; the findings of the last one that read the
-- image stay while another runs or after one that failed. counts_json counts
-- every finding; findings_json keeps the release's own images' findings,
-- worst first and at most a thousand; targets_json each image read (the
-- release's, and the bases it was pinned on, with theirs); bases_json what the
-- registry said each base's tag names now. db_updated_at is when the
-- database the scan read was built. The row goes with its release.
CREATE TABLE IF NOT EXISTS deploy_image_scans (
  release_id     INTEGER PRIMARY KEY REFERENCES deploy_releases(id) ON DELETE CASCADE,
  environment_id INTEGER NOT NULL,
  state          TEXT NOT NULL,
  reason         TEXT NOT NULL DEFAULT '',
  trigger        TEXT NOT NULL DEFAULT '',
  scanner        TEXT NOT NULL DEFAULT '',
  db_updated_at  INTEGER NOT NULL DEFAULT 0,
  counts_json    TEXT NOT NULL DEFAULT '{}',
  findings_json  TEXT NOT NULL DEFAULT '[]',
  targets_json   TEXT NOT NULL DEFAULT '[]',
  bases_json     TEXT NOT NULL DEFAULT '[]',
  truncated      INTEGER NOT NULL DEFAULT 0,
  requested_at   INTEGER NOT NULL,
  started_at     INTEGER NOT NULL DEFAULT 0,
  finished_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_deploy_image_scans_environment
  ON deploy_image_scans(environment_id, release_id DESC);
-- No row is the defaults (deploy.DefaultImageScanPolicy): scanning on, from
-- HIGH up. A row exists once somebody saved the environment's terms, and goes
-- with it.
CREATE TABLE IF NOT EXISTS deploy_image_scan_policies (
  environment_id INTEGER PRIMARY KEY REFERENCES deploy_environments(id) ON DELETE CASCADE,
  enabled        INTEGER NOT NULL DEFAULT 1,
  severity_floor TEXT NOT NULL DEFAULT 'HIGH',
  updated_by     TEXT NOT NULL DEFAULT '',
  updated_at     INTEGER NOT NULL
);
`
