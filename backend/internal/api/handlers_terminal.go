package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountTerminalRoutes(r chi.Router) {
	r.Route("/terminal", func(r chi.Router) {
		// The entire terminal surface, listing included, requires the
		// terminal capability: knowing which shells are open is itself
		// information a read-only account has no business with.
		r.Use(httpx.RequireCapability(auth.CapTerminal))
		r.Method(http.MethodGet, "/", s.handle(s.handleTerminalList))
		r.Method(http.MethodPost, "/", s.handle(s.handleTerminalCreate))
		r.Method(http.MethodGet, "/{id}/attach", s.handle(s.handleTerminalAttach))
		r.Method(http.MethodPost, "/{id}/clipboard", s.handle(s.handleTerminalClipboardUpload))

		// Naming and filing address the in-memory workspace rather than an
		// individual window, so every direct PTY in the workspace stays grouped.
		r.Method(http.MethodPatch, "/{id}", s.handle(s.handleTerminalMeta))

		// Folders are the dashboard's own record, but remain part of this
		// surface so the terminal route map stays in one place.
		s.mountTerminalFolderRoutes(r)

		// Each window is an independent direct PTY grouped by the workspace id.
		r.Method(http.MethodGet, "/{id}/windows", s.handle(s.handleTerminalWindows))
		r.Method(http.MethodPost, "/{id}/windows", s.handle(s.handleTerminalWindowCreate))
		r.Method(http.MethodPatch, "/{id}/windows/{window}", s.handle(s.handleTerminalWindowUpdate))
		// Closing a window or session is destructive — it takes
		// whatever is running with it — and stays inside `s.destructive` for
		// the capability check, the tighter budget and the audit entry. It
		// deliberately carries **no typed phrase**, which is the one place in
		// this API that combination appears.
		//
		// The phrase exists so that an irreversible action cannot be a
		// mis-click. That reasoning holds for deleting a container or a backup,
		// which somebody does a handful of times a year. Closing a shell is an
		// everyday act — a dozen a day for anyone using this panel as intended
		// — and a phrase in front of an everyday act does not get read, it gets
		// typed. Training the operator to type "close terminal" without looking
		// is worse than no guard at all, because it is exactly the habit the
		// typed confirmation exists to prevent everywhere it still applies.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/windows/{window}", s.handle(s.handleTerminalWindowKill))
		})

		s.destructive(r, func(r chi.Router) {
			// Killing a session takes whatever is running in it with it. No
			// typed phrase, for the reason given above the window route.
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleTerminalKill))
		})
		r.Method(http.MethodGet, "/{id}/cwd", s.handle(s.handleTerminalCWD))
	})
}

func mapTermError(err error) error {
	switch {
	case errors.Is(err, term.ErrDisabled):
		return httpx.Err(http.StatusServiceUnavailable, "terminal_disabled", err.Error())
	case errors.Is(err, term.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, term.ErrTooMany):
		return httpx.Err(http.StatusTooManyRequests, "too_many_sessions", err.Error())
	case errors.Is(err, term.ErrNoPersistence):
		return httpx.Err(http.StatusConflict, "not_persistent", err.Error())
	case errors.Is(err, term.ErrSessionOwner):
		return httpx.Err(http.StatusForbidden, "terminal_session_owner", err.Error())
	case errors.Is(err, term.ErrClipboardType):
		return httpx.Err(http.StatusUnsupportedMediaType, "unsupported_image_type",
			"only PNG, JPEG and WebP clipboard images are supported")
	case errors.Is(err, term.ErrClipboardTooLarge):
		return httpx.Err(http.StatusRequestEntityTooLarge, "image_too_large",
			"clipboard images are limited to 20 MB")
	case errors.Is(err, term.ErrClipboardSessionID):
		return httpx.BadRequest("invalid terminal session id")
	default:
		return httpx.Internal(err)
	}
}

// workspace is one terminal as the operator thinks of it: a named, filed group
// of direct PTYs. Every workspace in the response is live and process-local.
type workspace struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Named is whether the operator chose Title. A default title gives way in
	// the rail to what the session is doing, a chosen one does not.
	Named     bool      `json:"named"`
	Folder    string    `json:"folder,omitempty"`
	Favourite bool      `json:"favourite"`
	Live      bool      `json:"live"`
	CWD       string    `json:"cwd,omitempty"`
	Windows   int       `json:"windows"`
	CreatedAt time.Time `json:"createdAt"`
	Attached  int       `json:"attached"`
	User      string    `json:"user,omitempty"`
	Shell     string    `json:"shell,omitempty"`
	Owner     string    `json:"owner,omitempty"`
	// Busy is whether any window has a program in the foreground, and Current
	// is the window that stands for the session — the one a browser last
	// showed, or failing that the one opened most recently — so the rail can
	// name a session after what is going on inside it.
	Busy bool `json:"busy"`
	// Working and FinishedAt aggregate the windows the same way: something in
	// the session is working, or the most recent thing in it to finish.
	Working    bool           `json:"working"`
	FinishedAt int64          `json:"finishedAt,omitempty"`
	Current    *sessionWindow `json:"current,omitempty"`
}

// sessionWindow is one direct PTY as the rail and the window strip see it: its
// name, whether that name was chosen, and what it is doing right now.
type sessionWindow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Named bool   `json:"named"`
	term.Activity
}

// windowView adds what the strip needs beyond the name: position and directory.
type windowView struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Name  string `json:"name"`
	Named bool   `json:"named"`
	CWD   string `json:"cwd,omitempty"`
	term.Activity
}

func (s *Server) handleTerminalList(w http.ResponseWriter, r *http.Request) error {
	byID := map[string]*workspace{}
	focused := map[string]time.Time{}
	out := []*workspace{}
	for _, sess := range s.modules.term.List() {
		// Persistent tmux sessions are no longer part of the product. Existing
		// ones are left untouched on the host, but are not adopted into this
		// direct-PTY interface.
		if sess.TmuxName != "" {
			continue
		}
		id := sess.WorkspaceID
		if id == "" {
			id = sess.ID
		}
		ws := byID[id]
		if ws == nil {
			meta := sess.Meta()
			ws = &workspace{
				ID: id, Title: meta.Title, Named: meta.Named, Folder: meta.Folder,
				Favourite: meta.Favourite, Live: true, CreatedAt: sess.CreatedAt,
				User: sess.User, Shell: sess.Shell, Owner: sess.Owner,
			}
			byID[id] = ws
			out = append(out, ws)
		}
		ws.Windows++
		ws.Attached += sess.Attached()
		if ws.CWD == "" {
			ws.CWD = sess.CWD()
		}
		window := sessionWindow{
			ID: sess.ID, Name: sess.WindowName, Named: sess.WindowNamed(), Activity: sess.Activity(),
		}
		ws.Busy = ws.Busy || window.Busy
		ws.Working = ws.Working || window.Working
		if window.FinishedAt > ws.FinishedAt {
			ws.FinishedAt = window.FinishedAt
		}
		if at := sess.FocusedAt(); ws.Current == nil || at.After(focused[id]) {
			ws.Current = &window
			focused[id] = at
		}
	}

	// Who a new session will belong to, and where it will start. The page
	// says so before you open one: a root-equivalent shell is not somewhere to
	// discover your identity by running whoami.
	account, accountErr := s.modules.term.Account()
	login := map[string]any{"user": account.Name, "home": account.Home, "shell": account.Shell}
	if accountErr != nil {
		login["error"] = accountErr.Error()
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"enabled": s.modules.term.Enabled(),
		"login":   login,
		// The folders come with the listing rather than from a second
		// request, because the rail cannot be drawn without both and two
		// polls would render a session in a folder that has not arrived yet
		// — the same class of flicker this endpoint was collapsed into one
		// list to remove.
		"folders":  s.mergedTerminalFolders(r.Context(), out),
		"sessions": out,
	})
	return nil
}

type createTerminalRequest struct {
	Title  string `json:"title"`
	CWD    string `json:"cwd"`
	Folder string `json:"folder"`
	Rows   uint16 `json:"rows"`
	Cols   uint16 `json:"cols"`
}

func (s *Server) handleTerminalCreate(w http.ResponseWriter, r *http.Request) error {
	var req createTerminalRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	sess, err := s.modules.term.Create(r.Context(), term.CreateOptions{
		Title: req.Title, Owner: p.Username(), CWD: req.CWD, Folder: req.Folder,
		Rows: req.Rows, Cols: req.Cols,
	})
	if err != nil {
		return mapTermError(err)
	}
	if s.Log != nil {
		rows, cols := sess.Size()
		s.Log.Debug("terminal PTY created", "session", sess.ID, "rows", rows, "cols", cols)
	}
	// The account is the part of this record that matters later: "a shell was
	// opened" and "a shell was opened as root" are different events.
	httpx.SetAudit(r, "terminal.create", sess.ID,
		map[string]any{"shell": sess.Shell, "user": sess.User, "cwd": req.CWD})
	meta := sess.Meta()
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id": sess.WorkspaceID, "windowId": sess.ID, "title": meta.Title,
		"shell": sess.Shell, "user": sess.User, "pid": sess.PID, "folder": meta.Folder,
	})
	return nil
}

type terminalControl struct {
	Type string `json:"type"`
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
	Data string `json:"data"`
}

// handleTerminalAttach wires a browser to a PTY. Binary frames carry raw
// terminal bytes in both directions; JSON frames carry control messages. The
// session is written to the audit trail at attach time, because a shell that
// crashes the process must still have left a record that it was opened.
func (s *Server) handleTerminalAttach(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	sess, err := s.modules.term.Get(id)
	if err != nil {
		return mapTermError(err)
	}
	if sess.TmuxName != "" {
		return httpx.ErrNotFound
	}
	s.recordAudit(r, "terminal.attach", id,
		map[string]any{"shell": sess.Shell, "user": sess.User})

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)

	// Subscribe before touching the PTY size. TIOCSWINSZ may make the program
	// redraw immediately; subscribing afterwards loses those bytes and
	// leaves the new emulator with an already-stale picture.
	snapshot, subID, out, err := sess.Subscribe()
	if err != nil {
		conn.SendError(err.Error())
		return nil
	}
	defer sess.Unsubscribe(subID)

	// xterm puts its measured geometry on the handshake. Apply it after the
	// subscription exists but before sending any stored bytes, so every redraw
	// caused by the resize is queued behind a coherent starting point.
	if rows, cols, ok := terminalSizeQuery(r); ok {
		if err := sess.SynchronizeSize(rows, cols); err != nil {
			conn.SendError("could not resize terminal")
			return nil
		}
		if s.Log != nil {
			s.Log.Debug("terminal PTY synchronized", "session", id, "rows", rows, "cols", cols)
		}
	}

	// Direct sessions have no independent screen model, so reconnect retains
	// the historical best-effort replay used for ordinary shell scrollback.
	if len(snapshot) > 0 {
		// Announced before it is sent, because the browser has to know that
		// what follows is a replay rather than live output.
		//
		// A terminal emulator answers some of what it is written: `CSI c` and
		// friends are questions the shell asks the terminal, and xterm replies
		// down the same channel a keystroke uses. Replaying a scrollback that
		// contains one makes it answer a question that was already answered —
		// and the answer lands at whatever prompt is there now, which is how
		// reopening a tab typed `1;2c0;276` into the shell and left a column
		// of "command not found". The client suppresses its replies for the
		// duration of the replay; it cannot work that out on its own, because
		// from the browser's side the bytes are identical either way.
		if err := conn.Send("scrollback", map[string]any{"bytes": len(snapshot)}); err != nil {
			return nil
		}
		if err := conn.WriteBinary(snapshot); err != nil {
			return nil
		}
	}
	// What the window is doing, stated once on arrival and then whenever it
	// changes, so a tab can be titled and marked busy without waiting for the
	// next listing poll.
	if err := conn.Send("state", sess.Activity()); err != nil {
		return nil
	}
	events := sess.Events(subID)
	go func() {
		for {
			select {
			case chunk, ok := <-out:
				if !ok {
					cancel()
					return
				}
				if err := conn.WriteBinary(chunk); err != nil {
					cancel()
					return
				}
			case activity, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if err := conn.Send("state", activity); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if kind == websocketTextFrame && len(data) > 0 && data[0] == '{' {
			var ctrl terminalControl
			if json.Unmarshal(data, &ctrl) == nil {
				switch ctrl.Type {
				case "resize":
					changed, resizeErr := sess.Resize(ctrl.Rows, ctrl.Cols)
					if resizeErr != nil {
						_ = conn.SendError("could not resize terminal")
						continue
					}
					if changed && s.Log != nil {
						s.Log.Debug("terminal PTY resized", "session", id, "rows", ctrl.Rows, "cols", ctrl.Cols)
					}
					continue
				case "input":
					sess.Write([]byte(ctrl.Data))
					continue
				case "focus":
					// The browser is showing this window. Which window a
					// session is "on" is otherwise unknowable here: a visited
					// window keeps its socket while hidden, so attaching says
					// nothing about what is on screen.
					sess.MarkFocused()
					continue
				case "ping":
					continue
				}
			}
		}
		if _, err := sess.Write(data); err != nil {
			break
		}
	}
	s.recordAudit(r, "terminal.detach", id, map[string]any{"attached": sess.Attached()})
	return nil
}

func terminalSizeQuery(r *http.Request) (rows, cols uint16, ok bool) {
	parsedRows, errRows := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)
	parsedCols, errCols := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
	if errRows != nil || errCols != nil || parsedRows == 0 || parsedCols == 0 {
		return 0, 0, false
	}
	return uint16(parsedRows), uint16(parsedCols), true
}

// gorilla's TextMessage constant, kept local so handlers do not need to import
// the websocket package directly.
const websocketTextFrame = 1

// handleTerminalClipboardUpload moves an image out-of-band rather than
// feeding binary clipboard data through the PTY. The destination is derived
// entirely from the live session and a random server-side name; the multipart
// filename is display metadata only and never participates in a filesystem
// path.
func (s *Server) handleTerminalClipboardUpload(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	sess, err := s.modules.term.Get(id)
	if err != nil || sess.TmuxName != "" {
		return httpx.Err(http.StatusNotFound, "terminal_session_not_found",
			"the terminal session no longer exists")
	}
	const multipartAllowance = 1 << 20
	r.Body = http.MaxBytesReader(w, r.Body, term.MaxClipboardImageBytes+multipartAllowance)
	reader, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("expected a multipart image upload: %v", err)
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return mapTermError(term.ErrClipboardTooLarge)
			}
			return httpx.BadRequest("malformed clipboard upload: %v", err)
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}

		declaredMIME, _, mimeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if mimeErr != nil {
			part.Close()
			return mapTermError(term.ErrClipboardType)
		}
		originalName := clipboardDisplayName(part.FileName(), "clipboard")
		file, saveErr := s.modules.term.SaveClipboard(
			chi.URLParam(r, "id"), httpx.MustPrincipal(r).Username(), declaredMIME, part,
		)
		part.Close()
		if saveErr != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(saveErr, &tooLarge) {
				return mapTermError(term.ErrClipboardTooLarge)
			}
			if errors.Is(saveErr, term.ErrNotFound) {
				return httpx.Err(http.StatusNotFound, "terminal_session_not_found",
					"the terminal session no longer exists")
			}
			return mapTermError(saveErr)
		}

		if originalName == "clipboard" {
			originalName = "clipboard" + filepath.Ext(file.Path)
		}
		httpx.SetAudit(r, "terminal.clipboard.upload", chi.URLParam(r, "id"), map[string]any{
			"path": file.Path, "mime": file.MIME, "bytes": file.Size,
		})
		httpx.JSON(w, http.StatusCreated, map[string]any{
			"path": file.Path, "name": originalName, "mime": file.MIME, "size": file.Size,
		})
		return nil
	}
	return httpx.BadRequest("no image file found in the upload")
}

// clipboardDisplayName is UI metadata, never a destination. Keeping it
// bounded and free of control characters prevents a crafted multipart header
// from turning a small toast into misleading terminal-like output.
func clipboardDisplayName(raw, fallback string) string {
	name := filepath.Base(strings.ReplaceAll(raw, `\`, "/"))
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name))
	if name == "" || name == "." {
		return fallback
	}
	runes := []rune(name)
	if len(runes) > 120 {
		name = string(runes[:120])
	}
	return name
}

func (s *Server) handleTerminalKill(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	if err := s.modules.term.KillWorkspace(r.Context(), id); err != nil {
		return mapTermError(err)
	}
	httpx.SetAudit(r, "terminal.kill", id, nil)
	httpx.NoContent(w)
	return nil
}

// handleTerminalCWD lets the frontend anchor in-session upload and download to
// wherever the shell has navigated, which is what makes those actions feel
// like they belong to the terminal rather than to the file manager.
func (s *Server) handleTerminalCWD(w http.ResponseWriter, r *http.Request) error {
	sess, err := s.modules.term.Get(chi.URLParam(r, "id"))
	if err != nil {
		return mapTermError(err)
	}
	if sess.TmuxName != "" {
		return httpx.ErrNotFound
	}
	cwd := sess.CWD()
	if cwd == "" {
		return httpx.Err(http.StatusServiceUnavailable, "cwd_unavailable",
			"cannot determine the session's working directory")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cwd": cwd, "checkedAt": time.Now().UTC()})
	return nil
}

// handleTerminalMeta renames a session and files it away.
//
// A rename is not a destructive action and deliberately carries no typed
// confirmation: the whole point is that naming a session should be as cheap as
// naming a browser tab, or nobody does it and every session stays
// `vpsd-3f2a91c4`.
// Every field is a pointer so that an omitted one means "leave it alone"
// rather than "set it to empty". Dragging a session into a folder sends the
// folder and nothing else, and a request shaped the other way would quietly
// erase the name the operator had given it.
type sessionMetaRequest struct {
	Title     *string `json:"title"`
	Folder    *string `json:"folder"`
	Favourite *bool   `json:"favourite"`
}

func (s *Server) handleTerminalMeta(w http.ResponseWriter, r *http.Request) error {
	var req sessionMetaRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	id := chi.URLParam(r, "id")
	if len(s.modules.term.Workspace(id)) == 0 {
		return httpx.ErrNotFound
	}
	meta, err := s.modules.term.Meta(r.Context(), id)
	if err != nil {
		return mapTermError(err)
	}
	if req.Title != nil {
		meta.Title = *req.Title
		// A title somebody typed is theirs from now on: the rail stops
		// following the shell for this session and shows what they wrote.
		if strings.TrimSpace(*req.Title) != "" {
			meta.Named = true
		}
	}
	if req.Folder != nil {
		meta.Folder = *req.Folder
	}
	if req.Favourite != nil {
		meta.Favourite = *req.Favourite
	}
	if err := s.modules.term.SetMeta(r.Context(), id, meta); err != nil {
		return mapTermError(err)
	}
	httpx.SetAudit(r, "terminal.rename", id, map[string]any{
		"title": meta.Title, "folder": meta.Folder,
		"favourite": meta.Favourite,
	})
	httpx.JSON(w, http.StatusOK, meta)
	return nil
}

func (s *Server) handleTerminalWindows(w http.ResponseWriter, r *http.Request) error {
	sessions := s.modules.term.Workspace(chi.URLParam(r, "id"))
	if len(sessions) == 0 {
		return httpx.ErrNotFound
	}
	windows := make([]windowView, 0, len(sessions))
	for index, sess := range sessions {
		windows = append(windows, windowView{
			ID: sess.ID, Index: index, Name: sess.WindowName, Named: sess.WindowNamed(),
			CWD: sess.CWD(), Activity: sess.Activity(),
		})
	}
	httpx.JSON(w, http.StatusOK, windows)
	return nil
}

func (s *Server) handleTerminalWindowCreate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
		CWD  string `json:"cwd"`
	}
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			return err
		}
	}
	id := chi.URLParam(r, "id")
	sess, err := s.modules.term.NewDirectWindow(r.Context(), id, req.Name, req.CWD, 30, 110)
	if err != nil {
		return mapTermError(err)
	}
	httpx.SetAudit(r, "terminal.window.create", id, map[string]any{"name": sess.WindowName})
	httpx.JSON(w, http.StatusCreated, map[string]any{"id": sess.ID, "name": sess.WindowName})
	return nil
}

// handleTerminalWindowUpdate renames or reorders a direct-PTY window.
func (s *Server) handleTerminalWindowUpdate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name,omitempty"`
		// Position is where a drag dropped the window in the strip.
		Position *int `json:"position,omitempty"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	id := chi.URLParam(r, "id")
	windowID := chi.URLParam(r, "window")
	if req.Name != "" {
		if err := s.modules.term.RenameDirectWindow(id, windowID, req.Name); err != nil {
			return mapTermError(err)
		}
	}
	if req.Position != nil {
		if err := s.modules.term.MoveDirectWindow(id, windowID, *req.Position); err != nil {
			return mapTermError(err)
		}
	}
	httpx.SetAudit(r, "terminal.window.update", id, map[string]any{
		"window": windowID, "name": req.Name, "position": req.Position,
	})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleTerminalWindowKill(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	windowID := chi.URLParam(r, "window")
	if err := s.modules.term.KillDirectWindow(r.Context(), id, windowID); err != nil {
		// "This is the only window" is a sentence the operator needs, not a
		// 500 — it names the route they should have taken instead.
		if errors.Is(err, term.ErrNotFound) {
			return httpx.ErrNotFound
		}
		return httpx.BadRequest("%s", err.Error())
	}
	httpx.SetAudit(r, "terminal.window.kill", id, map[string]any{"window": windowID})
	httpx.NoContent(w)
	return nil
}
