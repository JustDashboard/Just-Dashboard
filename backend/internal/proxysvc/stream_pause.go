package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Pausing a stream.
//
// nginx reads the stream directory through `include stream.d/*.conf`, a glob
// that does not descend into subdirectories. A stream moved into
// stream.d/paused/ is therefore out of nginx's configuration with its file
// intact: stopping a forward for an evening no longer means deleting the
// rule and typing it in again. Resume moves it back under the same checks a
// save meets — its port, the configuration test, and the reload binding it —
// and puts it back in paused/ when any of them refuses.

// StreamPaused is a stream kept in paused/, which nginx does not read.
const StreamPaused = "paused"

// streamPausedReason is the listing's sentence for a paused stream.
const streamPausedReason = "Paused: the file is kept in paused/, which the stream directory's include does not reach, so nginx does not read it. Resume puts it back."

// ErrStreamLinkPause refuses to pause a symbolic link: moved into paused/, a
// relative link points somewhere else, and an available/enabled layout
// pauses a stream by removing its link, which delete already does.
var ErrStreamLinkPause = errors.New("this stream is a symbolic link — delete the link to stop it; what it points to stays")

func (s *Service) pausedStreamDir() string { return filepath.Join(s.streamDir(), "paused") }

// pausedStreamFile is the file of a paused stream, found by the name the
// listing gave it, under the same rules as streamFile. A paused symbolic link
// is not made here, so only a regular file is taken.
func (s *Service) pausedStreamFile(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") ||
		strings.ContainsAny(name, "/\\\x00") {
		return "", fmt.Errorf("invalid stream name")
	}
	path := filepath.Join(s.pausedStreamDir(), name+".conf")
	st, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrStreamNotFound, name)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file — change it by hand", path)
	}
	return path, nil
}

// SetStreamEnabled pauses a stream (enabled false) or resumes a paused one.
//
// Both move the file, test the whole configuration when nginx reads the
// stream directory, and reload; a failing test moves the file back. A pause
// can fail the test only where the directory is included by a glob that
// takes the paused/ directory itself, which nginx cannot read as a file.
//
// Resume is refused when the name has been taken since, or when another
// stream, a site or another program has the port (PortInUseError), and a
// reload nginx fails on the stream's own port puts it back in paused/ with
// nginx's own words, as a save does: a stream nginx cannot bind makes every
// later reload on the host fail.
func (s *Service) SetStreamEnabled(ctx context.Context, name string, enabled bool) (*StreamResult, error) {
	var files []ConfigFile
	var view *socketView
	if enabled {
		// Read before the lock, as a save does: finding the running nginx
		// walks /proc, and the lock holds up every other save on the host.
		files, _ = readConfigFiles(s.nginxDir)
		view = readNginx(ctx, s, files, 0)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	live := filepath.Join(s.streamDir(), name+".conf")
	var from, to string
	if enabled {
		path, err := s.pausedStreamFile(name)
		if err != nil {
			return nil, err
		}
		from, to = path, live
	} else {
		path, linked, err := s.streamFile(name)
		if err != nil {
			return nil, err
		}
		if linked {
			return nil, ErrStreamLinkPause
		}
		from, to = path, filepath.Join(s.pausedStreamDir(), name+".conf")
	}
	if _, err := os.Lstat(to); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrStreamExists, name)
	}
	b, err := os.ReadFile(from)
	if err != nil {
		return nil, err
	}
	content := string(b)
	parsed := parseStreamFile(name+".conf", content)
	spec := parsed.spec

	if enabled {
		// The file's own sockets, not the form's reading of it: a paused
		// file may be hand-written with more than one listen.
		files, _ = readConfigFiles(s.nginxDir)
		claims := s.portClaims(live, files, view)
		for _, bound := range parsed.binds {
			if refused := claims.holder(ctx, bound, nil); refused != nil {
				if spec.Listen > 0 {
					refused.Suggest = claims.freePort(ctx, &spec, nil)
				}
				return nil, refused
			}
		}
	} else if err := os.MkdirAll(s.pausedStreamDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(from, to); err != nil {
		return nil, err
	}
	undo := func() {
		os.Rename(to, from)
		// An empty paused/ is removed so a directory glob never meets it.
		os.Remove(s.pausedStreamDir())
	}

	res := &StreamResult{Name: name, Path: to, Content: content, Warnings: []string{}}
	if streamDirRead(s.nginxDir, s.streamDir()) {
		res.Validation = runValidator(ctx, "nginx", "-t")
		if !res.Validation.Valid {
			undo()
			return res, ErrInvalidConf
		}
		// Watching the reload needs the sockets as the form reads them; a
		// file the form cannot read whole is reloaded without the watch.
		if enabled && spec.Listen > 0 && len(parsed.unsupported) == 0 {
			if refused := s.reloadAndWatch(ctx, res, &spec, nil); refused != nil {
				undo()
				return nil, refused
			}
		} else {
			res.Reloaded, res.Output, res.ReloadError = reloadNginx(ctx)
		}
	} else if enabled {
		res.ListenNote = "nginx does not read the stream directory, so the stream is back in it but not forwarding until the directory is connected."
	}
	if enabled {
		os.Remove(s.pausedStreamDir())
	}
	action := ChangeDisable
	if enabled {
		action = ChangeEnable
	}
	s.recordChange(ctx, Change{Path: live, Action: action, Before: b, BeforeExisted: true, After: b})
	return res, nil
}
