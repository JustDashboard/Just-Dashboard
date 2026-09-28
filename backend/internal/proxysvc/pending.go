package proxysvc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

// Pending is what is on disk that the running nginx has not loaded.
//
// A save that did not reload, a hand edit, a site switched while its reload
// failed: each leaves nginx serving the configuration it read at its last
// reload, while every file says otherwise. nginx replaces all of its worker
// processes each time it loads its configuration and keeps them when a
// reload fails, so the start of its oldest worker is when the configuration
// it runs was read — and `nginx -s reload` exiting 0 says only that the
// signal was sent, not that the master managed to load what it was sent for.
type Pending struct {
	// Running says a running nginx was found that reads this configuration.
	// Without one there is nothing to compare the files with, and Reason
	// says why.
	Running bool   `json:"running"`
	Reason  string `json:"reason,omitempty"`
	// LastReload is when the running nginx loaded its configuration, and
	// Generation names that load, for a caller to ask whether a reload it
	// asked for has happened since.
	LastReload *time.Time `json:"lastReload,omitempty"`
	Generation string     `json:"generation,omitempty"`
	// Problem is nginx's first error in the configuration on disk, which it
	// refuses to load: every reload is turned away until it is fixed. The
	// files nginx would read are then unknown, and Files holds only those
	// the running nginx is known to have loaded.
	Problem string        `json:"problem,omitempty"`
	Files   []PendingFile `json:"files"`
}

// PendingFile is one change nginx has not loaded.
type PendingFile struct {
	// Path is the file as nginx names it: a site's link in sites-enabled,
	// not the file behind it.
	Path string `json:"path"`
	// Site and Layout are the Sites listing's entry for the file, where it
	// is one: the site a link in sites-enabled serves, or a conf.d file.
	Site   string `json:"site,omitempty"`
	Layout string `json:"layout,omitempty"`
	// Change is "changed" for a file whose content is not what nginx
	// loaded, or that was saved since without nginx having read it before;
	// "added" for a link put into sites-enabled since; and "removed" for a
	// file nginx loaded and no longer reads.
	Change string `json:"change"`
	// Modified is when the file, or its link, last changed. A removal has
	// no file left to say.
	Modified *time.Time `json:"modified,omitempty"`
}

const (
	pendingChanged = "changed"
	pendingAdded   = "added"
	pendingRemoved = "removed"
)

// pendingSettle is how long Pending waits for nginx to load a configuration
// newer than the one a caller saw before asking for a reload. The master
// takes the signal, reads every file and starts new workers in well under a
// second; one that has not done so by now refused what it read, and says why
// in its error log.
const pendingSettle = 5 * time.Second

// pendingDumpWait bounds the wait for `nginx -T`, which queues behind the
// service lock: a certificate order can hold that for minutes, and the page
// asking is read by every account.
const pendingDumpWait = 5 * time.Second

// pendingTracker remembers what the running nginx loaded, which nothing on
// disk records: nginx keeps no copy of the files it read. A file unchanged
// since the load is known to be what nginx read, and its digest is kept, so
// that a file that changed and changed back — a dry-run test puts the
// candidate at the live path and the original back — reads as loaded, and a
// file nginx loaded that has gone since can still be named.
type pendingTracker struct {
	mu sync.Mutex
	// generation is the load loaded belongs to; a new load starts afresh.
	generation string
	loaded     map[string]loadedFile
	// main is the last main configuration `nginx -T` named, for when it
	// cannot be run.
	main  string
	build *nginxBuild
	// processes lists nginx's processes; nil reads /proc. settle and
	// dumpWait override pendingSettle and pendingDumpWait. All three are for
	// tests.
	processes func() ([]nginxProcess, error)
	settle    time.Duration
	dumpWait  time.Duration
}

// loadedFile is a file the running nginx read: its digest, the change time
// it had then, and the listing entry it belongs to.
type loadedFile struct {
	digest       [sha256.Size]byte
	changed      time.Time
	site, layout string
}

// Pending says which changes on disk the running nginx has not loaded. after
// is a Generation the caller saw before it asked for a reload: the answer
// waits up to a few seconds for nginx to load a newer one, since `nginx -s
// reload` returns before nginx has.
//
// Only the files ReadConfig would show are compared, as EffectiveConfig
// returns them; a certificate nginx has not reloaded is a question for the
// certificate it serves, not for its configuration.
func (s *Service) Pending(ctx context.Context, after string) (*Pending, error) {
	out := &Pending{Files: []PendingFile{}}
	if !hostexec.Available("nginx") {
		out.Reason = "nginx is not installed on this host"
		return out, nil
	}
	t := &s.pending
	wait := pendingDumpWait
	if t.dumpWait > 0 {
		wait = t.dumpWait
	}
	dumpCtx, cancel := context.WithTimeout(ctx, wait)
	files, dumpErr := s.EffectiveConfig(dumpCtx)
	cancel()
	main := filepath.Join(s.nginxDir, "nginx.conf")
	t.mu.Lock()
	if dumpErr == nil && len(files) > 0 {
		t.main = files[0].Path
	}
	if t.main != "" {
		main = t.main
	}
	t.mu.Unlock()

	gen, reason, err := t.running(ctx, main, after)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		out.Reason = reason
		return out, nil
	}
	out.Running = true
	if gen.ticks == 0 {
		out.Reason = "nginx is running without a worker process"
		return out, nil
	}
	loaded := gen.loaded
	out.LastReload, out.Generation = &loaded, gen.token()

	listed := dumpErr == nil
	if !listed && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !listed && !errors.Is(dumpErr, context.DeadlineExceeded) {
		// nginx -T refused the configuration. What it said is nginx's own
		// test output, which is what the operator has to fix.
		problem := &ValidationResult{Output: strings.TrimPrefix(dumpErr.Error(), "nginx -T: ")}
		problem.diagnose("nginx")
		if firstFailure(problem) == nil {
			return nil, dumpErr
		}
		out.Problem = FailureHeadline(problem)
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	var judged bool
	out.Files, judged = t.compare(s, gen, paths, listed)
	if !judged && out.Problem == "" {
		// The dump waited out its time behind the lock, and nothing is
		// known of this load to judge instead: saying nothing is pending
		// would be a guess.
		return nil, fmt.Errorf("nginx -T is waiting behind another change to the configuration: %w", dumpErr)
	}
	return out, nil
}

// compare judges each file nginx reads against what the running nginx
// loaded, and names the files it loaded that it no longer reads. With listed
// false the files nginx reads are unknown, and only those it is known to
// have loaded are judged; judged is false when there are none.
func (t *pendingTracker) compare(s *Service, gen generation, paths []string, listed bool) (files []PendingFile, judged bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.generation != gen.token() {
		t.generation, t.loaded = gen.token(), map[string]loadedFile{}
	}
	judged = listed || len(t.loaded) > 0
	if !listed {
		for path := range t.loaded {
			paths = append(paths, path)
		}
	}
	out := []PendingFile{}
	present := map[string]bool{}
	for _, path := range paths {
		if present[path] {
			continue
		}
		changed, link, ok := changeTimes(path)
		if !ok {
			continue
		}
		present[path] = true
		file, known := t.loaded[path]
		if known && changed.Equal(file.changed) {
			continue
		}
		if known || !changed.After(gen.loaded) {
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(content)
			switch {
			case !known:
				site, layout := s.pendingSite(path)
				t.loaded[path] = loadedFile{digest: sum, changed: changed, site: site, layout: layout}
				continue
			case sum == file.digest:
				file.changed = changed
				t.loaded[path] = file
				continue
			}
		}
		change := pendingChanged
		if !known && link.After(gen.loaded) {
			change = pendingAdded
		}
		site, layout := file.site, file.layout
		if !known {
			site, layout = s.pendingSite(path)
		}
		when := changed
		out = append(out, PendingFile{Path: path, Site: site, Layout: layout, Change: change, Modified: &when})
	}
	for path, file := range t.loaded {
		if !present[path] {
			out = append(out, PendingFile{Path: path, Site: file.site, Layout: file.layout, Change: pendingRemoved})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, judged
}

// changeTimes are when path last changed, through its links, and when its own
// link did, if it is one. The change time rather than the modification time:
// a file moved or copied in with its old modification time kept is still a
// file nginx has not read, and nothing but the kernel sets the change time.
func changeTimes(path string) (changed, link time.Time, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	changed = changeTime(info)
	if own, err := os.Lstat(path); err == nil && own.Mode()&os.ModeSymlink != 0 {
		link = changeTime(own)
		if link.After(changed) {
			changed = link
		}
	}
	return changed, link, true
}

func changeTime(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctim.Unix()).UTC()
	}
	return info.ModTime().UTC()
}

// pendingSite is the Sites listing's entry for a file nginx reads, by the
// listing's own rules: a link in sites-enabled belongs to the sites-available
// file it serves, under that file's name, and is listed as its own otherwise;
// a conf.d file is listed by its name. Anything else — nginx.conf, a snippet
// — is no one site's.
func (s *Service) pendingSite(path string) (site, layout string) {
	dir, name := filepath.Split(path)
	switch filepath.Clean(dir) {
	case filepath.Join(s.nginxDir, "sites-enabled"):
		if site := s.availableSiteOf(path); site != "" {
			return site, "sites-available"
		}
		return name, "sites-enabled"
	case filepath.Join(s.nginxDir, "conf.d"):
		return name, "conf.d"
	}
	return "", ""
}

// nginxProcess is one process of a running nginx.
type nginxProcess struct {
	PID, PPID int
	// Title is the command line nginx writes over its own: "nginx: master
	// process /usr/sbin/nginx -c /etc/nginx/nginx.conf", "nginx: worker
	// process", "nginx: worker process is shutting down".
	Title string
	// Ticks is when the process started, in clock ticks since boot: exact,
	// and the same on every read. Start is the same moment as a wall-clock
	// time, rounded up to the tick.
	Ticks uint64
	Start time.Time
}

// generation is a configuration a running nginx loaded: its master, and the
// start of its oldest worker that is not shutting down.
type generation struct {
	master int
	ticks  uint64
	loaded time.Time
}

func (g generation) token() string { return fmt.Sprintf("%d-%d", g.master, g.ticks) }

// ParseGeneration reads a Generation back, for a caller to name the load it
// saw.
func ParseGeneration(token string) (master int, ticks uint64, err error) {
	pid, tick, ok := strings.Cut(token, "-")
	if ok {
		if master, err = strconv.Atoi(pid); err == nil && master > 0 {
			if ticks, err = strconv.ParseUint(tick, 10, 64); err == nil && ticks > 0 {
				return master, ticks, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("%q is not a configuration load", token)
}

// newerThan is whether g is a later load than the one token names: a later
// worker start under the same master, or another master altogether, which
// read the configuration when it started.
func (g generation) newerThan(token string) bool {
	master, ticks, err := ParseGeneration(token)
	if err != nil {
		return true
	}
	return g.master != master || g.ticks > ticks
}

// running finds the nginx that reads main and the configuration it loaded,
// waiting while that is not newer than after. reason says why there is none
// to find.
func (t *pendingTracker) running(ctx context.Context, main, after string) (generation, string, error) {
	settle := pendingSettle
	if t.settle > 0 {
		settle = t.settle
	}
	deadline := time.Now().Add(settle)
	for {
		procs, err := t.list()
		if err != nil {
			return generation{}, "", err
		}
		master, reason := t.master(ctx, procs, main)
		if reason != "" {
			return generation{}, reason, nil
		}
		gen := currentGeneration(procs, master.PID)
		if after == "" || gen.newerThan(after) || !time.Now().Before(deadline) {
			return gen, "", nil
		}
		select {
		case <-ctx.Done():
			return gen, "", nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (t *pendingTracker) list() ([]nginxProcess, error) {
	if t.processes != nil {
		return t.processes()
	}
	return procNginxProcesses("/proc")
}

// currentGeneration is the load master's workers run. nginx starts every
// worker afresh for each load and retitles the ones from before as shutting
// down, which may take as long as their longest connection; a worker started
// later on its own replaces one that died, and runs the same load. With no
// worker the generation is empty.
func currentGeneration(procs []nginxProcess, master int) generation {
	gen := generation{master: master}
	for _, p := range procs {
		if p.PPID != master || p.Title != "nginx: worker process" {
			continue
		}
		if gen.ticks == 0 || p.Ticks < gen.ticks {
			gen.ticks, gen.loaded = p.Ticks, p.Start
		}
	}
	return gen
}

// master is the nginx master process that reads main, the configuration file
// this service manages. A host can run several nginx — the host's own, and
// others in containers, which the dashboard's container sees too because it
// shares the host's processes — and each is told apart by the configuration
// its command line names, or the compiled-in one where it names none. Two
// that read the same path are told apart by the pid file main names.
func (t *pendingTracker) master(ctx context.Context, procs []nginxProcess, main string) (nginxProcess, string) {
	var candidates []nginxProcess
	for _, p := range procs {
		conf, prefix, ok := masterArgs(p.Title)
		if !ok {
			continue
		}
		if conf == "" || !filepath.IsAbs(conf) {
			build := t.nginxBuild(ctx)
			if prefix == "" {
				prefix = build.prefix
			}
			if conf == "" {
				conf = build.conf
			}
			if conf != "" && !filepath.IsAbs(conf) {
				conf = filepath.Join(prefix, conf)
			}
		}
		if conf != "" && sameFile(conf, main) {
			candidates = append(candidates, p)
		}
	}
	switch len(candidates) {
	case 0:
		return nginxProcess{}, "no running nginx reads " + main
	case 1:
		return candidates[0], ""
	}
	pid := masterFromPidFile(main, t.nginxBuild(ctx))
	for _, p := range candidates {
		if p.PID == pid {
			return p, ""
		}
	}
	return nginxProcess{}, fmt.Sprintf("%d nginx master processes read %s, and its pid file names none of them", len(candidates), main)
}

// masterArgs reads a master's -c and -p from its title, which is its command
// line joined by spaces. nginx takes a flag's value from the rest of its word
// or from the next one.
func masterArgs(title string) (conf, prefix string, ok bool) {
	rest, ok := strings.CutPrefix(title, "nginx: master process ")
	if !ok {
		return "", "", false
	}
	args := strings.Fields(rest)
	for i := 1; i < len(args); i++ {
		arg := args[i]
		var value *string
		switch {
		case strings.HasPrefix(arg, "-c"):
			value = &conf
		case strings.HasPrefix(arg, "-p"):
			value = &prefix
		default:
			continue
		}
		if len(arg) > 2 {
			*value = arg[2:]
		} else if i+1 < len(args) {
			*value = args[i+1]
			i++
		}
	}
	return conf, prefix, true
}

func sameFile(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b) || resolvedFile(a) == resolvedFile(b)
}

// masterFromPidFile is the pid in the pid file main names, or the compiled-in
// one. From the dashboard's container the host's /run is under /host.
func masterFromPidFile(main string, build nginxBuild) int {
	path := build.pid
	if b, err := os.ReadFile(main); err == nil {
		if directives, err := ParseNginxFile(main, string(b), nil); err == nil {
			for _, d := range directives {
				if d.Name == "pid" && len(d.Args) == 1 {
					path = d.Args[0]
				}
			}
		}
	}
	if path == "" {
		return 0
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(build.prefix, path)
	}
	for _, candidate := range []string{filepath.Join("/host", path), path} {
		if b, err := os.ReadFile(candidate); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
	}
	return 0
}

// nginxBuild is what nginx was compiled to read when its command line does
// not say.
type nginxBuild struct{ prefix, conf, pid string }

// nginxBuild asks `nginx -V` once. Must be called without t.mu held.
func (t *pendingTracker) nginxBuild(ctx context.Context) nginxBuild {
	t.mu.Lock()
	if t.build != nil {
		defer t.mu.Unlock()
		return *t.build
	}
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := hostexec.Command(ctx, "nginx", "-V").CombinedOutput()
	build := parseNginxBuild(string(out))
	if err == nil {
		t.mu.Lock()
		t.build = &build
		t.mu.Unlock()
	}
	return build
}

// parseNginxBuild reads the paths from `nginx -V`'s configure arguments, with
// nginx's own defaults for those it was built without.
func parseNginxBuild(out string) nginxBuild {
	build := nginxBuild{prefix: "/usr/local/nginx"}
	for _, field := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(field, "--prefix="); ok {
			build.prefix = v
		} else if v, ok := strings.CutPrefix(field, "--conf-path="); ok {
			build.conf = v
		} else if v, ok := strings.CutPrefix(field, "--pid-path="); ok {
			build.pid = v
		}
	}
	if build.conf == "" {
		build.conf = "conf/nginx.conf"
	}
	if build.pid == "" {
		build.pid = "logs/nginx.pid"
	}
	if !filepath.IsAbs(build.conf) {
		build.conf = filepath.Join(build.prefix, build.conf)
	}
	if !filepath.IsAbs(build.pid) {
		build.pid = filepath.Join(build.prefix, build.pid)
	}
	return build
}

// clockTicks is USER_HZ, the unit of a process's start time in /proc, which
// Linux fixes at 100 on every architecture it is built for here.
const clockTicks = 100

// procNginxProcesses lists the processes under root whose command is nginx.
//
// A start time in /proc counts clock ticks since boot, and boot is placed on
// the wall clock from CLOCK_BOOTTIME rather than /proc/stat's btime, which is
// whole seconds: a second's error puts a file saved just before a reload
// after it. The start is rounded up to the tick, since the kernel rounds it
// down: a file saved in the same hundredth of a second as the reload was read
// by it, as nginx reads its files before it starts the workers.
func procNginxProcesses(root string) ([]nginxProcess, error) {
	var boot unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &boot); err != nil {
		return nil, err
	}
	booted := time.Now().Add(-time.Duration(boot.Nano()))
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := []nginxProcess{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		// A process can exit between the listing and the read.
		stat, err := os.ReadFile(filepath.Join(root, e.Name(), "stat"))
		if err != nil {
			continue
		}
		comm, ppid, ticks, ok := parseProcStat(string(stat))
		if !ok || comm != "nginx" {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(root, e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		out = append(out, nginxProcess{
			PID: pid, PPID: ppid,
			Title: strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " ")),
			Ticks: ticks,
			Start: booted.Add(time.Duration(ticks+1) * time.Second / clockTicks).UTC(),
		})
	}
	return out, nil
}

// parseProcStat reads the command, parent and start time out of
// /proc/<pid>/stat. The command is in parentheses and may itself hold spaces
// and parentheses, so the fields are counted from the last ")".
func parseProcStat(stat string) (comm string, ppid int, ticks uint64, ok bool) {
	open, end := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if open < 0 || end < open {
		return "", 0, 0, false
	}
	fields := strings.Fields(stat[end+1:])
	// state ppid pgrp session tty_nr tpgid flags minflt cminflt majflt
	// cmajflt utime stime cutime cstime priority nice num_threads
	// itrealvalue starttime
	if len(fields) < 20 {
		return "", 0, 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, 0, false
	}
	ticks, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	return stat[open+1 : end], ppid, ticks, true
}
