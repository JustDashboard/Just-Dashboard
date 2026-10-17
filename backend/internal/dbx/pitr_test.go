package dbx

import (
	"archive/tar"
	"bytes"
	"debug/elf"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPostgresCommandSettingsKeepEveryOtherArgument(t *testing.T) {
	cmd := []string{
		"postgres", "-c", "shared_preload_libraries=pg_stat_statements",
		"-carchive_mode=off", "--archive-timeout=300", "-c", "archive_command=cp %p /x/%f", "-k", "/tmp",
	}
	got := PostgresCommandSettings(cmd, WALArchiveSettings("replica"))
	want := []string{
		"postgres", "-c", "shared_preload_libraries=pg_stat_statements", "-k", "/tmp",
		"-c", "archive_command=/jd-pitr/wal-g wal-push %p", "-c", "archive_mode=on", "-c", "archive_timeout=60",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rewritten command:\n got %q\nwant %q", got, want)
	}
	if !ArchivesWithWALG(got) {
		t.Error("the rewritten command does not read as archiving through WAL-G")
	}
	if ArchivesWithWALG(cmd) {
		t.Error("a server archiving with cp read as archiving through WAL-G")
	}
	back := WithoutWALSettings(got)
	if !reflect.DeepEqual(back, []string{"postgres", "-c", "shared_preload_libraries=pg_stat_statements", "-k", "/tmp"}) {
		t.Errorf("taking the settings out again left %q", back)
	}
	// An image that runs its own default command names none.
	if got := PostgresCommandSettings(nil, map[string]string{"archive_mode": "on"}); !reflect.DeepEqual(got, []string{"postgres", "-c", "archive_mode=on"}) {
		t.Errorf("an empty command became %q", got)
	}
}

func TestWALArchiveSettingsRaiseOnlyAMinimalLevel(t *testing.T) {
	if _, ok := WALArchiveSettings("logical")["wal_level"]; ok {
		t.Error("a server at wal_level=logical would be lowered to replica, breaking its subscribers")
	}
	if WALArchiveSettings("minimal")["wal_level"] != "replica" {
		t.Error("a server at wal_level=minimal writes too little to recover from and was left there")
	}
}

func TestReadPostgresCommandSettingsLastOneWins(t *testing.T) {
	got := ReadPostgresCommandSettings([]string{"postgres", "-c", "port=5433", "--port=5434", "-c", "Archive_Mode=on",
		"--archive-command=/jd-pitr/wal-g wal-push %p"})
	if got["port"] != "5434" || got["archive_mode"] != "on" || got["archive_command"] != "/jd-pitr/wal-g wal-push %p" {
		t.Errorf("settings read as %v", got)
	}
	if !IsPostgresServerCommand([]string{"-c", "port=1"}) || !IsPostgresServerCommand(nil) ||
		IsPostgresServerCommand([]string{"sh", "-c", "exec postgres"}) {
		t.Error("which commands start the server itself is misjudged")
	}
}

func TestWALGConfigForEachDestination(t *testing.T) {
	account := WALGAccount{User: "jd", Password: "secret"}
	local, err := WALGConfig(WALStorage{Kind: "local"}, "jd-postgres-20261017-000000", "KEY", account)
	if err != nil {
		t.Fatal(err)
	}
	if local["WALG_FILE_PREFIX"] != WALGArchiveMount || local["WALG_PGP_KEY"] != "KEY" ||
		local["PGUSER"] != "jd" || local["PGPASSWORD"] != "secret" || local["PGPORT"] != "5432" {
		t.Errorf("local configuration: %v", local)
	}
	if _, set := local["WALG_S3_PREFIX"]; set {
		t.Error("a local destination was given an object store")
	}
	b2, err := WALGConfig(WALStorage{
		Kind: "b2", Bucket: "/backups/", Prefix: "/servers/", Endpoint: "s3.us-west-004.backblazeb2.com",
		AccessKeyID: "id", SecretAccessKey: "key",
	}, "jd-postgres-1", "", account)
	if err != nil {
		t.Fatal(err)
	}
	if b2["WALG_S3_PREFIX"] != "s3://backups/servers/jd-postgres-1" ||
		b2["AWS_ENDPOINT"] != "https://s3.us-west-004.backblazeb2.com" || b2["AWS_S3_FORCE_PATH_STYLE"] != "true" ||
		b2["AWS_REGION"] != "us-east-1" || b2["AWS_ACCESS_KEY_ID"] != "id" || b2["AWS_SECRET_ACCESS_KEY"] != "key" {
		t.Errorf("B2 configuration: %v", b2)
	}
	if _, set := b2["WALG_PGP_KEY"]; set {
		t.Error("an empty key was written as one")
	}
	aws, err := WALGConfig(WALStorage{Kind: "s3", Bucket: "b", Region: "eu-west-1"}, "s", "", WALGAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if aws["WALG_S3_PREFIX"] != "s3://b/s" || aws["AWS_REGION"] != "eu-west-1" || aws["PGUSER"] != "postgres" {
		t.Errorf("AWS configuration: %v", aws)
	}
	if _, set := aws["AWS_ENDPOINT"]; set {
		t.Error("AWS was given an endpoint of its own")
	}
	if _, set := aws["AWS_ACCESS_KEY_ID"]; set {
		t.Error("no keys must mean the instance role, not empty keys")
	}
	for _, stream := range []string{"", "../x", "a/b", "-x"} {
		if _, err := WALGConfig(WALStorage{Kind: "local"}, stream, "", account); err == nil {
			t.Errorf("%q was taken for an archive name", stream)
		}
	}
	if _, err := WALGConfig(WALStorage{Kind: "s3"}, "s", "", account); err == nil {
		t.Error("an object store with no bucket was accepted")
	}
	if _, err := WALGConfig(WALStorage{Kind: "ftp"}, "s", "", account); err == nil {
		t.Error("an unknown destination was accepted")
	}
}

// What WAL-G v3.0.9 printed for a real archive.
const walgBackupList = `[{"backup_name":"base_000000010000000000000003","time":"2026-10-17T00:52:07.298468866Z","wal_file_name":"000000010000000000000003","storage_name":"default","start_time":"2026-10-17T00:52:05.771523Z","finish_time":"2026-10-17T00:52:07.29231Z","date_fmt":"%Y-%m-%dT%H:%M:%S.%fZ","hostname":"32d5b5f9b512","data_dir":"/var/lib/postgresql/data","pg_version":170011,"start_lsn":50331688,"finish_lsn":50331936,"is_permanent":false,"system_identifier":7697433087718391842,"uncompressed_size":31153856,"compressed_size":5729480},{"backup_name":"base_000000010000000000000001","time":"2026-10-16T00:52:07Z","wal_file_name":"000000010000000000000001","start_time":"2026-10-16T00:52:05Z","finish_time":"2026-10-16T00:52:06Z","pg_version":170011,"is_permanent":true,"uncompressed_size":100,"compressed_size":10}]`

const walgObjects = `type size    last modified                           name
obj  3262328 2026-10-17 00:51:41.905060293 +0000 UTC 000000010000000000000001.lz4
obj  112105  2026-10-17 00:52:05.8824461 +0000 UTC   000000010000000000000002.lz4
obj  714     2026-10-17 00:52:06.272452371 +0000 UTC 000000010000000000000003.00000028.backup.lz4
obj  66586   2026-10-17 00:52:06.287452612 +0000 UTC 000000010000000000000003.lz4
obj  72037   2026-10-17 00:52:09.760508444 +0000 UTC 000000010000000000000004.lz4
obj  58      2026-10-17 00:53:09.000000000 +0000 UTC 00000002.history.lz4
dir  0       0001-01-01 00:00:00 +0000 UTC basebackups_005/
`

func TestParseWALGListings(t *testing.T) {
	backups, err := ParseWALGBackups([]byte(walgBackupList))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 || backups[0].Name != "base_000000010000000000000001" || !backups[0].Permanent {
		t.Fatalf("backups read as %+v", backups)
	}
	newest := backups[1]
	if newest.PGVersion != 170011 || newest.Size != 5729480 || newest.DataSize != 31153856 ||
		newest.WALFile != "000000010000000000000003" ||
		!newest.FinishedAt.Equal(time.Date(2026, 10, 17, 0, 52, 7, 292310000, time.UTC)) {
		t.Errorf("the newest backup read as %+v", newest)
	}
	for _, empty := range []string{"", "[]", "INFO: No backups found\n"} {
		if got, err := ParseWALGBackups([]byte(empty)); err != nil || len(got) != 0 || got == nil {
			t.Errorf("%q read as %v, %v", empty, got, err)
		}
	}
	if _, err := ParseWALGBackups([]byte("ERROR: no such bucket")); err == nil {
		t.Error("an error message read as a list")
	}

	objects := ParseWALGObjects([]byte(walgObjects))
	if len(objects) != 6 || objects[1].Size != 112105 || objects[1].Name != "000000010000000000000002.lz4" {
		t.Fatalf("objects read as %+v", objects)
	}
	latest, ok := LatestWALSegment(objects)
	if !ok || latest.Name != "000000010000000000000004.lz4" ||
		!latest.Modified.Equal(time.Date(2026, 10, 17, 0, 52, 9, 760508444, time.UTC)) {
		t.Errorf("the newest segment is %+v (the history file is not one)", latest)
	}
	if _, ok := LatestWALSegment(ParseWALGObjects([]byte("type size last modified name\n"))); ok {
		t.Error("an empty folder has a newest segment")
	}
}

func TestRecoverableWindowAndTheBackupARecoveryStartsFrom(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 3, 0, 0, 0, time.UTC) }
	backups := []WALBackup{{Name: "a", FinishedAt: day(10)}, {Name: "b", FinishedAt: day(12)}, {Name: "c", FinishedAt: day(14)}}
	window := RecoverableWindow(backups, day(15))
	if window.From == nil || !window.From.Equal(day(10)) || window.To == nil || !window.To.Equal(day(15)) {
		t.Errorf("window %+v", window)
	}
	if w := RecoverableWindow(nil, day(15)); w.From != nil || w.To != nil {
		t.Error("an archive with no base backup claimed a window")
	}
	// A backup newer than the last segment still covers its own end.
	if w := RecoverableWindow(backups, day(1)); !w.To.Equal(day(14)) {
		t.Errorf("window ends %v", w.To)
	}

	for target, want := range map[time.Time]string{
		day(11): "a", day(12): "b", day(13).Add(time.Hour): "b", day(20): "c", {}: "c",
	} {
		got, err := BaseBackupFor(backups, target)
		if err != nil || got.Name != want {
			t.Errorf("recovery to %v starts from %q (%v), want %q", target, got.Name, err, want)
		}
	}
	if _, err := BaseBackupFor(backups, day(9)); err == nil || !strings.Contains(err.Error(), "after the moment") {
		t.Errorf("a moment before every backup: %v", err)
	}
	if _, err := BaseBackupFor(nil, time.Time{}); err == nil {
		t.Error("an empty archive offered a backup")
	}
}

func TestWALRetentionKeepsTheBackupTheWindowStartsFrom(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ago := func(days float64) time.Time { return now.Add(-time.Duration(days * float64(24*time.Hour))) }
	backups := []WALBackup{
		{Name: "d10", FinishedAt: ago(10)}, {Name: "d8", FinishedAt: ago(8)},
		{Name: "d6", FinishedAt: ago(6)}, {Name: "d1", FinishedAt: ago(1)},
	}
	keep, ok := WALRetentionKeep(backups, now, 7)
	if !ok || keep.Name != "d8" {
		t.Fatalf("keeping 7 days keeps from %q (%v); the backup 8 days old starts the 7th day", keep.Name, ok)
	}
	for _, c := range []struct {
		why     string
		backups []WALBackup
		days    int
	}{
		{"no limit", backups, 0},
		{"one backup", backups[:1], 1},
		{"every backup is inside the window", []WALBackup{{Name: "x", FinishedAt: ago(2)}, {Name: "y", FinishedAt: ago(1)}}, 7},
		{"the oldest is the one the window starts from", []WALBackup{{Name: "x", FinishedAt: ago(9)}, {Name: "y", FinishedAt: ago(1)}}, 7},
	} {
		if keep, ok := WALRetentionKeep(c.backups, now, c.days); ok {
			t.Errorf("%s: retention would delete before %q", c.why, keep.Name)
		}
	}
}

func TestRecoveryTargets(t *testing.T) {
	at := time.Date(2026, 10, 17, 0, 52, 8, 484022000, time.UTC)
	for _, c := range []struct {
		target RecoveryTarget
		want   map[string]string
	}{
		{RecoveryTarget{Kind: "latest"}, map[string]string{}},
		{RecoveryTarget{Kind: "time", Time: at.In(time.FixedZone("x", 3*3600))}, map[string]string{
			"recovery_target_time": "2026-10-17 00:52:08.484022+00", "recovery_target_action": "promote",
		}},
		{RecoveryTarget{Kind: "name", Name: "before-migration"}, map[string]string{
			"recovery_target_name": "before-migration", "recovery_target_action": "promote",
		}},
	} {
		if err := c.target.Validate(); err != nil {
			t.Errorf("%+v: %v", c.target, err)
		}
		got := c.target.RecoverySettings()
		if got["restore_command"] != "/jd-pitr/wal-g wal-fetch %f %p" || got["archive_mode"] != "off" {
			t.Errorf("%s: %v", c.target.Kind, got)
		}
		for name, value := range c.want {
			if got[name] != value {
				t.Errorf("%s: %s = %q, want %q", c.target.Kind, name, got[name], value)
			}
		}
		if len(got) != len(c.want)+2 {
			t.Errorf("%s: %v carries settings it was not asked for", c.target.Kind, got)
		}
	}
	for _, bad := range []RecoveryTarget{
		{Kind: "time"}, {Kind: "name", Name: "two words"}, {Kind: "name", Name: "-x"}, {Kind: "soon"},
		{Kind: "name", Name: strings.Repeat("a", 64)},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	// Every setting a recovery gives is one WithoutWALSettings takes back out.
	cmd := PostgresCommandSettings([]string{"postgres"}, RecoveryTarget{Kind: "name", Name: "x"}.RecoverySettings())
	if got := WithoutWALSettings(cmd); !reflect.DeepEqual(got, []string{"postgres"}) {
		t.Errorf("after recovery the command is still %q", got)
	}
}

func TestWhatAServersLogSaysAboutArchivingAndRecovery(t *testing.T) {
	archiving := strings.Split(`2026-10-17 01:00:00.000 UTC [40] LOG:  checkpoint starting: time
ERROR: 2026/10/17 01:00:01.123456 failed to upload 'pg_wal/000000010000000000000007' to bucket 'shop': AccessDenied: Access Denied
	status code: 403, request id: 1
2026-10-17 01:00:01.200 UTC [41] LOG:  archive command failed with exit code 1
2026-10-17 01:00:01.200 UTC [41] DETAIL:  The failed archive command was: /jd-pitr/wal-g wal-push pg_wal/000000010000000000000007`, "\n")
	if got := LastArchiveError(archiving); got != "failed to upload 'pg_wal/000000010000000000000007' to bucket 'shop': AccessDenied: Access Denied" {
		t.Errorf("archive error read as %q", got)
	}
	if got := LastArchiveError([]string{"LOG:  database system is ready"}); got != "" {
		t.Errorf("a quiet log read as %q", got)
	}

	recovery := strings.Split(`2026-10-17 00:52:59.027 UTC [30] LOG:  recovery stopping before commit of transaction 742, time 2026-10-17 00:52:09.591828+00
2026-10-17 00:52:59.027 UTC [30] LOG:  redo done at 0/4004A28 system usage: CPU: user: 0.00 s
2026-10-17 00:52:59.027 UTC [30] LOG:  last completed transaction was at log time 2026-10-17 00:52:07.393671+00
2026-10-17 00:52:59.276 UTC [30] LOG:  archive recovery complete`, "\n")
	got, ok := RecoveredTo(recovery)
	if !ok || !got.Equal(time.Date(2026, 10, 17, 0, 52, 7, 393671000, time.UTC)) {
		t.Errorf("recovered to %v (%v)", got, ok)
	}
	if _, ok := RecoveredTo([]string{"LOG:  starting PostgreSQL 17"}); ok {
		t.Error("a log that says nothing about where it stopped named a moment")
	}
	failed := []string{
		"2026-10-17 00:52:59.027 UTC [30] LOG:  redo done at 0/4004A28",
		"2026-10-17 00:52:59.027 UTC [30] FATAL:  recovery ended before configured recovery target was reached",
	}
	if got := RecoveryFailure(failed); got != "recovery ended before configured recovery target was reached" {
		t.Errorf("failure read as %q", got)
	}
}

func TestWALKeysEncryptWithThePublicHalfAlone(t *testing.T) {
	public, private, err := NewWALKey("jd-postgres")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(public, "-----BEGIN PGP PUBLIC KEY BLOCK-----") ||
		!strings.HasPrefix(private, "-----BEGIN PGP PRIVATE KEY BLOCK-----") {
		t.Fatal("the halves are not armoured keys")
	}
	if strings.Contains(public, "PRIVATE") {
		t.Fatal("the public half carries private material")
	}
	fingerprint := WALKeyFingerprint(public)
	if len(fingerprint) != 49 || WALKeyFingerprint(private) != fingerprint {
		t.Errorf("fingerprint %q", fingerprint)
	}
	if WALKeyFingerprint("not a key") != "" {
		t.Error("a fingerprint was read from nothing")
	}
}

func TestTheToolCarriesTheLoaderItWasLinkedWith(t *testing.T) {
	// Any dynamically linked program on this machine stands in for WAL-G.
	program := "/bin/true"
	f, err := elf.Open(program)
	if err != nil {
		t.Skipf("no %s to stand in for WAL-G: %v", program, err)
	}
	interpreter := elfInterpreter(f)
	f.Close()
	if interpreter == "" {
		t.Skipf("%s is linked statically here", program)
	}
	defer func(old string) { WALGBinary = old }(WALGBinary)
	WALGBinary = program
	tool, err := LoadWALGTool()
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := tool.WriteTar(&archive); err != nil {
		t.Fatal(err)
	}
	files := map[string]*tar.Header{}
	contents := map[string]string{}
	reader := tar.NewReader(&archive)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = header
		if header.Name == "wal-g" || header.Name == "lib/tool.sha256" {
			data, _ := io.ReadAll(reader)
			contents[header.Name] = string(data)
		}
	}
	for _, name := range []string{"wal-g", "lib/wal-g", "lib/ld.so", "lib/libc.so.6", "lib/tool.sha256"} {
		header := files[name]
		if header == nil {
			t.Errorf("the tool has no %s (it has %v)", name, keysOf(files))
			continue
		}
		if header.Uid != 0 || header.Mode&0o022 != 0 {
			t.Errorf("%s is writable by somebody other than root: uid %d mode %o", name, header.Uid, header.Mode)
		}
	}
	if !strings.Contains(contents["wal-g"], "--argv0 /jd-pitr/wal-g /jd-pitr/lib/wal-g") ||
		!strings.Contains(contents["wal-g"], "WALG_CONFIG_PATH=/jd-pitr/walg.json") {
		t.Errorf("the wrapper is:\n%s", contents["wal-g"])
	}
	if strings.TrimSpace(contents["lib/tool.sha256"]) != tool.Digest || len(tool.Digest) != 64 {
		t.Errorf("the tool names its build %q, its digest is %q", contents["lib/tool.sha256"], tool.Digest)
	}

	WALGBinary = "/nonexistent/wal-g"
	if _, err := LoadWALGTool(); err == nil || !strings.Contains(err.Error(), "no WAL-G") {
		t.Errorf("a dashboard without WAL-G said %v", err)
	}
}

func TestTheConfigurationIsTheServersAlone(t *testing.T) {
	var archive bytes.Buffer
	if err := WriteWALGConfigTar(&archive, map[string]string{"AWS_SECRET_ACCESS_KEY": "s"}, 70, 70); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&archive)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "walg.json" || header.Uid != 70 || header.Gid != 70 || header.Mode != 0o600 {
		t.Errorf("configuration written as %s uid %d gid %d mode %o", header.Name, header.Uid, header.Gid, header.Mode)
	}
	var cfg map[string]string
	if err := json.NewDecoder(reader).Decode(&cfg); err != nil || cfg["AWS_SECRET_ACCESS_KEY"] != "s" {
		t.Errorf("configuration read back as %v (%v)", cfg, err)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Error("the archive holds more than the configuration")
	}
}

func keysOf(m map[string]*tar.Header) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestWALStreamNamesAreArchiveNames(t *testing.T) {
	name := WALStreamName("jd-postgres", time.Date(2026, 10, 17, 1, 2, 3, 0, time.FixedZone("x", 7200)))
	if name != "jd-postgres-20261016-230203" || !validWALStream(name) {
		t.Errorf("stream name %q", name)
	}
}
