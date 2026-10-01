package dbx

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The engines' own dump and restore tools.
//
// Every one of them is started through hostexec with an explicit argument
// vector — never a shell, never a string assembled from a request — which is
// the same rule every other command this dashboard runs is held to. Nothing in
// this package starts a process any other way.
//
// One thing is deliberately not taken from hostexec: its willingness to run a
// tool that exists only on the host side of a container boundary. A dump tool
// has to dial the server from where the dashboard dials it — a connection
// string naming a container on a Docker network resolves here and nowhere
// else — and it reads the credentials file and writes the dump at paths that
// exist in this mount namespace. So a tool counts as installed only when it is
// installed here, and when it is not the built-in dumper is used instead.

// toolRun is one invocation of an engine tool.
type toolRun struct {
	name string
	args []string
	// env is added to this process's environment, for the one secret Postgres
	// takes there rather than in a file.
	env []string
	// stdin feeds a script to a client; stdout receives a dump from a tool
	// that writes it there. Unset, the tool's standard output is read as
	// messages like its standard error.
	stdin  io.Reader
	stdout io.Writer
	// progress receives each line the tool prints as it prints it.
	progress func(line string)
}

// toolGrace is how long a tool gets between being asked to stop and being
// killed. A dump has nothing to tidy that the caller does not remove anyway.
const toolGrace = 5 * time.Second

// run starts the tool, waits for it, and returns the end of what it said.
//
// The process is started as its own group, so cancelling the context stops the
// tool and anything it started rather than only the process this one forked:
// a pg_restore running parallel workers is several processes, and a cancelled
// restore that left them loading is not cancelled.
func (t toolRun) run(ctx context.Context) (string, error) {
	cmd := hostexec.Command(ctx, t.name, t.args...)
	if len(t.env) > 0 {
		cmd.Env = append(os.Environ(), t.env...)
	}
	messages := newLineSink(t.progress)
	cmd.Stdin = t.stdin
	cmd.Stderr = messages
	if t.stdout != nil {
		cmd.Stdout = t.stdout
	} else {
		cmd.Stdout = messages
	}
	_, err := hostexec.RunGroup(ctx, cmd, toolGrace)
	messages.flush()
	return messages.tail(), err
}

// toolAvailable reports whether a dump tool can actually be executed here. An
// absolute path is one postgresTool already found on disk; a bare name has to
// be looked up, and not finding it is the ordinary case on a machine that never
// installed it rather than an error worth reporting.
func toolAvailable(name string) bool {
	if name == "" {
		return false
	}
	return hostexec.Available(name) && !hostexec.OnHost(name)
}

// firstAvailableTool returns the first of several names for the same tool that
// is installed. MariaDB ships its clients under their own names and, on some
// distributions, no longer under MySQL's.
func firstAvailableTool(names ...string) string {
	for _, name := range names {
		if toolAvailable(name) {
			return name
		}
	}
	return ""
}

// toolVersion asks a tool what it is. It is recorded beside the dump, because
// "which pg_dump wrote this" is the first question when a restore refuses an
// archive, and the answer cannot be recovered from the file afterwards on
// every engine.
func toolVersion(ctx context.Context, name string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := hostexec.Command(ctx, name, "--version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return versionNumber(line)
}

// versionNumber takes the version out of a tool's --version line, which every
// one of them phrases differently: "pg_dump (PostgreSQL) 17.8 (Ubuntu …)",
// "mysqldump  Ver 8.4.11 for Linux …", "mongodump version: 100.18.0".
func versionNumber(line string) string {
	for _, word := range strings.Fields(line) {
		word = strings.TrimSuffix(strings.TrimPrefix(word, "v"), ",")
		if word == "" || word[0] < '0' || word[0] > '9' || !strings.Contains(word, ".") {
			continue
		}
		return word
	}
	return strings.TrimSpace(line)
}

const (
	// sinkTailBytes is how much of a tool's output is kept for the result and
	// for an error. A verbose restore prints a line per object; the end is
	// where it says what went wrong.
	sinkTailBytes = 32 << 10
	// sinkLineBytes bounds one line, for a tool that prints without newlines.
	sinkLineBytes = 8 << 10
)

// lineSink turns a tool's output into lines for the progress callback while
// keeping the last of it. It is written to by the copier goroutines os/exec
// starts for stdout and stderr, hence the lock.
type lineSink struct {
	mu       sync.Mutex
	progress func(string)
	partial  bytes.Buffer
	kept     bytes.Buffer
}

func newLineSink(progress func(string)) *lineSink { return &lineSink{progress: progress} }

func (s *lineSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexAny(rest, "\r\n")
		if i < 0 {
			s.partial.Write(rest)
			if s.partial.Len() >= sinkLineBytes {
				s.emit()
			}
			break
		}
		s.partial.Write(rest[:i])
		s.emit()
		rest = rest[i+1:]
	}
	return len(p), nil
}

// emit delivers the buffered line. Must be called with the lock held.
func (s *lineSink) emit() {
	line := strings.TrimRight(s.partial.String(), " \t")
	s.partial.Reset()
	if line == "" {
		return
	}
	if s.kept.Len()+len(line)+1 > sinkTailBytes {
		// Drop from the front, on a line boundary, to make room.
		kept := s.kept.Bytes()
		cut := len(kept) + len(line) + 1 - sinkTailBytes
		if cut >= len(kept) {
			s.kept.Reset()
		} else {
			if nl := bytes.IndexByte(kept[cut:], '\n'); nl >= 0 {
				cut += nl + 1
			}
			remaining := append([]byte(nil), kept[cut:]...)
			s.kept.Reset()
			s.kept.Write(remaining)
		}
	}
	s.kept.WriteString(line)
	s.kept.WriteByte('\n')
	if s.progress != nil {
		s.progress(line)
	}
}

func (s *lineSink) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emit()
}

func (s *lineSink) tail() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.kept.String())
}

// The argument vectors.
//
// Every value that came from a request or a connection string is passed as
// option=value in one argument, or after a "--" that ends the options. A
// database called "--result-file=/etc/cron.d/x" is then a database of that
// name, which does not exist, rather than an instruction to the tool.

func pgConnArgs(info *ConnInfo) []string {
	return []string{"--host=" + info.Host, "--port=" + info.Port, "--username=" + info.User, "--no-password"}
}

func pgDumpArgs(info *ConnInfo, database, path string, opts DumpOptions, sel dumpSelection) []string {
	args := append(pgConnArgs(info), "--format=custom", "--verbose", "--file="+path)
	if opts.SchemaOnly {
		args = append(args, "--schema-only")
	}
	if opts.DataOnly {
		args = append(args, "--data-only")
	}
	if opts.Compression == CompressionNone {
		args = append(args, "--compress=0")
	}
	// pg_dump reads these as patterns. Quoting each part makes it the one name
	// it spells: inside double quotes a star is a star.
	for _, t := range sel.include {
		args = append(args, "--table="+t.postgresPattern())
	}
	for _, t := range sel.exclude {
		args = append(args, "--exclude-table="+t.postgresPattern())
	}
	return append(args, "--dbname="+database)
}

func pgRestoreArgs(info *ConnInfo, database, dumpPath string) []string {
	return append(pgConnArgs(info), "--verbose", "--clean", "--if-exists", "--dbname="+database, "--", dumpPath)
}

func psqlArgs(info *ConnInfo, database string) []string {
	return append(pgConnArgs(info), "--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1",
		"--single-transaction", "--dbname="+database)
}

func mysqldumpArgs(defaults, database string, opts DumpOptions, sel dumpSelection) []string {
	args := []string{
		"--defaults-extra-file=" + defaults,
		"--single-transaction", "--quick", "--verbose",
		// Tablespace metadata needs the PROCESS privilege, which is
		// server-wide and which no sensible application login has. Asking for
		// it put "mysqldump: Error: Access denied" on the end of a dump that
		// had otherwise worked perfectly.
		"--no-tablespaces",
	}
	switch {
	case opts.SchemaOnly:
		args = append(args, "--no-data", "--routines", "--triggers")
	case opts.DataOnly:
		// Routines and triggers are structure. Left on, a data-only dump
		// recreates every trigger over the ones already there.
		args = append(args, "--no-create-info", "--skip-triggers")
	default:
		args = append(args, "--routines", "--triggers")
	}
	for _, t := range sel.exclude {
		args = append(args, "--ignore-table="+database+"."+t.table)
	}
	args = append(args, "--", database)
	for _, t := range sel.include {
		args = append(args, t.table)
	}
	return args
}

func mysqlArgs(defaults, database string) []string {
	return []string{"--defaults-extra-file=" + defaults, "--database=" + database}
}

func mongodumpArgs(conf, database, path string, gzip bool, include string, exclude []string) []string {
	args := []string{"--config=" + conf, "--db=" + database, "--archive=" + path}
	if gzip {
		args = append(args, "--gzip")
	}
	if include != "" {
		args = append(args, "--collection="+include)
	}
	for _, name := range exclude {
		args = append(args, "--excludeCollection="+name)
	}
	return args
}

// mongorestoreArgs restores the collections of one database of an archive
// into the target database, and nothing else. source is the database they
// were dumped from; unknown, it is taken to be the target.
func mongorestoreArgs(conf, dumpPath string, gzip bool, source, database string) []string {
	args := []string{"--config=" + conf, "--archive=" + dumpPath, "--drop"}
	if gzip {
		args = append(args, "--gzip")
	}
	if source == "" {
		source = database
	}
	args = append(args, "--nsInclude="+source+".*")
	if source != database {
		args = append(args, "--nsFrom="+source+".*", "--nsTo="+database+".*")
	}
	return args
}
