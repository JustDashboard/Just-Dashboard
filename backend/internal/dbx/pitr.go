package dbx

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Point-in-time recovery for PostgreSQL.
//
// A dump is the database as it was at one moment; the moments between two
// dumps are lost. A server that hands every finished segment of its
// write-ahead log to an archive, beside a base backup taken now and then, can
// be brought back as it was at any moment the archive covers: the base backup
// is unpacked and the log replayed on top of it up to the moment asked for.
// WAL-G does both halves, into the same destinations a backup job writes to,
// and encrypts what it writes there.
//
// This file is what that takes from the engine and from WAL-G's own words: the
// settings a server archives with, the configuration WAL-G reads, what its
// listings say, which moments an archive covers, which base backup a recovery
// starts from and what retention may delete. Running WAL-G is the API
// package's, and always happens inside the server's own container as the
// server's own account — never in this process — so the archive is written
// with the permissions the server reads it back with.

const (
	// WALGMount is where the tool and its configuration are mounted in the
	// server's container, read-only.
	WALGMount = "/jd-pitr"
	// WALGArchiveMount is where a destination that is a directory on this
	// server is mounted.
	WALGArchiveMount = "/jd-pitr-archive"
	// WALGCommand is what the server runs to archive a segment and to fetch
	// one back. It is a wrapper that starts WAL-G with the C library it was
	// built against and the configuration beside it.
	WALGCommand = WALGMount + "/wal-g"
	// PostgresDataDir is where the official image keeps the cluster.
	PostgresDataDir = "/var/lib/postgresql/data"
	// WALArchiveTimeout bounds how far behind the archive can fall on a
	// server that writes little: a segment is handed over at least this
	// often whenever anything was written into it.
	WALArchiveTimeout = 60
)

// The settings a server archives with. They are given on the server's command
// line rather than with ALTER SYSTEM: a base backup carries
// postgresql.auto.conf with it, and a copy restored from one would otherwise
// start archiving into the same place as the server it was restored from.
var (
	walArchiveSettings  = []string{"archive_mode", "archive_command", "archive_timeout"}
	walRecoverySettings = []string{
		"restore_command", "recovery_target", "recovery_target_time", "recovery_target_name",
		"recovery_target_lsn", "recovery_target_xid", "recovery_target_action",
		"recovery_target_inclusive", "recovery_target_timeline",
	}
)

// WALArchiveSettings is what a server is started with to archive into WAL-G.
// wal_level is left alone unless it is minimal, the one level that writes too
// little to recover from: a server running at logical has a replication slot
// or a subscriber relying on it.
func WALArchiveSettings(walLevel string) map[string]string {
	settings := map[string]string{
		"archive_mode":    "on",
		"archive_command": WALGCommand + " wal-push %p",
		"archive_timeout": strconv.Itoa(WALArchiveTimeout),
	}
	if strings.EqualFold(strings.TrimSpace(walLevel), "minimal") {
		settings["wal_level"] = "replica"
	}
	return settings
}

// WithoutWALSettings is a server's command with every archive and recovery
// setting this feature gives taken out, and nothing else changed.
func WithoutWALSettings(cmd []string) []string {
	return PostgresCommandSettings(cmd, nil, append(append([]string{}, walArchiveSettings...), walRecoverySettings...)...)
}

// ArchivesWithWALG reports whether a server's command line archives through
// this feature's wrapper.
func ArchivesWithWALG(cmd []string) bool {
	settings := ReadPostgresCommandSettings(cmd)
	return strings.EqualFold(settings["archive_mode"], "on") &&
		strings.HasPrefix(settings["archive_command"], WALGCommand+" ")
}

// IsPostgresServerCommand reports whether a container's command starts the
// server itself, the only kind whose settings this file can rewrite. The
// official image's entrypoint runs `postgres` for a command that begins with
// a flag.
func IsPostgresServerCommand(cmd []string) bool {
	if len(cmd) == 0 {
		return true
	}
	return cmd[0] == "postgres" || strings.HasPrefix(cmd[0], "-")
}

// ReadPostgresCommandSettings reads the `-c name=value` settings off a server
// command, in every spelling the server takes: `-c name=value`,
// `-cname=value` and `--name=value`. A later one wins, as it does for the
// server.
func ReadPostgresCommandSettings(cmd []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(cmd); i++ {
		setting, consumed := postgresSettingAt(cmd, i)
		if setting == "" {
			continue
		}
		name, value, _ := strings.Cut(setting, "=")
		out[strings.ToLower(strings.TrimSpace(name))] = value
		i += consumed
	}
	return out
}

// postgresSettingAt is the `name=value` an argument begins, and how many
// arguments after it belong to it.
func postgresSettingAt(cmd []string, i int) (string, int) {
	arg := cmd[i]
	switch {
	case arg == "-c" && i+1 < len(cmd):
		return cmd[i+1], 1
	case strings.HasPrefix(arg, "-c") && len(arg) > 2 && strings.Contains(arg, "="):
		return arg[2:], 0
	case strings.HasPrefix(arg, "--") && strings.Contains(arg, "="):
		// The server reads dashes in the name as underscores; the value is
		// the value.
		name, value, _ := strings.Cut(arg[2:], "=")
		return strings.ReplaceAll(name, "-", "_") + "=" + value, 0
	}
	return "", 0
}

// PostgresCommandSettings rewrites a server command: every spelling of a
// setting named in unset or set is taken out, and set is appended as `-c`
// flags in a fixed order. The other arguments — the operator's own flags, or
// another feature's — stay where they were.
func PostgresCommandSettings(cmd []string, set map[string]string, unset ...string) []string {
	drop := map[string]bool{}
	for _, name := range unset {
		drop[strings.ToLower(name)] = true
	}
	for name := range set {
		drop[strings.ToLower(name)] = true
	}
	out := []string{}
	if len(cmd) == 0 {
		out = append(out, "postgres")
	}
	for i := 0; i < len(cmd); i++ {
		setting, consumed := postgresSettingAt(cmd, i)
		if setting != "" {
			name, _, _ := strings.Cut(setting, "=")
			if drop[strings.ToLower(strings.TrimSpace(name))] {
				i += consumed
				continue
			}
		}
		out = append(out, cmd[i:i+1+consumed]...)
		i += consumed
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, "-c", name+"="+set[name])
	}
	return out
}

// WALStorage is a destination as WAL-G addresses it from inside the server's
// container. A directory on this server is mounted at WALGArchiveMount, so its
// path here is that mount's.
type WALStorage struct {
	Kind     string
	Bucket   string
	Region   string
	Endpoint string
	Prefix   string
	// AccessKeyID and SecretAccessKey are empty where the instance's own
	// role is meant, as they are for a backup job.
	AccessKeyID     string
	SecretAccessKey string
}

// WALGAccount is how WAL-G signs in to the server for a base backup: over its
// unix socket, as the account the image was initialised with.
type WALGAccount struct {
	User     string
	Password string
	Port     int
}

// WALGConfig is the configuration file WAL-G reads, by the names of the
// environment variables it would otherwise take. key is the armoured OpenPGP
// key: the public half for a server that archives, which is all encrypting
// needs, and the private half only for a recovery.
func WALGConfig(storage WALStorage, stream, key string, account WALGAccount) (map[string]string, error) {
	if !validWALStream(stream) {
		return nil, fmt.Errorf("%q is not an archive name", stream)
	}
	port := account.Port
	if port <= 0 {
		port = 5432
	}
	user := account.User
	if user == "" {
		user = "postgres"
	}
	cfg := map[string]string{
		// lz4 is WAL-G's own default; written down so a later default does
		// not change how a running archive is written.
		"WALG_COMPRESSION_METHOD": "lz4",
		// Two servers restored from one archive could otherwise write the
		// same segment name with different contents over each other.
		"WALG_PREVENT_WAL_OVERWRITE": "true",
		"PGHOST":                     "/var/run/postgresql",
		"PGPORT":                     strconv.Itoa(port),
		"PGUSER":                     user,
		"PGDATABASE":                 "postgres",
	}
	if account.Password != "" {
		cfg["PGPASSWORD"] = account.Password
	}
	if key != "" {
		cfg["WALG_PGP_KEY"] = key
	}
	switch storage.Kind {
	case "local":
		cfg["WALG_FILE_PREFIX"] = WALGArchiveMount
	case "s3", "b2":
		bucket := strings.Trim(strings.TrimSpace(storage.Bucket), "/")
		if bucket == "" {
			return nil, errors.New("an object storage destination needs a bucket")
		}
		cfg["WALG_S3_PREFIX"] = "s3://" + path.Join(bucket, strings.Trim(storage.Prefix, "/"), stream)
		region := strings.TrimSpace(storage.Region)
		if region == "" {
			region = "us-east-1"
		}
		cfg["AWS_REGION"] = region
		if endpoint := strings.TrimSpace(storage.Endpoint); endpoint != "" {
			if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
				endpoint = "https://" + endpoint
			}
			cfg["AWS_ENDPOINT"] = endpoint
			// Backblaze and MinIO answer at one host for every bucket.
			cfg["AWS_S3_FORCE_PATH_STYLE"] = "true"
		}
		if storage.AccessKeyID != "" {
			cfg["AWS_ACCESS_KEY_ID"] = storage.AccessKeyID
			cfg["AWS_SECRET_ACCESS_KEY"] = storage.SecretAccessKey
		}
	default:
		return nil, fmt.Errorf("destination must be one of local, s3, b2")
	}
	return cfg, nil
}

var walStreamRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func validWALStream(stream string) bool {
	return walStreamRe.MatchString(stream) && !strings.Contains(stream, "..")
}

// WALStreamName is the folder one server's archive is kept in inside its
// destination. A server set up again later, or the copy that replaced it,
// archives into a folder of its own rather than into one whose timeline it
// does not share.
func WALStreamName(container string, at time.Time) string {
	return container + "-" + at.UTC().Format("20060102-150405")
}

// WALBackup is one base backup, as WAL-G's listing describes it.
type WALBackup struct {
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	// WALFile is the segment the backup begins in.
	WALFile   string `json:"walFile"`
	PGVersion int    `json:"pgVersion"`
	// Size is what the destination holds for it, compressed and encrypted;
	// DataSize is the cluster it unpacks to.
	Size      int64 `json:"size"`
	DataSize  int64 `json:"dataSize"`
	Permanent bool  `json:"permanent,omitempty"`
}

// ParseWALGBackups reads `wal-g backup-list --json --detail`, oldest first.
func ParseWALGBackups(out []byte) ([]WALBackup, error) {
	text := strings.TrimSpace(string(out))
	if text == "" || strings.Contains(text, "No backups found") {
		return []WALBackup{}, nil
	}
	var raw []struct {
		Name         string    `json:"backup_name"`
		Time         time.Time `json:"time"`
		WALFile      string    `json:"wal_file_name"`
		StartTime    time.Time `json:"start_time"`
		FinishTime   time.Time `json:"finish_time"`
		PGVersion    int       `json:"pg_version"`
		Permanent    bool      `json:"is_permanent"`
		Uncompressed int64     `json:"uncompressed_size"`
		Compressed   int64     `json:"compressed_size"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("WAL-G's list of base backups could not be read: %w", err)
	}
	backups := make([]WALBackup, 0, len(raw))
	for _, b := range raw {
		finished := b.FinishTime
		if finished.IsZero() {
			finished = b.Time
		}
		backups = append(backups, WALBackup{
			Name: b.Name, StartedAt: b.StartTime.UTC(), FinishedAt: finished.UTC(), WALFile: b.WALFile,
			PGVersion: b.PGVersion, Size: b.Compressed, DataSize: b.Uncompressed, Permanent: b.Permanent,
		})
	}
	sort.SliceStable(backups, func(i, j int) bool { return backups[i].FinishedAt.Before(backups[j].FinishedAt) })
	return backups, nil
}

// WALObject is one entry of `wal-g st ls`.
type WALObject struct {
	Name     string
	Size     int64
	Modified time.Time
}

// ParseWALGObjects reads the table `wal-g st ls` prints: kind, size, the
// modification time in Go's default layout, and the name. Folders and the
// header are left out.
func ParseWALGObjects(out []byte) []WALObject {
	objects := []WALObject{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 7 || fields[0] != "obj" {
			continue
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		modified, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", strings.Join(fields[2:6], " "))
		if err != nil {
			continue
		}
		objects = append(objects, WALObject{Name: strings.Join(fields[6:], " "), Size: size, Modified: modified.UTC()})
	}
	return objects
}

// walSegmentRe is a segment of the log, as WAL-G names the object it keeps
// for one: the 24 hexadecimal digits and the compression's extension.
var walSegmentRe = regexp.MustCompile(`^[0-9A-F]{24}(\.[a-z0-9]+)?$`)

// LatestWALSegment is the newest whole segment among an archive's objects.
// History files, backup labels and partial segments are not log a recovery
// replays to the end of.
func LatestWALSegment(objects []WALObject) (WALObject, bool) {
	var newest WALObject
	found := false
	for _, object := range objects {
		if !walSegmentRe.MatchString(object.Name) {
			continue
		}
		if !found || object.Modified.After(newest.Modified) {
			newest, found = object, true
		}
	}
	return newest, found
}

// WALWindow is the stretch of time an archive can recover to.
type WALWindow struct {
	// From is the end of the oldest base backup: no moment before a backup
	// finished can be recovered from it.
	From *time.Time `json:"from,omitempty"`
	// To is the last segment the archive holds was archived, which is the
	// latest moment it has the log for.
	To *time.Time `json:"to,omitempty"`
}

// RecoverableWindow is what an archive with these base backups and this
// newest segment covers. No base backup is no window at all, whatever log
// there is: the log is replayed on top of a backup.
func RecoverableWindow(backups []WALBackup, latest time.Time) WALWindow {
	if len(backups) == 0 {
		return WALWindow{}
	}
	from := backups[0].FinishedAt
	for _, b := range backups[1:] {
		if b.FinishedAt.Before(from) {
			from = b.FinishedAt
		}
	}
	to := latest
	for _, b := range backups {
		if b.FinishedAt.After(to) {
			to = b.FinishedAt
		}
	}
	return WALWindow{From: &from, To: &to}
}

// BaseBackupFor is the base backup a recovery to target starts from: the
// newest one that had finished by then, which leaves the least log to replay.
// A zero target is the latest moment, which the newest backup starts.
func BaseBackupFor(backups []WALBackup, target time.Time) (WALBackup, error) {
	var chosen *WALBackup
	for i := range backups {
		b := &backups[i]
		if !target.IsZero() && b.FinishedAt.After(target) {
			continue
		}
		if chosen == nil || b.FinishedAt.After(chosen.FinishedAt) {
			chosen = b
		}
	}
	if chosen == nil {
		if len(backups) == 0 {
			return WALBackup{}, errors.New("the archive holds no base backup yet; take one before recovering")
		}
		return WALBackup{}, fmt.Errorf("the oldest base backup finished at %s, after the moment asked for; nothing earlier can be recovered",
			backups[0].FinishedAt.UTC().Format(time.RFC3339))
	}
	return *chosen, nil
}

// WALRetentionKeep is the oldest base backup retention must keep so that every
// moment of the last `days` stays recoverable: the newest one finished before
// that stretch began, because the moments at its start are replayed from it.
// Everything older, and the log before it, may go. ok is false where nothing
// may: no limit, too few backups, or none old enough to start the stretch.
func WALRetentionKeep(backups []WALBackup, now time.Time, days int) (WALBackup, bool) {
	if days <= 0 || len(backups) < 2 {
		return WALBackup{}, false
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	keep, err := BaseBackupFor(backups, cutoff)
	if err != nil {
		return WALBackup{}, false
	}
	for _, b := range backups {
		if b.FinishedAt.Before(keep.FinishedAt) {
			return keep, true
		}
	}
	return WALBackup{}, false
}

// RecoveryTarget is the moment a copy is recovered to.
type RecoveryTarget struct {
	// Kind is "latest", "time" or "name".
	Kind string    `json:"kind"`
	Time time.Time `json:"time,omitempty"`
	// Name is a restore point made with pg_create_restore_point.
	Name string `json:"name,omitempty"`
}

var restorePointRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,62}$`)

// ValidRestorePointName holds a restore point's name to what is unambiguous
// on a command line and in a log: the server takes up to 63 bytes of anything.
func ValidRestorePointName(name string) error {
	if !restorePointRe.MatchString(name) {
		return errors.New("a restore point's name is 1 to 63 letters, digits and . _ : -, starting with a letter or digit")
	}
	return nil
}

// Validate refuses a target the server would only refuse later, halfway
// through a recovery.
func (t RecoveryTarget) Validate() error {
	switch t.Kind {
	case "latest":
		return nil
	case "time":
		if t.Time.IsZero() {
			return errors.New("a recovery to a moment needs the moment")
		}
		return nil
	case "name":
		return ValidRestorePointName(t.Name)
	}
	return errors.New("a recovery target is latest, a time or a restore point's name")
}

// RecoverySettings is what a copy is started with to recover: where its log
// comes from, where to stop, and to become a server of its own once it gets
// there rather than wait for more. The latest moment names no target: the
// server replays all the log there is and then promotes.
func (t RecoveryTarget) RecoverySettings() map[string]string {
	settings := map[string]string{
		"restore_command": WALGCommand + " wal-fetch %f %p",
		// A copy writes nothing anywhere until it is somebody's to keep.
		"archive_mode": "off",
	}
	switch t.Kind {
	case "time":
		settings["recovery_target_time"] = t.Time.UTC().Format("2006-01-02 15:04:05.999999") + "+00"
		settings["recovery_target_action"] = "promote"
	case "name":
		settings["recovery_target_name"] = t.Name
		settings["recovery_target_action"] = "promote"
	}
	return settings
}

// WALArchiver is what the server itself says about its archiving, from
// pg_stat_archiver and its settings.
type WALArchiver struct {
	ArchiveMode    string     `json:"archiveMode"`
	WALLevel       string     `json:"walLevel"`
	ArchivedCount  int64      `json:"archivedCount"`
	LastArchived   string     `json:"lastArchived,omitempty"`
	LastArchivedAt *time.Time `json:"lastArchivedAt,omitempty"`
	FailedCount    int64      `json:"failedCount"`
	LastFailed     string     `json:"lastFailed,omitempty"`
	LastFailedAt   *time.Time `json:"lastFailedAt,omitempty"`
	// Current is the segment being written now, the next one to be handed
	// over.
	Current string `json:"current,omitempty"`
	// Waiting is how many finished segments the archiver has not handed over
	// yet. Nil where the account may not list the server's own directory.
	Waiting *int `json:"waiting,omitempty"`
	// Version is server_version_num.
	Version    int  `json:"version"`
	InRecovery bool `json:"inRecovery"`
}

// Failing reports a server whose last attempt to archive failed: one that
// failed and has not succeeded since.
func (a *WALArchiver) Failing() bool {
	if a == nil || a.LastFailedAt == nil {
		return false
	}
	return a.LastArchivedAt == nil || a.LastFailedAt.After(*a.LastArchivedAt)
}

// ReadWALArchiver reads a server's archiving as the server reports it.
func ReadWALArchiver(ctx context.Context, db *sql.DB) (*WALArchiver, error) {
	out := &WALArchiver{}
	var (
		lastArchived, lastFailed     sql.NullString
		lastArchivedAt, lastFailedAt sql.NullTime
	)
	err := db.QueryRowContext(ctx, `SELECT current_setting('archive_mode'), current_setting('wal_level'),
		current_setting('server_version_num')::int, pg_is_in_recovery(),
		archived_count, last_archived_wal, last_archived_time, failed_count, last_failed_wal, last_failed_time
		FROM pg_stat_archiver`).Scan(&out.ArchiveMode, &out.WALLevel, &out.Version, &out.InRecovery,
		&out.ArchivedCount, &lastArchived, &lastArchivedAt, &out.FailedCount, &lastFailed, &lastFailedAt)
	if err != nil {
		return nil, err
	}
	out.LastArchived, out.LastFailed = lastArchived.String, lastFailed.String
	if lastArchivedAt.Valid {
		at := lastArchivedAt.Time.UTC()
		out.LastArchivedAt = &at
	}
	if lastFailedAt.Valid {
		at := lastFailedAt.Time.UTC()
		out.LastFailedAt = &at
	}
	if !out.InRecovery {
		var current sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT pg_walfile_name(pg_current_wal_lsn())`).Scan(&current); err == nil {
			out.Current = current.String
		}
	}
	// The archiver's own queue. Listing it needs pg_monitor or a superuser;
	// an account without either gets no number rather than a wrong one.
	var waiting int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_ls_archive_statusdir() WHERE name LIKE '%.ready'`).Scan(&waiting); err == nil {
		out.Waiting = &waiting
	}
	return out, nil
}

// CreateRestorePoint names the present moment in the log, so that a recovery
// can be asked to stop there, and closes the segment it is in so the name
// reaches the archive now rather than when the segment fills.
func CreateRestorePoint(ctx context.Context, db *sql.DB, name string) (string, error) {
	if err := ValidRestorePointName(name); err != nil {
		return "", err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	var lsn string
	if err := conn.QueryRowContext(ctx, `SELECT pg_create_restore_point($1)::text`, name).Scan(&lsn); err != nil {
		return "", err
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_switch_wal()`); err != nil {
		return lsn, err
	}
	return lsn, nil
}

// LastArchiveError is what the server's log last said about a segment it
// could not archive: WAL-G's own error, which names the cause — a refused
// key, a bucket that does not exist — where pg_stat_archiver says only that
// it failed.
func LastArchiveError(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], "archive command failed") {
			continue
		}
		for j := i - 1; j >= 0 && j >= i-20; j-- {
			if _, message, ok := strings.Cut(lines[j], "ERROR: "); ok {
				// WAL-G stamps each line with a date and a time before the text.
				fields := strings.SplitN(strings.TrimSpace(message), " ", 3)
				if len(fields) == 3 && strings.Count(fields[0], "/") == 2 {
					return strings.TrimSpace(fields[2])
				}
				return strings.TrimSpace(message)
			}
		}
		return strings.TrimSpace(lines[i])
	}
	return ""
}

// RecoveryFailure is the line of a copy's log that says its recovery cannot
// finish, where there is one: a moment past the end of the log, a restore
// point the log never reached.
func RecoveryFailure(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if _, message, ok := strings.Cut(lines[i], "FATAL:"); ok {
			return strings.TrimSpace(message)
		}
	}
	return ""
}

var recoveryStopRe = regexp.MustCompile(`(?:last completed transaction was at log time|recovery stopping (?:before|after) commit of transaction \d+, time) (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?[+-]\d{2}(?::?\d{2})?)`)

// RecoveredTo is the moment a copy's recovery stopped at, as its log says:
// the commit it stopped before or after, or the last one it replayed.
func RecoveredTo(lines []string) (time.Time, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		match := recoveryStopRe.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		for _, layout := range []string{"2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05.999999-0700"} {
			if at, err := time.Parse(layout, match[1]); err == nil {
				return at.UTC(), true
			}
		}
	}
	return time.Time{}, false
}
