package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The assistant's saved chats: a deployment's, a new deployment's and the
// server's conversations, each its owner's alone. A press saves its chat after
// every step (saveChat), so a crash or a take-over loses nothing, and a chat that
// is not in memory is loaded back (loadChat) to be carried on. The conversation
// and what the page was shown hold the person's words and the server's answers,
// already masked, and are sealed with the install's master key as the
// assistant's keyring is.

const (
	// chatRetention is how many chats each person keeps; the oldest go when a
	// new one is saved.
	chatRetention = 200
	// chatListLimit is how many one list answers with, the newest first.
	chatListLimit = 100
	chatTitleMax  = 200
	// chatImageRetention is how many pasted images each person keeps whole, to
	// be opened large: some 30 MB of screenshots, at most 120. An older one
	// opens as the thumbnail its question keeps.
	chatImageRetention = 60
)

// chatImage is a pasted image kept whole with its chat, named by the id its
// question's line gives it.
type chatImage struct {
	ID string
	assistant.Image
}

// chatScope is what a chat is about, as the page reads it.
type chatScope struct {
	Kind        string `json:"kind"`
	Deployment  int64  `json:"deployment,omitempty"`
	Environment int64  `json:"environment,omitempty"`
	Draft       string `json:"draft,omitempty"`
	Database    int64  `json:"database,omitempty"`
	Name        string `json:"name"`
	// About is the page a chat about the server was opened from.
	About *assistant.ChatAbout `json:"about,omitempty"`
}

// chatSummary is a chat as the page lists it.
type chatSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Scope     chatScope `json:"scope"`
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	// Running is a press running it now, somewhere; Waiting, calls waiting on
	// the person's Allow or Decline.
	Running bool `json:"running"`
	Waiting bool `json:"waiting"`
	Steps   int  `json:"steps"`
	Changes int  `json:"changes"`
}

// saveChat writes a chat as it stands, and appends what the page was shown since
// it was last saved and the images pasted since. A chat saved for the first time
// may push its owner's oldest past chatRetention, and those go; an image may push
// their oldest images past chatImageRetention.
func (s *Server) saveChat(ctx context.Context, c *assistant.Conversation, lines []map[string]any, images ...chatImage) error {
	if c.Chat == nil || c.OwnerID == 0 {
		return errors.New("not a saved chat")
	}
	state, err := c.State()
	if err != nil {
		return err
	}
	sealedState, err := s.Sealer.Seal(string(state))
	if err != nil {
		return err
	}
	sealedTitle, err := s.Sealer.Seal(c.Chat.Title)
	if err != nil {
		return err
	}
	var sealedLines string
	if len(lines) > 0 {
		raw, err := json.Marshal(lines)
		if err != nil {
			return err
		}
		if sealedLines, err = s.Sealer.Seal(string(raw)); err != nil {
			return err
		}
	}
	sealedImages := make([]string, len(images))
	for i, img := range images {
		raw, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return err
		}
		if sealedImages[i], err = s.Sealer.Seal(string(raw)); err != nil {
			return err
		}
	}
	info := c.Chat
	info.Updated = time.Now().UTC()
	deployment := sql.NullInt64{Int64: info.Deployment, Valid: info.Deployment > 0}
	sealedAbout := ""
	if info.About != nil {
		raw, err := json.Marshal(info.About)
		if err != nil {
			return err
		}
		if sealedAbout, err = s.Sealer.Seal(string(raw)); err != nil {
			return err
		}
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existed int
	_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM assistant_chats WHERE id = ?`, c.ID).Scan(&existed)
	// The title is written when the chat begins and when it is renamed, never by
	// a press, which may have begun before the rename.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO assistant_chats(id, owner_id, scope_kind, deployment_id, environment_id, draft_id, scope_name, scope_key,
		  title_enc, provider, model, created_at, updated_at, steps, changes, waiting, conversation_enc, database_id, about_enc)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET scope_name = excluded.scope_name, provider = excluded.provider, model = excluded.model,
		  updated_at = excluded.updated_at, steps = excluded.steps, changes = excluded.changes, waiting = excluded.waiting,
		  conversation_enc = excluded.conversation_enc`,
		c.ID, c.OwnerID, info.Kind, deployment, info.Environment, info.Draft, info.Name, c.Connection,
		sealedTitle, info.Provider, info.Model, info.Created.Unix(), info.Updated.Unix(), info.Steps, info.Changes,
		len(c.Pending) > 0, sealedState, info.Database, sealedAbout); err != nil {
		return err
	}
	if sealedLines != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_chat_lines(chat_id, seq, lines_enc)
			VALUES(?, COALESCE((SELECT MAX(seq) + 1 FROM assistant_chat_lines WHERE chat_id = ?), 0), ?)`,
			c.ID, c.ID, sealedLines); err != nil {
			return err
		}
	}
	for i, img := range images {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO assistant_chat_images(chat_id, id, owner_id, created_at, media_type, data_enc)
			VALUES(?, ?, ?, ?, ?, ?)`, c.ID, img.ID, c.OwnerID, info.Updated.Unix(), img.MediaType, sealedImages[i]); err != nil {
			return err
		}
	}
	if len(images) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM assistant_chat_images WHERE rowid IN (
			SELECT rowid FROM assistant_chat_images WHERE owner_id = ?
			ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, c.OwnerID, chatImageRetention); err != nil {
			return err
		}
	}
	var gone []string
	if existed == 0 {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM assistant_chats WHERE owner_id = ?
			ORDER BY updated_at DESC, created_at DESC, id LIMIT -1 OFFSET ?`, c.OwnerID, chatRetention)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			// One a press is running stays: it is being carried on.
			if id != c.ID && s.assistant.operators.Forget(id) == nil {
				gone = append(gone, id)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM assistant_chats WHERE id = ?`, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// loadChat reads a saved chat back to be carried on: its conversation exactly as
// the provider was last sent it, with what it is offered set again from what it
// is about. A chat that is not saved is assistant.ErrGone.
func (s *Server) loadChat(ctx context.Context, id string) (*assistant.Conversation, error) {
	row := s.Store.DB.QueryRowContext(ctx, `SELECT owner_id, scope_kind, COALESCE(deployment_id, 0), environment_id, draft_id, scope_name,
		scope_key, title_enc, provider, model, created_at, updated_at, steps, changes, conversation_enc, database_id, about_enc
		FROM assistant_chats WHERE id = ?`, id)
	var (
		info                             assistant.ChatInfo
		owner, key                       int64
		created, updated                 int64
		sealedTitle, sealed, sealedAbout string
	)
	err := row.Scan(&owner, &info.Kind, &info.Deployment, &info.Environment, &info.Draft, &info.Name, &key, &sealedTitle,
		&info.Provider, &info.Model, &created, &updated, &info.Steps, &info.Changes, &sealed, &info.Database, &sealedAbout)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, assistant.ErrGone
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return nil, err
	}
	c, err := assistant.LoadConversation(id, []byte(plain))
	if err != nil {
		return nil, err
	}
	if title, err := s.Sealer.Open(sealedTitle); err == nil {
		info.Title = title
	}
	info.Created, info.Updated = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	info.About = s.openAbout(sealedAbout)
	c.OwnerID, c.Connection, c.Chat = owner, key, &info
	kind, _ := agentScopeNamed(info.Kind)
	agentOffer(c, kind)
	return c, nil
}

// chatRow is a chat's listed columns, read for its owner.
func (s *Server) chatRows(ctx context.Context, where string, args ...any) ([]chatSummary, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, scope_kind, COALESCE(deployment_id, 0), environment_id, draft_id, scope_name,
		title_enc, provider, model, created_at, updated_at, steps, changes, waiting, database_id, about_enc
		FROM assistant_chats WHERE `+where+` ORDER BY updated_at DESC, created_at DESC, id LIMIT ?`, append(args, chatListLimit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chatSummary{}
	for rows.Next() {
		var (
			c                        chatSummary
			sealedTitle, sealedAbout string
			created, updated         int64
		)
		if err := rows.Scan(&c.ID, &c.Scope.Kind, &c.Scope.Deployment, &c.Scope.Environment, &c.Scope.Draft, &c.Scope.Name,
			&sealedTitle, &c.Provider, &c.Model, &created, &updated, &c.Steps, &c.Changes, &c.Waiting,
			&c.Scope.Database, &sealedAbout); err != nil {
			return nil, err
		}
		c.Scope.About = s.openAbout(sealedAbout)
		if title, err := s.Sealer.Open(sealedTitle); err == nil {
			c.Title = title
		}
		c.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
		c.UpdatedAt = time.Unix(updated, 0).UTC().Format(time.RFC3339)
		c.Running = s.assistant.operators.Busy(c.ID)
		out = append(out, c)
	}
	return out, rows.Err()
}

// chatFor reads one chat of the person asking; anybody else's is not there.
func (s *Server) chatFor(r *http.Request) (chatSummary, error) {
	id := chi.URLParam(r, "chat")
	chats, err := s.chatRows(r.Context(), `id = ? AND owner_id = ?`, id, httpx.MustPrincipal(r).UserID())
	if err != nil {
		return chatSummary{}, httpx.Internal(err)
	}
	if len(chats) == 0 {
		return chatSummary{}, httpx.Err(http.StatusNotFound, "not_found", "there is no such chat of yours")
	}
	return chats[0], nil
}

// handleAssistantChats lists the person's chats, the newest first: all of them,
// one deployment's (?deployment=), one draft's (?draft=) or the server's
// (?scope=server).
func (s *Server) handleAssistantChats(w http.ResponseWriter, r *http.Request) error {
	where := []string{"owner_id = ?"}
	args := []any{httpx.MustPrincipal(r).UserID()}
	q := r.URL.Query()
	if raw := q.Get("deployment"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return httpx.BadRequest("deployment is a deployment's id")
		}
		where, args = append(where, "deployment_id = ?"), append(args, id)
	}
	if draft := q.Get("draft"); draft != "" {
		where, args = append(where, "draft_id = ?"), append(args, draft)
	}
	if raw := q.Get("database"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return httpx.BadRequest("database is a database connection's id")
		}
		where, args = append(where, "scope_kind = 'database' AND database_id = ?"), append(args, id)
	}
	if scope := q.Get("scope"); scope != "" {
		if _, ok := agentScopeNamed(scope); !ok {
			return httpx.BadRequest("scope is server, deployment, draft or database")
		}
		where, args = append(where, "scope_kind = ?"), append(args, scope)
	}
	chats, err := s.chatRows(r.Context(), strings.Join(where, " AND "), args...)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"chats": chats})
	return nil
}

// handleAssistantChat is one chat and what the page was shown in it, in order,
// with the person's questions and answers among it: replayed, it is the chat as
// it looked.
func (s *Server) handleAssistantChat(w http.ResponseWriter, r *http.Request) error {
	chat, err := s.chatFor(r)
	if err != nil {
		return err
	}
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT lines_enc FROM assistant_chat_lines WHERE chat_id = ? ORDER BY seq`, chat.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	lines := []json.RawMessage{}
	for rows.Next() {
		var sealed string
		if err := rows.Scan(&sealed); err != nil {
			return httpx.Internal(err)
		}
		plain, err := s.Sealer.Open(sealed)
		if err != nil {
			return errAssistantSealed
		}
		var batch []json.RawMessage
		if err := json.Unmarshal([]byte(plain), &batch); err != nil {
			return httpx.Internal(err)
		}
		lines = append(lines, batch...)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"chat": chat, "lines": lines})
	return nil
}

// handleAssistantChatImage is an image pasted into one of the person's chats,
// whole, for the page to open large. One past chatImageRetention is gone, and
// the page opens the thumbnail its question keeps.
func (s *Server) handleAssistantChatImage(w http.ResponseWriter, r *http.Request) error {
	chat, err := s.chatFor(r)
	if err != nil {
		return err
	}
	var mediaType, sealed string
	err = s.Store.DB.QueryRowContext(r.Context(), `SELECT media_type, data_enc FROM assistant_chat_images WHERE chat_id = ? AND id = ?`,
		chat.ID, chi.URLParam(r, "image")).Scan(&mediaType, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.Err(http.StatusNotFound, "not_found", "only a small copy of this image is kept")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return errAssistantSealed
	}
	// Only the formats CheckImage let in were kept, and each was the format it
	// named; nothing here is drawn as anything else.
	if !slices.Contains(assistant.ImageTypes, mediaType) {
		return httpx.Internal(fmt.Errorf("a kept image is %q", mediaType))
	}
	h := w.Header()
	h.Set("Content-Type", mediaType)
	h.Set("Content-Length", strconv.Itoa(len(plain)))
	// An image never changes under its id; it is the person's own.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, plain)
	return nil
}

// handleAssistantChatRename renames a chat.
func (s *Server) handleAssistantChatRename(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title string `json:"title"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	title := strings.Join(strings.Fields(in.Title), " ")
	if title == "" || utf8.RuneCountInString(title) > chatTitleMax || !utf8.ValidString(title) {
		return httpx.BadRequest("a chat's title is 1 to %d characters", chatTitleMax)
	}
	chat, err := s.chatFor(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "assistant.chat.rename", chat.ID[:min(8, len(chat.ID))], nil)
	sealed, err := s.Sealer.Seal(title)
	if err != nil {
		return httpx.Internal(err)
	}
	if _, err := s.Store.DB.ExecContext(r.Context(), `UPDATE assistant_chats SET title_enc = ? WHERE id = ? AND owner_id = ?`,
		sealed, chat.ID, httpx.MustPrincipal(r).UserID()); err != nil {
		return httpx.Internal(err)
	}
	s.assistant.operators.Retitle(chat.ID, title)
	chat.Title = title
	httpx.JSON(w, http.StatusOK, map[string]any{"chat": chat})
	return nil
}

// handleAssistantChatDelete deletes a chat and what it was shown. One a press is
// running is refused: the press would save it again.
func (s *Server) handleAssistantChatDelete(w http.ResponseWriter, r *http.Request) error {
	chat, err := s.chatFor(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "assistant.chat.delete", chat.ID[:min(8, len(chat.ID))], nil)
	if err := s.assistant.operators.Forget(chat.ID); errors.Is(err, assistant.ErrBusy) {
		return httpx.Err(http.StatusConflict, "chat_busy", "this chat is answering; stop it before deleting it")
	}
	if _, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM assistant_chats WHERE id = ? AND owner_id = ?`,
		chat.ID, httpx.MustPrincipal(r).UserID()); err != nil {
		return httpx.Internal(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// openAbout reads a chat's sealed page back, nil when there is none or it does
// not open.
func (s *Server) openAbout(sealed string) *assistant.ChatAbout {
	if sealed == "" {
		return nil
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return nil
	}
	var about assistant.ChatAbout
	if json.Unmarshal([]byte(plain), &about) != nil || about.Path == "" {
		return nil
	}
	return &about
}
