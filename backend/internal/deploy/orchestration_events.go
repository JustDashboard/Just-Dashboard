package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

type EventInput struct {
	Type   EventType
	StepID int64
	Stream string
	Text   string
	Data   json.RawMessage
}

func appendEventTx(
	ctx context.Context,
	tx *sql.Tx,
	now time.Time,
	runID, stepID int64,
	eventType EventType,
	stream, text string,
	data json.RawMessage,
) (RunEvent, error) {
	if !validEventType(eventType) || eventType == EventResync {
		return RunEvent{}, fmt.Errorf("invalid persisted deployment event type %q", eventType)
	}
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	if !json.Valid(data) {
		return RunEvent{}, fmt.Errorf("deployment event data is not valid JSON")
	}
	if len(data) > maxEventBytes {
		return RunEvent{}, ErrEventPayloadTooLarge
	}
	text, truncated := truncateUTF8(text, maxLogTextBytes)
	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM deploy_log_chunks WHERE run_id = ?`,
		runID).Scan(&seq); err != nil {
		return RunEvent{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_log_chunks(
		  run_id, step_id, seq, event_type, stream, ts, text, data_json, truncated)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, stepID, seq, eventType, stream, now.Unix(), text, string(data), boolInt(truncated)); err != nil {
		return RunEvent{}, err
	}
	return RunEvent{Seq: seq, Type: eventType, RunID: runID, StepID: stepID, TS: now, Data: data}, nil
}

// AppendEvent appends feature evidence only while the caller owns the active
// lease. State transitions have dedicated methods so they cannot be smuggled
// through a generic event write.
func (s *OrchestrationStore) AppendEvent(
	ctx context.Context,
	runID int64,
	claimToken string,
	input EventInput,
) (RunEvent, error) {
	if input.Type == EventRunState || input.Type == EventStepState || input.Type == EventResync {
		return RunEvent{}, fmt.Errorf("event type %q requires its state method", input.Type)
	}
	if input.Type == EventStepLog {
		return s.AppendLog(ctx, runID, input.StepID, claimToken, input.Stream, input.Text)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RunEvent{}, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, now); err != nil {
		return RunEvent{}, err
	}
	event, err := appendEventTx(ctx, tx, now, runID, input.StepID, input.Type,
		input.Stream, input.Text, input.Data)
	if err != nil {
		return RunEvent{}, err
	}
	if input.StepID != 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE deploy_steps SET last_seq = ? WHERE id = ? AND run_id = ?`,
			event.Seq, input.StepID, runID); err != nil {
			return RunEvent{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RunEvent{}, err
	}
	s.publish([]RunEvent{event})
	return event, nil
}

// LogLine is one line of a step's output on its way into the transcript.
type LogLine struct {
	Stream string
	Text   string
}

// AppendLog appends one line. A step's output goes through storeStepOutput,
// which hands over many lines at a time; this is for the single sentences
// written outside a step.
func (s *OrchestrationStore) AppendLog(
	ctx context.Context,
	runID, stepID int64,
	claimToken, stream, text string,
) (RunEvent, error) {
	events, err := s.AppendLogs(ctx, runID, stepID, claimToken, []LogLine{{Stream: stream, Text: text}})
	if err != nil {
		return RunEvent{}, err
	}
	return events[0], nil
}

// AppendLogs appends lines to a run's transcript in one transaction, in order
// and with consecutive sequence numbers: one lease check, one read of the
// run's sequence, one row per line, one update of the step and one of the
// legacy tail. A line used to be a transaction of its own, six statements and
// a commit each, so a chatty build spent longer persisting its output than
// printing it and every other writer queued behind thousands of tiny write
// locks.
func (s *OrchestrationStore) AppendLogs(
	ctx context.Context,
	runID, stepID int64,
	claimToken string,
	lines []LogLine,
) ([]RunEvent, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	type prepared struct {
		stream, text string
		truncated    bool
		data         json.RawMessage
	}
	rows := make([]prepared, len(lines))
	tailBytes := 0
	for index, line := range lines {
		if line.Stream != "stdout" && line.Stream != "stderr" && line.Stream != "status" {
			return nil, fmt.Errorf("invalid deployment log stream %q", line.Stream)
		}
		row := prepared{stream: line.Stream}
		row.text, row.truncated = truncateUTF8(line.Text, maxLogTextBytes)
		// The event a subscriber receives is bounded as well as the stored
		// text, and bounding it can shorten the text further.
		row.data = logEventData(row.stream, &row.text, &row.truncated)
		rows[index] = row
		tailBytes += len(row.text)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Through the store's bulk writer, which does not sync the disk on every
	// commit: a power cut may take the last moment of what a build printed,
	// and takes the build with it. How a step and its run ended is written
	// on the durable pool, and that commit makes these lines durable too.
	tx, err := s.st.Bulk().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if err := assertLeaseTx(ctx, tx, runID, claimToken, now); err != nil {
		return nil, err
	}
	if stepID != 0 {
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM deploy_steps WHERE id = ? AND run_id = ?`, stepID, runID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrStepNotFound
			}
			return nil, err
		}
	}
	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM deploy_log_chunks WHERE run_id = ?`, runID).Scan(&seq); err != nil {
		return nil, err
	}
	// A line's text is stored once, in its own column beside its stream and
	// whether it was cut. data_json used to repeat all three as JSON, which
	// was most of what a transcript weighed; EventsAfter builds the event's
	// data from the columns instead.
	insert, err := tx.PrepareContext(ctx, `
		INSERT INTO deploy_log_chunks(
		  run_id, step_id, seq, event_type, stream, ts, text, data_json, truncated)
		VALUES(?, ?, ?, ?, ?, ?, ?, '{}', ?)`)
	if err != nil {
		return nil, err
	}
	defer insert.Close()
	events := make([]RunEvent, len(rows))
	tail := make([]byte, 0, tailBytes)
	for index, row := range rows {
		seq++
		if _, err := insert.ExecContext(ctx, runID, stepID, seq, EventStepLog, row.stream, now.Unix(),
			row.text, boolInt(row.truncated)); err != nil {
			return nil, err
		}
		events[index] = RunEvent{Seq: seq, Type: EventStepLog, RunID: runID, StepID: stepID, TS: now, Data: row.data}
		tail = append(tail, row.text...)
	}
	if stepID != 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE deploy_steps SET last_seq = ? WHERE id = ?`, seq, stepID); err != nil {
			return nil, err
		}
	}
	// The 0.6.6 run shape still answers with deploy_runs.log (the legacy run
	// list and a deployment's lastRun, both in the published API), so a
	// bounded tail is kept beside the chunks, which remain authoritative. It
	// is rewritten once for the lines of this call rather than once a line.
	// What the tail cannot hold is not sent: only its end survives anyway.
	appended := legacyLogTail(string(tail))
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs
		   SET log = CASE
		     WHEN length(log) + length(?) <= 64000 THEN log || ?
		     ELSE '…' || substr(log || ?, -64000)
		   END
		 WHERE id = ?`, appended, appended, appended, runID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.publish(events)
	return events, nil
}

// legacyLogTail is the end of text that the 64,000-character legacy tail can
// still show, cut on a character boundary.
func legacyLogTail(text string) string {
	const keep = 64000 * utf8.UTFMax
	if len(text) <= keep {
		return text
	}
	cut := len(text) - keep
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return text[cut:]
}

func logEventData(stream string, text *string, truncated *bool) json.RawMessage {
	for {
		data := mustJSON(map[string]any{
			"stream": stream, "text": *text, "truncated": *truncated,
		})
		if len(data) <= maxEventBytes {
			return data
		}
		// JSON escaping can make control-heavy output several times larger than
		// its persisted text. Reduce by bytes while retaining a complete rune;
		// the event envelope's bound is as important as the text-column bound.
		limit := len(*text) * 3 / 4
		if limit == len(*text) {
			limit--
		}
		*text, _ = truncateUTF8(*text, limit)
		*truncated = true
	}
}

// EventsAfter returns retained events in sequence order. If compaction removed
// payload newer than after, the first event is an in-memory resync snapshot;
// resync is never persisted into the run sequence itself.
func (s *OrchestrationStore) EventsAfter(
	ctx context.Context,
	runID, after int64,
	limit int,
) ([]RunEvent, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	if _, err := s.Run(ctx, runID); err != nil {
		return nil, err
	}
	var watermark int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) FROM deploy_log_chunks
		 WHERE run_id = ? AND compacted = 1`, runID).Scan(&watermark); err != nil {
		return nil, err
	}
	result := make([]RunEvent, 0, limit+1)
	start := after
	if after < watermark {
		snapshot, err := s.Snapshot(ctx, runID)
		if err != nil {
			return nil, err
		}
		var oldest int64
		if err := s.db.QueryRowContext(ctx, `
			SELECT COALESCE(MIN(seq), 0) FROM deploy_log_chunks
			 WHERE run_id = ? AND seq > ? AND compacted = 0`, runID, watermark).Scan(&oldest); err != nil {
			return nil, err
		}
		result = append(result, RunEvent{
			Seq: watermark, Type: EventResync, RunID: runID, TS: s.now().UTC(),
			Data: mustJSON(map[string]any{"oldestSeq": oldest, "snapshot": snapshot}),
		})
		start = watermark
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, step_id, event_type, ts, data_json, stream, text, truncated
		  FROM deploy_log_chunks
		 WHERE run_id = ? AND seq > ? AND compacted = 0
		 ORDER BY seq
		 LIMIT ?`, runID, start, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var event RunEvent
		var ts int64
		var data, stream, text string
		var truncated bool
		event.RunID = runID
		if err := rows.Scan(&event.Seq, &event.StepID, &event.Type, &ts, &data, &stream, &text, &truncated); err != nil {
			return nil, err
		}
		event.TS = unixTime(ts)
		event.Data = json.RawMessage(data)
		// A line written since its text stopped being stored twice carries no
		// data of its own. One written before still does, and so does the
		// transcript a 0.6.6 run was migrated with, which names its migration.
		if event.Type == EventStepLog && data == "{}" {
			event.Data = logEventData(stream, &text, &truncated)
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

// CompactRunLogs retains the newest log payloads and one sequence watermark.
// State/evidence/artifact events are never selected. The watermark makes a
// reconnect request prove that it needs a fresh snapshot even when retained
// state events have older sequence numbers.
func (s *OrchestrationStore) CompactRunLogs(ctx context.Context, runID int64, keep int) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if keep < 0 {
		return fmt.Errorf("negative deployment log retention")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var terminal string
	if err := tx.QueryRowContext(ctx,
		`SELECT state FROM deploy_runs WHERE id = ?`, runID).Scan(&terminal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRunNotFound
		}
		return err
	}
	if !RunState(terminal).Terminal() {
		return fmt.Errorf("deployment logs compact only after the run is terminal")
	}
	var watermark int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) FROM deploy_log_chunks
		 WHERE run_id = ? AND event_type = ? AND compacted = 0
		   AND seq NOT IN (
		     SELECT seq FROM deploy_log_chunks
		      WHERE run_id = ? AND event_type = ? AND compacted = 0
		      ORDER BY seq DESC LIMIT ?
		   )`, runID, EventStepLog, runID, EventStepLog, keep).Scan(&watermark); err != nil {
		return err
	}
	if watermark == 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM deploy_log_chunks
		 WHERE run_id = ? AND event_type = ? AND compacted = 0 AND seq < ?`,
		runID, EventStepLog, watermark); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_log_chunks
		   SET compacted = 1, stream = 'status', text = '', data_json = '{}',
		       redacted = 0, truncated = 0
		 WHERE run_id = ? AND seq = ?`, runID, watermark); err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
