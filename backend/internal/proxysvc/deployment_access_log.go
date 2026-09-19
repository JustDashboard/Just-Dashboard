package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The request record, read back.
//
// Where it lives depends on which ingress is serving the deployment, and the
// difference is real rather than cosmetic: managed host nginx writes the stock
// `combined` format to a file under /var/log/nginx that logrotate owns, while
// the Docker Caddy ingress writes JSON inside its own persistent volume, which
// this process can only reach through the container. Both are hidden behind
// accesslog.Reader, and the store above them reads each byte once.
//
// Both readers address a file by what it is rather than where it is. A roll
// renames the file the reader was following and puts a new one at its path;
// a reader that reopened the path would read the new file from the old
// offset and never see the old file's tail. So a read names the generation it
// wants by inode, finds it wherever it now sits, and holds it open while it
// reads — an open file keeps its inode however it is renamed.

const (
	accessDriverCaddy = "docker-caddy"
	accessDriverNginx = "nginx"
)

// AccessLogReader resolves which ingress serves the route and returns a reader
// over its record, with the facts the page needs about that record.
func (s *Service) AccessLogReader(ctx context.Context, name string) (accesslog.Reader, accesslog.Facts, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, accesslog.Facts{}, err
	}
	if edge != nil {
		path, err := dockerCaddyAccessLogPath(name)
		if err != nil {
			return nil, accesslog.Facts{}, err
		}
		facts := accesslog.Facts{Driver: accessDriverCaddy, Format: accesslog.FormatCaddyJSON, Latency: true, Path: path}
		return &caddyAccessLog{edge: *edge, path: path}, facts, nil
	}
	if !siteNameRe.MatchString(name) {
		return nil, accesslog.Facts{}, errors.New("invalid deployment route name")
	}
	path := nginxAccessLogPath(name)
	facts := accesslog.Facts{Driver: accessDriverNginx, Format: accesslog.FormatCombined, Latency: false, Path: path}
	return &fileAccessLog{path: path}, facts, nil
}

// nginxAccessLogPath is the file `deploymentSiteSpec` asked nginx for. The two
// spellings must agree, and this is the one place either is written.
func nginxAccessLogPath(name string) string {
	return "/var/log/nginx/" + name + ".access.log"
}

// caddyAccessLog reads a route's record out of the ingress container.
type caddyAccessLog struct {
	edge dockerCaddy
	path string
}

// caddyReadScript is one exec that does everything a read needs, so a poll
// costs one process rather than three: it reports the live file on stderr,
// finds the wanted generation by inode, opens it and holds it, reports what
// it opened on stdout, and streams the wanted range after that.
//
// Only constant shell source is used; every variable is an argument. The
// file is read through the held descriptor rather than by path, which is what
// makes the report and the bytes describe the same generation even if the
// ingress rolls the file in between.
const caddyReadScript = `dir="$1"; name="$2"; want="$3"; off="$4"; lim="$5"
live="$dir/$name"
if [ -f "$live" ]; then printf 'live %s\n' "$(stat -c '%i %s %Y' "$live")" >&2; else printf 'live absent\n' >&2; fi
if [ -z "$want" ]; then f="$live"; else f=$(find "$dir" -maxdepth 1 -type f -inum "$want" 2>/dev/null | head -n 1); fi
if [ -z "$f" ] || [ ! -f "$f" ]; then printf 'absent\n'; exit 0; fi
exec 3<"$f"
s=$(stat -L -c '%i %s %Y' /proc/self/fd/3) || { printf 'absent\n'; exit 0; }
printf 'file %s\n' "$s"
if [ -n "$want" ] && [ "${s%% *}" != "$want" ]; then exit 0; fi
[ "$lim" -gt 0 ] || exit 0
tail -c +"$off" <&3 | head -c "$lim"
`

// caddyRolledScript lists the rolled generations beside the live file. A glob
// with nothing to match stays a literal, which the file test skips.
const caddyRolledScript = `cd "$1" 2>/dev/null || exit 0
for f in "$2"-*.log; do [ -f "$f" ] && stat -c '%i %s %Y' "$f"; done
exit 0
`

func (c *caddyAccessLog) Read(ctx context.Context, identity string, offset, limit int64, fn func(string)) (accesslog.FileStat, accesslog.FileStat, int64, error) {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	command := hostexec.Command(ctx, "docker", "exec", "-i", c.edge.ID, "sh", "-c", caddyReadScript, "sh",
		filepath.Dir(c.path), filepath.Base(c.path), identity,
		// tail counts from one.
		strconv.FormatInt(offset+1, 10), strconv.FormatInt(limit, 10))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return accesslog.FileStat{}, accesslog.FileStat{}, offset, fmt.Errorf("read the ingress request record: %w", err)
	}
	read, live, data := parseCaddyRead(stdout.Bytes(), stderr.Bytes())
	if !read.Exists || limit <= 0 {
		return read, live, offset, nil
	}
	next := offset + accesslog.ConsumeLines(data, int64(len(data)) >= limit, fn)
	return read, live, next, nil
}

// parseCaddyRead splits the script's two reports from the bytes that follow
// the first. Anything unexpected reads as absent: a shell that died before
// its first report has nothing to say about the file, and treating that as
// "gone" makes the store follow the live file again, which is the safe move.
func parseCaddyRead(stdout, stderr []byte) (read, live accesslog.FileStat, data []byte) {
	for _, l := range bytes.Split(stderr, []byte{'\n'}) {
		if rest, ok := bytes.CutPrefix(l, []byte("live ")); ok {
			live = parseStatLine(string(rest))
			break
		}
	}
	head, rest, found := bytes.Cut(stdout, []byte{'\n'})
	if !found {
		head = stdout
	}
	if body, ok := bytes.CutPrefix(head, []byte("file ")); ok {
		read = parseStatLine(string(body))
		if found {
			data = rest
		}
	}
	return read, live, data
}

// parseStatLine reads "inode size mtime", as both scripts print it.
func parseStatLine(text string) accesslog.FileStat {
	fields := strings.Fields(text)
	if len(fields) < 3 || fields[0] == "absent" {
		return accesslog.FileStat{}
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return accesslog.FileStat{}
	}
	stat := accesslog.FileStat{Exists: true, Identity: fields[0], Size: size}
	if seconds, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
		stat.Modified = time.Unix(seconds, 0).UTC()
	}
	return stat
}

func (c *caddyAccessLog) Rolled(ctx context.Context) ([]accesslog.FileStat, error) {
	command := hostexec.Command(ctx, "docker", "exec", "-i", c.edge.ID, "sh", "-c", caddyRolledScript, "sh",
		filepath.Dir(c.path), strings.TrimSuffix(filepath.Base(c.path), ".log"))
	raw, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list the ingress request record's generations: %w", err)
	}
	var out []accesslog.FileStat
	for _, l := range strings.Split(string(raw), "\n") {
		if stat := parseStatLine(l); stat.Exists {
			out = append(out, stat)
		}
	}
	sortGenerations(out)
	return out, nil
}

// sortGenerations orders oldest first, which is the order they are read in so
// the record ends up in time order. Two written in the same second — a roll
// storm — fall back to the inode, which only has to be stable.
func sortGenerations(gens []accesslog.FileStat) {
	sort.SliceStable(gens, func(i, j int) bool {
		if !gens[i].Modified.Equal(gens[j].Modified) {
			return gens[i].Modified.Before(gens[j].Modified)
		}
		return gens[i].Identity < gens[j].Identity
	})
}

// fileAccessLog reads a record that is a file this process can open: host
// nginx's, under a log root the dashboard mounts.
type fileAccessLog struct {
	path string
}

func (f *fileAccessLog) Read(ctx context.Context, identity string, offset, limit int64, fn func(string)) (accesslog.FileStat, accesslog.FileStat, int64, error) {
	if offset < 0 {
		offset = 0
	}
	live := statPath(f.path)
	path := f.path
	if identity != "" && identity != live.Identity {
		path = f.rolledPath(identity)
		if path == "" {
			return accesslog.FileStat{}, live, offset, nil
		}
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return accesslog.FileStat{}, live, offset, nil
		}
		return accesslog.FileStat{}, live, offset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return accesslog.FileStat{}, live, offset, err
	}
	read := statInfo(info)
	// Renamed between the stat and the open: the path now holds a different
	// generation. The store treats "gone" by finding it again next time.
	if identity != "" && read.Identity != identity {
		return accesslog.FileStat{}, live, offset, nil
	}
	if limit <= 0 || offset >= read.Size {
		return read, live, offset, nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return read, live, offset, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return read, live, offset, err
	}
	return read, live, offset + accesslog.ConsumeLines(data, int64(len(data)) >= limit, fn), nil
}

func (f *fileAccessLog) Rolled(context.Context) ([]accesslog.FileStat, error) {
	var out []accesslog.FileStat
	for _, path := range f.generations() {
		if stat := statPath(path); stat.Exists {
			out = append(out, stat)
		}
	}
	sortGenerations(out)
	return out, nil
}

// generations lists the rotated files beside the live one that can be read
// as text. logrotate's compressed generations are skipped: a `.gz` cannot be
// read from an offset, and the newest generation is the uncompressed one
// under `delaycompress`, which is the one a roll's tail sits in.
func (f *fileAccessLog) generations() []string {
	matches, _ := filepath.Glob(f.path + "*")
	var out []string
	for _, match := range matches {
		if match == f.path {
			continue
		}
		switch filepath.Ext(match) {
		case ".gz", ".bz2", ".xz", ".zst":
			continue
		}
		out = append(out, match)
	}
	return out
}

func (f *fileAccessLog) rolledPath(identity string) string {
	for _, path := range f.generations() {
		if statPath(path).Identity == identity {
			return path
		}
	}
	return ""
}

func statPath(path string) accesslog.FileStat {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return accesslog.FileStat{}
	}
	return statInfo(info)
}

// statInfo reads the inode as the identity. Off Linux there is none to read,
// and a record with no identity is followed by path alone — rotation goes
// unnoticed there, truncation does not.
func statInfo(info os.FileInfo) accesslog.FileStat {
	stat := accesslog.FileStat{Exists: true, Size: info.Size(), Modified: info.ModTime().UTC()}
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		stat.Identity = strconv.FormatUint(uint64(sys.Ino), 10)
	}
	return stat
}

// removeAccessLog deletes a route's request record. Best effort by design: the
// route is already gone by the time this runs, and failing the removal of a
// deployment because a log file could not be unlinked would leave the operator
// with a half-removed deployment over a file that logrotate will take anyway.
func (c *dockerCaddy) removeAccessLog(ctx context.Context, name string) {
	path, err := dockerCaddyAccessLogPath(name)
	if err != nil {
		return
	}
	// Caddy's roller writes siblings named <base>-<timestamp>.log, so the
	// generations go with the live file rather than outliving it.
	_, _ = c.command(ctx, "", "sh", "-c",
		`rm -f "$1" "${1%.log}"-*.log "${1%.log}"-*.log.gz`, "sh", path)
}

// ensureAccessLogDir creates the directory Caddy is about to be pointed at.
// Caddy would create it, but only at the moment the first request arrives —
// and a reader that checks for the file in between would report the ingress as
// unavailable rather than idle.
func (c *dockerCaddy) ensureAccessLogDir(ctx context.Context) error {
	_, err := c.command(ctx, "", "mkdir", "-p", dockerCaddyAccessRoot)
	return err
}
