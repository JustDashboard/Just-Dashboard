package deploy

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// RunTranscriptLine is one piece of output a run's step printed, as the run
// page's transcript draws it. Its text is what was stored, so a value the
// build or a release task masked as it was written stays masked here.
type RunTranscriptLine struct {
	Seq    int64     `json:"seq"`
	StepID int64     `json:"stepId,omitempty"`
	Stream string    `json:"stream"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
}

// RunTranscriptTail reads a run's last limit log lines in order, and whether
// earlier ones exist, kept or compacted away. The end of a transcript is
// where a failure is, so a bounded read keeps the end. A run that predates
// sequenced chunks has only the bounded tail on its row, read as lines.
func (s *OrchestrationStore) RunTranscriptTail(ctx context.Context, runID int64, limit int) ([]RunTranscriptLine, bool, error) {
	if limit <= 0 {
		return []RunTranscriptLine{}, false, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, step_id, stream, ts, text FROM deploy_log_chunks
		 WHERE run_id = ? AND event_type = ? AND compacted = 0
		 ORDER BY seq DESC LIMIT ?`, runID, EventStepLog, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	lines := []RunTranscriptLine{}
	for rows.Next() {
		var line RunTranscriptLine
		var ts int64
		if err := rows.Scan(&line.Seq, &line.StepID, &line.Stream, &ts, &line.Text); err != nil {
			return nil, false, err
		}
		line.At = unixTime(ts)
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	earlier := len(lines) > limit
	if earlier {
		lines = lines[:limit]
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	if !earlier {
		var compacted int
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM deploy_log_chunks WHERE run_id = ? AND compacted = 1`, runID).
			Scan(&compacted); err != nil {
			return nil, false, err
		}
		earlier = compacted > 0
	}
	if len(lines) > 0 || earlier {
		return lines, earlier, nil
	}
	var legacy string
	var ts int64
	err = s.db.QueryRowContext(ctx, `SELECT log, requested_at FROM deploy_runs WHERE id = ?`, runID).Scan(&legacy, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrRunNotFound
	}
	if err != nil {
		return nil, false, err
	}
	all := strings.Split(strings.TrimRight(legacy, "\n"), "\n")
	if legacy == "" {
		all = nil
	}
	if len(all) > limit {
		all, earlier = all[len(all)-limit:], true
	}
	for i, text := range all {
		lines = append(lines, RunTranscriptLine{Seq: int64(i + 1), Stream: "stdout", At: unixTime(ts), Text: text})
	}
	return lines, earlier || strings.HasPrefix(legacy, "…"), nil
}

// ProjectRunByNumber finds a project's run by the number its page shows.
func (s *OrchestrationStore) ProjectRunByNumber(ctx context.Context, projectID, number int64) (*EngineRun, error) {
	run, err := scanEngineRun(s.db.QueryRowContext(ctx, `SELECT `+engineRunColumns+`
		FROM deploy_runs WHERE project_id = ? AND run_number = ?`, projectID, number))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	return run, err
}

// StepLabel is a step's name as the run page writes it.
func StepLabel(key StepKey, operation Operation) string { return stepLabel(key, operation) }
