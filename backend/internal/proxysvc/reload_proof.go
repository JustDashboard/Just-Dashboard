package proxysvc

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// What a reload did, asked of the master that was sent it.
//
// `nginx -s reload` exiting 0 says only that the signal reached the master.
// The master then reads every file again and, when it can, opens what the new
// configuration listens on and replaces all of its workers; when it cannot —
// a port another program holds is the usual case, and `nginx -t` passes it —
// it logs the reason at [emerg] and goes on serving what it had. A save that
// said "is live" after the signal alone could be a save nginx never loaded,
// and every later reload would fail on the same port.

// Load states.
const (
	// LoadLoaded is a reload the master took up: every worker serving is one
	// started since the signal, each file the change wrote still holds what
	// it wrote, and nginx holds every socket the change listens on.
	LoadLoaded = "loaded"
	// LoadRefused is a reload the master logged an error for and did not take
	// up: it serves the configuration it had before.
	LoadRefused = "refused"
	// LoadUnconfirmed is a reload the master was not seen taking up within
	// the wait, or took up without what the change asked for.
	LoadUnconfirmed = "unconfirmed"
	// LoadUnchecked is a host whose running nginx could not be read.
	LoadUnchecked = "unchecked"
)

// LoadProof is what the running nginx showed of a reload.
type LoadProof struct {
	State string `json:"state"`
	// Master is the master the reload was sent to, and Workers how many
	// workers it runs on the configuration it loaded.
	Master  int32 `json:"master,omitempty"`
	Workers int   `json:"workers,omitempty"`
	// LoadedAt is when the newest worker started: when the master took the
	// configuration up.
	LoadedAt *time.Time `json:"loadedAt,omitempty"`
	// Files are the files the change wrote, each with the digest it wrote
	// and whether the file still held it once nginx had loaded.
	Files []ProvenFile `json:"files,omitempty"`
	// Listens are the sockets the change asks for; Listening is whether
	// nginx held every one of them after the load. Both are absent for a
	// reload that names no change, such as the engine's own.
	Listens   []string `json:"listens,omitempty"`
	Listening *bool    `json:"listening,omitempty"`
	// Error is the master's first [emerg] line for a refused reload, without
	// its timestamp and pids.
	Error string `json:"error,omitempty"`
	// Note says why a load is unconfirmed or unchecked.
	Note string `json:"note,omitempty"`

	// bindError is set when Error is nginx failing to bind one of the
	// change's own sockets, which the change must not leave behind.
	bindError bool
}

// ProvenFile is one file a reload was sent for.
type ProvenFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	// Held is whether the file held exactly that content once nginx had
	// loaded; false means something wrote it again after the change.
	Held bool `json:"held"`
}

// loadMark is the running nginx, the end of its error log and the change's
// files just before a reload: what the reload is measured against.
type loadMark struct {
	reload reloadMark
	files  []ProvenFile
	sent   time.Time
}

// loadWait is how long a reload is watched. The master reads the files,
// opens new sockets and starts its workers in well under a second, and logs a
// port it cannot bind at the first of its five attempts.
var loadWait = 3 * time.Second

// markLoad notes the running nginx and what each of the change's files holds,
// before the reload is sent. files maps a path to the content the change
// wrote there.
func (s *Service) markLoad(ctx context.Context, files map[string]string) loadMark {
	tree, _ := readConfigFiles(s.nginxDir)
	mark := loadMark{reload: s.markReload(ctx, tree), sent: time.Now()}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		mark.files = append(mark.files, ProvenFile{Path: path, Digest: ContentDigest(files[path])})
	}
	return mark
}

// awaitLoad watches the master take a reload up, for loadWait at most: done
// when every worker serving is new and nginx holds the change's sockets,
// refused when the master logs an [emerg] line after the signal.
func (s *Service) awaitLoad(ctx context.Context, mark loadMark, binds []bind) *LoadProof {
	proof := &LoadProof{Files: slices.Clone(mark.files)}
	for _, b := range binds {
		if label := b.label(); !slices.Contains(proof.Listens, label) {
			proof.Listens = append(proof.Listens, label)
		}
	}
	before := mark.reload.nginx
	if before == nil {
		proof.State = LoadUnchecked
		proof.Note = "Whether nginx took the reload up could not be checked: " + mark.reload.why + "."
		return proof
	}
	proof.Master = before.master
	deadline := time.Now().Add(loadWait)
	offset := mark.reload.offset
	var logged []string
	// retried is the master's first bind() failure when its attempts ended
	// without it giving the reload up: a socket freed between attempts is
	// bound on the next, and the reload is then read as any other load.
	retried := ""
	for {
		if mark.reload.log != "" {
			var fresh []string
			fresh, offset = appendedLines(mark.reload.log, offset)
			logged = append(logged, emergencies(fresh)...)
		}
		if len(logged) > 0 && (retried == "" || slices.ContainsFunc(logged, gaveUpBinding)) {
			if retried == "" {
				// A failed attempt logs every socket it could not bind before
				// it waits to try again; the rest of that attempt is read
				// first.
				time.Sleep(150 * time.Millisecond)
				var fresh []string
				fresh, offset = appendedLines(mark.reload.log, offset)
				logged = append(logged, emergencies(fresh)...)
			}
			if _, ok := parseBindFailure(logged[0]); ok && !slices.ContainsFunc(logged, gaveUpBinding) {
				// The master tries a socket it cannot bind five times, half
				// a second apart, and gives the reload up only after the
				// last. Its later attempts are this reload's, and read as the
				// next one's if it comes before they end; and until it says
				// it gave up, the failure is not a refusal.
				var fresh []string
				fresh, offset = awaitBindAttempts(mark.reload.log, offset)
				logged = append(logged, emergencies(fresh)...)
				if !slices.ContainsFunc(logged, gaveUpBinding) {
					retried = emergencyText(logged[0])
				}
			}
			if retried == "" || slices.ContainsFunc(logged, gaveUpBinding) {
				proof.State, proof.Error = LoadRefused, emergencyText(logged[0])
				for _, line := range logged {
					failure, ok := parseBindFailure(line)
					if !ok {
						continue
					}
					for _, b := range binds {
						if failure.address == b.address() {
							proof.Error, proof.bindError = failure.text, true
							return proof
						}
					}
				}
				return proof
			}
		}
		view := readNginx(ctx, s, nil, before.master)
		after := view.nginx
		if after != nil && replacedAll(before, after) {
			proof.Workers = len(after.workers)
			if !after.loaded.IsZero() {
				loaded := after.loaded
				proof.LoadedAt = &loaded
			}
			held := s.filesHeld(proof.Files)
			listening := len(binds) == 0 || view.servedByNginx(binds)
			if len(binds) > 0 {
				proof.Listening = &listening
			}
			switch {
			case !held:
				proof.State = LoadUnconfirmed
				proof.Note = "nginx loaded a configuration, but a file the change wrote was written again before it did, so what it loaded is not this change."
			case !listening:
				proof.State = LoadUnconfirmed
				proof.Note = fmt.Sprintf("nginx took the reload up but holds no socket for %s.", bindsLabel(binds))
			default:
				proof.State = LoadLoaded
			}
			return proof
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			proof.State = LoadUnconfirmed
			switch {
			case after == nil:
				proof.Note = "The nginx master the reload was sent to is gone: " + view.why + "."
			case startedAnew(before, after):
				proof.Note = fmt.Sprintf("nginx started a new worker but %s after the reload still runs a worker from before it, which a worker that crashed and was replaced also does.", loadWait)
			case retried != "":
				proof.Note = fmt.Sprintf("nginx could not bind a socket at first (%s) and, %s after the reload was sent, had neither given it up nor been seen taking it up.", retried, loadWait)
			case mark.reload.log == "":
				proof.Note = fmt.Sprintf("nginx had not taken the reload up %s after it was sent, and its error log, which would say why, could not be read.", loadWait)
			default:
				proof.Note = fmt.Sprintf("nginx had not taken the reload up %s after it was sent, and logged nothing about it.", loadWait)
			}
			return proof
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// replacedAll reports a master serving only workers that were not there
// before: the whole set nginx starts when it loads a configuration. One new
// worker beside old ones is a crashed worker replaced on the same load.
func replacedAll(before, after *streamNginxProcess) bool {
	if len(after.workers) == 0 {
		return false
	}
	for _, pid := range after.workers {
		if slices.Contains(before.workers, pid) {
			return false
		}
	}
	return true
}

// filesHeld marks each file that still holds the content the change wrote,
// and reports whether every one does.
func (s *Service) filesHeld(files []ProvenFile) bool {
	all := true
	for i := range files {
		b, err := os.ReadFile(files[i].Path)
		files[i].Held = err == nil && ContentDigest(string(b)) == files[i].Digest
		all = all && files[i].Held
	}
	return all
}

// reloadProven reloads nginx and watches the master take it up. A reload the
// master refused is reported as not reloaded, with nginx's own words.
func (s *Service) reloadProven(ctx context.Context, files map[string]string, binds []bind) (reloaded bool, output, failure string, proof *LoadProof) {
	mark := s.markLoad(ctx, files)
	reloaded, output, failure = reloadNginxChecked(ctx)
	if !reloaded {
		return false, output, failure, nil
	}
	proof = s.awaitLoad(ctx, mark, binds)
	if proof.State == LoadRefused {
		return false, output, "nginx did not take the reload up: " + proof.Error, proof
	}
	return true, output, "", proof
}

// siteBinds are the sockets a site's file asks nginx for: every listen of its
// server blocks, and *:80 for a block with none.
func siteBinds(path, content string) []bind {
	tree, err := ParseNginxFile(path, content, []string{"http"})
	if err != nil {
		return nil
	}
	var out []bind
	for _, d := range tree {
		if d.Name != "server" || d.Block == nil {
			continue
		}
		listens := 0
		for _, inner := range d.Block {
			if inner.Name != "listen" {
				continue
			}
			listens++
			for _, b := range listenBinds(inner.Args, true) {
				if !slices.Contains(out, b) {
					out = append(out, b)
				}
			}
		}
		if listens == 0 && !slices.Contains(out, bind{"0.0.0.0", 80, false}) {
			out = append(out, bind{"0.0.0.0", 80, false})
		}
	}
	return out
}

// enabledFiles are the paths a site's change is read through: its file, and
// the link in sites-enabled that names it, where there is one.
func enabledFiles(full, content, link string) map[string]string {
	files := map[string]string{full: content}
	if target, err := filepath.EvalSymlinks(link); err == nil && sameFile(target, full) {
		files[link] = content
	}
	return files
}

// bindAttemptsWait bounds the wait for the master's last bind() attempt:
// five attempts half a second apart.
var bindAttemptsWait = 3 * time.Second

// awaitBindAttempts reads the error log from offset until the master gives a
// reload up over a socket it could not bind, which it says after its last
// attempt, or bindAttemptsWait passes, and returns the lines it read.
func awaitBindAttempts(log string, offset int64) ([]string, int64) {
	deadline := time.Now().Add(bindAttemptsWait)
	var read []string
	for time.Now().Before(deadline) {
		var fresh []string
		fresh, offset = appendedLines(log, offset)
		read = append(read, fresh...)
		if slices.ContainsFunc(fresh, gaveUpBinding) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return read, offset
}

// gaveUpBinding is the line the master logs after its last bind() attempt.
func gaveUpBinding(line string) bool {
	return strings.Contains(line, "still could not bind()")
}
