package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// Entry is one immutable record of who did what, when, from where.
type Entry struct {
	ID       int64     `json:"id"`
	TS       time.Time `json:"ts"`
	UserID   int64     `json:"userId"`
	Username string    `json:"username"`
	Role     string    `json:"role"`
	IP       string    `json:"ip"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Target   string    `json:"target"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Status   int       `json:"status"`
	Success  bool      `json:"success"`
	Detail   string    `json:"detail"`
}

type Logger struct {
	st  *store.Store
	log *slog.Logger
}

func New(st *store.Store, log *slog.Logger) *Logger {
	return &Logger{st: st, log: log}
}

// Record writes the entry to the audit table and mirrors it to the process log,
// so an operator retains a trail even if the database is later tampered with.
func (l *Logger) Record(ctx context.Context, e Entry) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	if e.Actor == "" {
		e.Actor = "session"
	}
	success := 1
	if !e.Success {
		success = 0
	}
	if _, err := l.st.DB.ExecContext(ctx,
		`INSERT INTO audit_log(ts, user_id, username, role, ip, actor, action, target, method, path, status, success, detail)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.TS.Unix(), e.UserID, e.Username, e.Role, e.IP, e.Actor, e.Action, e.Target,
		e.Method, e.Path, e.Status, success, truncate(e.Detail, 4000)); err != nil {
		l.log.Error("audit write failed", "err", err, "action", e.Action)
	}
	l.log.Info("audit",
		"user", e.Username, "role", e.Role, "ip", e.IP, "actor", e.Actor,
		"action", e.Action, "target", e.Target, "status", e.Status, "success", e.Success)
}

type Filter struct {
	Username string
	Action   string
	// ActionPrefixes keeps entries whose action starts with any of these,
	// so one page can ask for a whole family ("proxy.", "certificates.")
	// without the substring match Action does catching unrelated names.
	ActionPrefixes []string
	Since          time.Time
	Until          time.Time
	OnlyFail       bool
	Limit          int
	Offset         int
}

func (l *Logger) List(ctx context.Context, f Filter) ([]Entry, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Username != "" {
		where = append(where, "username LIKE ?")
		args = append(args, "%"+f.Username+"%")
	}
	if f.Action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, "%"+f.Action+"%")
	}
	if prefixes := nonEmpty(f.ActionPrefixes); len(prefixes) > 0 {
		terms := make([]string, len(prefixes))
		for i, p := range prefixes {
			terms[i] = `action LIKE ? ESCAPE '\'`
			args = append(args, likeEscaper.Replace(p)+"%")
		}
		where = append(where, "("+strings.Join(terms, " OR ")+")")
	}
	if !f.Since.IsZero() {
		where = append(where, "ts >= ?")
		args = append(args, f.Since.Unix())
	}
	if !f.Until.IsZero() {
		where = append(where, "ts <= ?")
		args = append(args, f.Until.Unix())
	}
	if f.OnlyFail {
		where = append(where, "success = 0")
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := l.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := l.st.DB.QueryContext(ctx,
		`SELECT id, ts, user_id, username, role, ip, actor, action, target, method, path, status, success, detail
		 FROM audit_log WHERE `+clause+` ORDER BY ts DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var ts int64
		var success int
		if err := rows.Scan(&e.ID, &ts, &e.UserID, &e.Username, &e.Role, &e.IP, &e.Actor,
			&e.Action, &e.Target, &e.Method, &e.Path, &e.Status, &success, &e.Detail); err != nil {
			return nil, 0, err
		}
		e.TS = time.Unix(ts, 0).UTC()
		e.Success = success == 1
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// Detail renders structured context as compact JSON for the detail column,
// with anything that looks like a secret replaced.
//
// Callers are expected to build a purpose-made map and never hand over a
// request struct — but one of them did, and a password reset spent its life
// written in clear into the one table here that is not sealed. The redaction
// pass is the backstop, not the rule: it is cheap because audit writes are
// rare, and it means the next handler that forgets cannot leak anything worse
// than a field name.
func Detail(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return string(b)
	}
	redacted, err := json.Marshal(redact(decoded))
	if err != nil {
		return string(b)
	}
	return string(redacted)
}

// secretish are substrings of a field name that mean "never write the value".
// Bare "key" is absent on purpose: deploy.env.set logs the *name* of the
// variable it set, and that is the useful half of the record.
var secretish = []string{
	"password", "passwd", "secret", "token", "credential",
	"passphrase", "dsn", "apikey", "api_key", "privatekey", "private_key",
}

func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if isSecretField(k) {
				out[k] = "[redacted]"
				continue
			}
			out[k] = redact(val)
		}
		return out
	case []any:
		for i, val := range t {
			t[i] = redact(val)
		}
		return t
	default:
		return v
	}
}

func isSecretField(name string) bool {
	lower := strings.ToLower(name)
	for _, needle := range secretish {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// likeEscaper keeps a prefix literal: an underscore in an action name would
// otherwise match any character.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func nonEmpty(values []string) []string {
	out := values[:0:0]
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
