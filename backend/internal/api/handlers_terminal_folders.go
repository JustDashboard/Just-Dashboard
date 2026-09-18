package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/go-chi/chi/v5"
)

// Folders are the dashboard's ordered record in the settings table, while
// membership stays on each in-memory workspace. The two are reconciled on
// read so a workspace cannot become unreachable if the folder record is stale.
const terminalFoldersKey = "terminal.folders"

type terminalFolder struct {
	Name string `json:"name"`
	// Collapsed is remembered on the server rather than in the browser
	// because it describes the work, not the screen: a folder of finished
	// deployments should still be folded on the operator's phone.
	Collapsed bool `json:"collapsed,omitempty"`
}

// terminalFolders reads the stored record. Order is the array's own — a folder
// list is short and hand-arranged, and a rank column would be a second thing
// to keep consistent for no gain.
func (s *Server) terminalFolders(ctx context.Context) []terminalFolder {
	out := []terminalFolder{}
	raw, ok, err := s.Store.Setting(ctx, terminalFoldersKey)
	if err != nil || !ok || raw == "" {
		return out
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return []terminalFolder{}
	}
	return out
}

func (s *Server) saveTerminalFolders(ctx context.Context, folders []terminalFolder) error {
	clean := make([]terminalFolder, 0, len(folders))
	seen := map[string]bool{}
	for _, f := range folders {
		name := term.SanitiseName(f.Name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		clean = append(clean, terminalFolder{Name: name, Collapsed: f.Collapsed})
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	return s.Store.SetSetting(ctx, terminalFoldersKey, string(encoded))
}

// mergedTerminalFolders is the stored record plus any folder a session names
// that the record has forgotten. The stored order is kept and the discovered
// ones are appended, so reordering is stable and nothing disappears.
func (s *Server) mergedTerminalFolders(ctx context.Context, sessions []*workspace) []terminalFolder {
	folders := s.terminalFolders(ctx)
	seen := map[string]bool{}
	for _, f := range folders {
		seen[f.Name] = true
	}
	for _, ws := range sessions {
		if ws.Folder != "" && !seen[ws.Folder] {
			seen[ws.Folder] = true
			folders = append(folders, terminalFolder{Name: ws.Folder})
		}
	}
	return folders
}

func (s *Server) mountTerminalFolderRoutes(r chi.Router) {
	r.Route("/folders", func(r chi.Router) {
		r.Method(http.MethodPost, "/", s.handle(s.handleTerminalFolderCreate))
		r.Method(http.MethodPut, "/", s.handle(s.handleTerminalFolderOrder))
		r.Method(http.MethodPatch, "/{name}", s.handle(s.handleTerminalFolderUpdate))
		r.Method(http.MethodDelete, "/{name}", s.handle(s.handleTerminalFolderDelete))
	})
}

func (s *Server) handleTerminalFolderCreate(w http.ResponseWriter, r *http.Request) error {
	var req terminalFolder
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name := term.SanitiseName(req.Name)
	if name == "" {
		return httpx.BadRequest("a folder name is required")
	}
	folders := s.terminalFolders(r.Context())
	for _, f := range folders {
		if strings.EqualFold(f.Name, name) {
			return httpx.Err(http.StatusConflict, "folder_exists", fmt.Sprintf("a folder called %q already exists", name))
		}
	}
	folders = append(folders, terminalFolder{Name: name})
	if err := s.saveTerminalFolders(r.Context(), folders); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "terminal.folder.create", name, nil)
	httpx.JSON(w, http.StatusCreated, terminalFolder{Name: name})
	return nil
}

// handleTerminalFolderOrder replaces the whole record, which is what a drag
// that reorders the rail produces: the client already holds the arrangement it
// wants, and sending it whole avoids a per-folder rank that two concurrent
// drags could interleave into a contradiction.
func (s *Server) handleTerminalFolderOrder(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Folders []terminalFolder `json:"folders"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.saveTerminalFolders(r.Context(), req.Folders); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "terminal.folder.order", "", map[string]any{"count": len(req.Folders)})
	httpx.JSON(w, http.StatusOK, s.terminalFolders(r.Context()))
	return nil
}

// handleTerminalFolderUpdate renames or collapses a folder.
//
// A rename has to move every session filed under the old name, and that is
// done here rather than as a loop in the browser: a page that renames a folder
// by issuing eight requests leaves four sessions in a folder that no longer
// exists if the tab is closed halfway.
func (s *Server) handleTerminalFolderUpdate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name      *string `json:"name"`
		Collapsed *bool   `json:"collapsed"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	current := chi.URLParam(r, "name")
	folders := s.terminalFolders(r.Context())

	renamed := current
	if req.Name != nil {
		renamed = term.SanitiseName(*req.Name)
		if renamed == "" {
			return httpx.BadRequest("a folder name is required")
		}
	}
	found := false
	for i, f := range folders {
		if !strings.EqualFold(f.Name, current) {
			if req.Name != nil && strings.EqualFold(f.Name, renamed) {
				return httpx.Err(http.StatusConflict, "folder_exists",
					fmt.Sprintf("a folder called %q already exists", renamed))
			}
			continue
		}
		found = true
		folders[i].Name = renamed
		if req.Collapsed != nil {
			folders[i].Collapsed = *req.Collapsed
		}
	}
	if !found {
		// A folder that exists only because sessions name it is still a
		// folder the operator can rename; the record simply has to start
		// holding it.
		folders = append(folders, terminalFolder{Name: renamed})
	}
	if err := s.saveTerminalFolders(r.Context(), folders); err != nil {
		return httpx.Internal(err)
	}

	moved := 0
	if renamed != current {
		moved = s.refileSessions(r, current, renamed)
	}
	httpx.SetAudit(r, "terminal.folder.update", current,
		map[string]any{"name": renamed, "sessions": moved})
	httpx.JSON(w, http.StatusOK, map[string]any{"name": renamed, "sessions": moved})
	return nil
}

// handleTerminalFolderDelete drops the folder and unfiles what was in it.
//
// Not destructive in the sense the rest of this API uses the word: no session
// ends and nothing running is touched, so it carries no typed confirmation.
// What it costs is the grouping, which the operator can rebuild by dragging.
func (s *Server) handleTerminalFolderDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	folders := s.terminalFolders(r.Context())
	kept := make([]terminalFolder, 0, len(folders))
	for _, f := range folders {
		if !strings.EqualFold(f.Name, name) {
			kept = append(kept, f)
		}
	}
	if err := s.saveTerminalFolders(r.Context(), kept); err != nil {
		return httpx.Internal(err)
	}
	moved := s.refileSessions(r, name, "")
	httpx.SetAudit(r, "terminal.folder.delete", name, map[string]any{"sessions": moved})
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": moved})
	return nil
}

// refileSessions moves every session filed under one folder to another. Best
// effort per session: one that has gone away
// between the listing and the write must not stop the rest from moving.
func (s *Server) refileSessions(r *http.Request, from, to string) int {
	moved := 0
	for name, meta := range s.modules.term.WorkspaceMeta() {
		if meta.Folder == "" || !strings.EqualFold(meta.Folder, from) {
			continue
		}
		meta.Folder = to
		if err := s.modules.term.SetMeta(r.Context(), name, meta); err == nil {
			moved++
		}
	}
	return moved
}
