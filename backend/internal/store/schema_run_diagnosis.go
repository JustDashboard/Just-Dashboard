package store

// runDiagnosisSchema holds the one table of a failed run's diagnosis
// (deploy/run_diagnosis_store.go): the last answer the assistant's provider
// gave about a run, so reopening the run reads it instead of paying for it
// again. Kept apart from `schema` so the block is read in one place; it is
// concatenated into `schema`, so the same rules hold: CREATE ... IF NOT
// EXISTS only, and a column the table gains after it has shipped goes into
// addedColumns.
const runDiagnosisSchema = `
-- One row per run, replaced when somebody asks again. sent_text is exactly
-- what went to the provider, already masked, so the page can show what left
-- the server; answer_json is the answer read into its parts. Neither holds a
-- value the run's page does not already show. The row goes with its run.
CREATE TABLE IF NOT EXISTS deploy_run_diagnoses (
  run_id      INTEGER PRIMARY KEY REFERENCES deploy_runs(id) ON DELETE CASCADE,
  provider    TEXT NOT NULL,
  model       TEXT NOT NULL,
  answer_json TEXT NOT NULL,
  sent_text   TEXT NOT NULL,
  sent_lines  INTEGER NOT NULL DEFAULT 0,
  masked      INTEGER NOT NULL DEFAULT 0,
  usage_json  TEXT NOT NULL DEFAULT '{}',
  created_by  TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);
`
