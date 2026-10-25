package accountlink

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
)

var (
	// A container or stack name, as Docker and Compose accept one.
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,2}$`)
	clockPattern   = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	// A ULID: Crockford base32 without I, L, O and U.
	commandIDPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
)

// Args are a command's arguments once they have been checked against the kind.
type Args struct {
	Container string
	Name      string
	JobID     int64
	Version   string
	Policy    UpdatePolicy
	Rebuild   bool
}

// refusal is a command this dashboard will not run, and the reason it reports.
type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

func refuse(format string, a ...any) error { return refusal{fmt.Sprintf(format, a...)} }

// onlyKeys insists on exactly the argument names a kind takes: no extras, none
// missing.
func onlyKeys(v map[string]any, keys ...string) error {
	if len(keys) == 0 {
		if len(v) != 0 {
			return errors.New("takes no arguments")
		}
		return nil
	}
	if len(v) != len(keys) {
		return fmt.Errorf("takes exactly %s", strings.Join(keys, ", "))
	}
	for _, k := range keys {
		if _, ok := v[k]; !ok {
			return fmt.Errorf("takes exactly %s", strings.Join(keys, ", "))
		}
	}
	return nil
}

func stringArg(v map[string]any, key string, pattern *regexp.Regexp) (string, error) {
	s, ok := v[key].(string)
	if !ok || !pattern.MatchString(s) {
		return "", fmt.Errorf("%s is not an acceptable value", key)
	}
	return s, nil
}

// parseArgs applies protocol section 5: the keys a kind takes, the shape of
// each value, and nothing else.
func parseArgs(kind string, v map[string]any) (Args, error) {
	var a Args
	var err error
	switch kind {
	case KindSnapshotNow:
		err = onlyKeys(v)
	case KindContainerStart, KindContainerStop, KindContainerReboot:
		if err = onlyKeys(v, "container"); err == nil {
			a.Container, err = stringArg(v, "container", namePattern)
		}
	case KindStackRestart:
		if err = onlyKeys(v, "name"); err == nil {
			a.Name, err = stringArg(v, "name", namePattern)
		}
	case KindBackupRun:
		if err = onlyKeys(v, "jobId"); err == nil {
			n, ok := v["jobId"].(json.Number)
			id, convErr := n.Int64()
			if !ok || convErr != nil || id < 1 || id > maxSafeInt {
				err = errors.New("jobId must be a positive integer")
			}
			a.JobID = id
		}
	case KindUpdateInstall:
		if err = onlyKeys(v, "version"); err == nil {
			a.Version, err = stringArg(v, "version", versionPattern)
		}
	case KindUpdatePolicy:
		if err = onlyKeys(v, "auto", "window"); err != nil {
			break
		}
		auto, ok := v["auto"].(string)
		window, wok := v["window"].(map[string]any)
		if !ok || !wok {
			err = errors.New("auto must be a string and window an object")
			break
		}
		if err = onlyKeys(window, "start", "end"); err != nil {
			err = fmt.Errorf("window %w", err)
			break
		}
		start, _ := window["start"].(string)
		end, _ := window["end"].(string)
		a.Policy = UpdatePolicy{Auto: auto, Window: Window{Start: start, End: end}}
		err = a.Policy.validate()
	case KindRestart:
		if err = onlyKeys(v, "rebuild"); err == nil {
			rebuild, ok := v["rebuild"].(bool)
			if !ok {
				err = errors.New("rebuild must be true or false")
			}
			a.Rebuild = rebuild
		}
	default:
		err = errors.New("the kind is not one this dashboard runs")
	}
	if err != nil {
		return Args{}, fmt.Errorf("%s: %w", kind, err)
	}
	return a, nil
}

// verify decides whether a command may run, in the order the protocol lists:
// the key it names is one this dashboard pinned, its signature verifies for this
// server, it has not expired, it has not been seen before, and its kind fits
// the scope chosen here. Only then are its arguments checked against the kind.
// A command that fails any of it is a refusal, and everything past the
// signature is recorded so the same command cannot be offered twice.
func (s *Service) verify(ctx context.Context, st State, cmd Command, now time.Time) (Args, claim, error) {
	if !commandIDPattern.MatchString(cmd.ID) {
		return Args{}, unclaimed, refuse("the command id is not a ULID")
	}
	if len(cmd.Kind) == 0 || len(cmd.Kind) > 64 {
		return Args{}, unclaimed, refuse("the command kind is missing or too long")
	}
	key, ok := st.siteKey(cmd.Kid, now)
	if !ok {
		return Args{}, unclaimed, refuse("the command is signed with key %q, which this server has not pinned", clip(cmd.Kid, 32))
	}
	pub, err := key.key()
	if err != nil {
		return Args{}, unclaimed, refuse("%v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(cmd.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Args{}, unclaimed, refuse("the command signature is not a base64 Ed25519 signature")
	}
	decoded, err := decodeArgs(cmd.Args)
	if err != nil {
		return Args{}, unclaimed, refuse("%v", err)
	}
	canonical, err := canonicalJSON(decoded)
	if err != nil {
		return Args{}, unclaimed, refuse("%v", err)
	}
	if !ed25519.Verify(pub, []byte(commandMessage(st.ServerID, cmd.ID, cmd.Kind, canonical, cmd.ExpiresAt)), sig) {
		return Args{}, unclaimed, refuse("the command signature does not verify for this server")
	}
	if cmd.ExpiresAt <= now.Unix() {
		return Args{}, unclaimed, refuse("the command expired at %s", time.Unix(cmd.ExpiresAt, 0).UTC().Format(time.RFC3339))
	}
	// The site expires a command ten minutes after issuing it. The record that
	// stops a replay is pruned after a week, so an expiry further off than an
	// hour would be a command that could be offered again once it was gone.
	if cmd.ExpiresAt > now.Add(maxCommandLifetime).Unix() {
		return Args{}, unclaimed, refuse("the command expires more than %s from now, which is longer than any command lives", maxCommandLifetime)
	}
	fresh, err := s.remember(ctx, cmd, now)
	if err != nil {
		return Args{}, unclaimed, fmt.Errorf("record the command before running it: %w", err)
	}
	if !fresh {
		return Args{}, seenBefore, refuse("the command was already received")
	}
	need, known := RequiredScope(cmd.Kind)
	if !known {
		return Args{}, claimed, refuse("the kind %q is not one this dashboard runs", clip(cmd.Kind, 64))
	}
	if !st.Scope.Includes(need) {
		return Args{}, claimed, refuse("%s needs scope %s; this server is linked at %s", cmd.Kind, need, st.Scope)
	}
	args, err := parseArgsOrRefuse(cmd.Kind, decoded)
	return args, claimed, err
}

// claim says what a verification did to the replay record of a command's id,
// which decides what may be written about that id afterwards.
type claim int

const (
	// unclaimed: it failed before the record, so there is none of this
	// command's to finish. The site is still told, since it is waiting.
	unclaimed claim = iota
	// claimed: this call wrote the record and is the only one that may finish it.
	claimed
	// seenBefore: an earlier delivery of the id holds the record. Whatever
	// became of that one is what the id means; a second delivery neither
	// rewrites it nor reports under it.
	seenBefore
)

// maxCommandLifetime is the furthest ahead of now a command's expiry may be.
const maxCommandLifetime = time.Hour

func parseArgsOrRefuse(kind string, decoded map[string]any) (Args, error) {
	a, err := parseArgs(kind, decoded)
	if err != nil {
		return Args{}, refuse("invalid arguments: %v", err)
	}
	return a, nil
}

// known reports whether an id has a record, which any delivery of it may have
// made.
func (s *Service) known(ctx context.Context, id string) bool {
	var one int
	return s.st.DB.QueryRowContext(ctx, `SELECT 1 FROM account_link_commands WHERE id = ?`, id).Scan(&one) == nil
}

// remember records a command that passed its signature. False means the id was
// there already.
func (s *Service) remember(ctx context.Context, cmd Command, now time.Time) (bool, error) {
	res, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO account_link_commands(id, kind, received_at, status, issued_by) VALUES(?, ?, ?, 'running', ?)
		 ON CONFLICT(id) DO NOTHING`, cmd.ID, cmd.Kind, now.Unix(), clip(cmd.IssuedBy, 254))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	// The record only has to outlive the time a command can still be offered,
	// which its own expiry bounds; a week is far past that.
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM account_link_commands WHERE received_at < ?`, now.Add(-7*24*time.Hour).Unix())
	return n == 1, nil
}

func (s *Service) finishRecord(ctx context.Context, id, status, errText string, at time.Time) {
	// A row that was never written (a signature that did not verify) simply
	// matches nothing.
	if _, err := s.st.DB.ExecContext(ctx,
		`UPDATE account_link_commands SET status = ?, finished_at = ?, error = ? WHERE id = ? AND status = 'running'`,
		status, at.Unix(), clip(errText, 1000), id); err != nil {
		s.log.Warn("could not record a command's outcome", "command", id, "err", err)
	}
}

// commandTimeout is the longest one command may hold the command loop.
const commandTimeout = 10 * time.Minute

// process runs one command start to finish: verify, execute, record, audit,
// report. Nothing it does can be reached without passing verify.
func (s *Service) process(ctx context.Context, st State, cmd Command) {
	started := s.now()
	var (
		status = resultSucceeded
		output string
		errMsg string
	)
	args, owned, err := s.verify(ctx, st, cmd, started)
	if err != nil && owned == unclaimed && s.known(ctx, cmd.ID) {
		// It failed before the record was reached, but the id is one that was
		// recorded: a copy of a command already received, whatever else is
		// wrong with it. It is the earlier delivery's id.
		owned = seenBefore
	}
	if err == nil {
		execCtx, cancel := context.WithTimeout(ctx, commandTimeout)
		output, err = s.execute(execCtx, cmd, args, "account:"+st.Owner)
		cancel()
	}
	if err != nil {
		var ref refusal
		switch {
		case errors.As(err, &ref):
			status, errMsg = resultRefused, ref.reason
		case errors.Is(err, ErrVersionMoved):
			status, errMsg = resultRefused, err.Error()
		default:
			status, errMsg = resultFailed, err.Error()
		}
	}
	finished := s.now()
	errMsg = clip(errMsg, 1000)
	if owned == claimed {
		s.finishRecord(context.WithoutCancel(ctx), cmd.ID, status, errMsg, finished)
	}
	s.auditCommand(ctx, st, cmd, status, errMsg, args)
	if owned == seenBefore {
		// The id belongs to a command already received; reporting a refusal
		// under it would overwrite what the site holds for the real one.
		return
	}

	res := CommandResult{Status: status, StartedAt: started.Unix(), FinishedAt: finished.Unix(), Error: errMsg}
	if len(output) > 8<<10 {
		output = output[:8<<10]
	}
	res.Output = output
	s.report(ctx, st, cmd.ID, res)
	// The site shows what the machine looks like now, so say it again.
	s.kickHeartbeat()
}

// report tells the site how a command ended. It tries a few times, because the
// command already ran and the site is waiting to hear.
func (s *Service) report(ctx context.Context, st State, commandID string, res CommandResult) {
	id, _, err := loadIdentity(ctx, s.st, s.sealer)
	if err != nil || id == nil {
		return
	}
	c := s.newClient(id)
	delay := time.Second
	for attempt := 0; attempt < 3; attempt++ {
		err = c.result(ctx, st.ServerID, commandID, res)
		if err == nil || isCode(err, 404, "unknown_command") || ctx.Err() != nil {
			break
		}
		if s.linkEnded(ctx, st, err) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			delay *= 2
		}
	}
	if err != nil {
		s.log.Warn("the site was not told how a command ended", "command", commandID, "err", err)
	}
}

// auditCommand writes the command to this dashboard's own audit log, as the
// site account that holds the link, whatever happened to it.
func (s *Service) auditCommand(ctx context.Context, st State, cmd Command, status, errMsg string, args Args) {
	if s.audit == nil {
		return
	}
	action := "account.command.invalid"
	if _, known := RequiredScope(cmd.Kind); known {
		action = "account.command." + cmd.Kind
	}
	detail := map[string]any{"command": cmd.ID, "kind": clip(cmd.Kind, 64), "status": status, "issuedBy": clip(cmd.IssuedBy, 254)}
	// What was asked, as sent: names and numbers, which is what a refusal needs
	// to be understood afterwards.
	if len(cmd.Args) > 0 {
		detail["args"] = clip(string(cmd.Args), 300)
	}
	if errMsg != "" {
		detail["error"] = errMsg
	}
	httpStatus := 200
	switch status {
	case resultRefused:
		httpStatus = 403
	case resultFailed:
		httpStatus = 500
	}
	s.audit.Record(ctx, audit.Entry{
		Username: "account:" + st.Owner, Actor: "account:" + st.Owner,
		Action: action, Target: commandTarget(cmd.Kind, args),
		Method: "LINK", Path: basePath + "/commands",
		Status: httpStatus, Success: status == resultSucceeded,
		Detail: audit.Detail(detail),
	})
}

func commandTarget(kind string, a Args) string {
	switch kind {
	case KindContainerStart, KindContainerStop, KindContainerReboot:
		return a.Container
	case KindStackRestart:
		return a.Name
	case KindBackupRun:
		if a.JobID > 0 {
			return fmt.Sprintf("job:%d", a.JobID)
		}
	case KindUpdateInstall:
		return a.Version
	case KindUpdatePolicy:
		return a.Policy.Auto
	}
	return ""
}
