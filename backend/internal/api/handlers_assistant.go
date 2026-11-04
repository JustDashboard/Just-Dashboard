package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The providers the assistant may ask, for the whole install: set up once here,
// and every page that uses the assistant reads the active one
// (s.assistantSettings). They are sealed whole with the install's master key,
// and no answer here ever carries a key.
//
// /databases/assistant is where the one provider lived before there were several
// and still answers: it reads and saves the active one the same way.

// assistantRingKey is where the keyring is stored; assistantSettingKey holds the
// one provider an install saved before there were several, until it next saves.
const assistantRingKey = "assistant.keyring"

// mountAssistantRoutes is called in the feature group.
func (s *Server) mountAssistantRoutes(r chi.Router) {
	r.Route("/assistant", func(r chi.Router) {
		// The assistant about the whole server (handlers_assistant_turn.go). It acts
		// as the person who asks, so it is a session's, as a deployment's is, and
		// it asks for what a deployment's does.
		r.With(httpx.RequireCapability(auth.CapServiceControl), httpx.RequireSession).
			Method(http.MethodPost, "/chat", s.handle(s.handleServerAssistant))
		// The person's own saved chats, whichever they are about
		// (handlers_assistant_chats.go): theirs alone, by session or their key.
		r.Route("/chats", func(r chi.Router) {
			r.Method(http.MethodGet, "/", s.handle(s.handleAssistantChats))
			r.Method(http.MethodGet, "/{chat}", s.handle(s.handleAssistantChat))
			r.Method(http.MethodGet, "/{chat}/images/{image}", s.handle(s.handleAssistantChatImage))
			r.Method(http.MethodPatch, "/{chat}", s.handle(s.handleAssistantChatRename))
			r.Method(http.MethodDelete, "/{chat}", s.handle(s.handleAssistantChatDelete))
			s.mountAssistantTerminalRoutes(r)
		})
		// Who answers and with what, which every page that offers the assistant
		// reads. The key is never in the answer.
		r.Method(http.MethodGet, "/", s.handle(s.handleAssistantSettings))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			// Who answers and what it may do alone are a person's to choose: a key
			// pointing it at a server of its own would be handed what everybody asks,
			// and could answer with what to do.
			r.Use(httpx.RequireSession)
			r.Method(http.MethodPost, "/models", s.handle(s.handleAssistantModels))
			r.Method(http.MethodPut, "/preferences", s.handle(s.handleAssistantPreferences))
			r.Method(http.MethodPut, "/providers/{preset}", s.handle(s.handleAssistantProviderSave))
			r.Method(http.MethodDelete, "/providers/{preset}", s.handle(s.handleAssistantProviderRemove))
			r.Method(http.MethodPost, "/providers/{preset}/activate", s.handle(s.handleAssistantProviderActivate))
		})
	})
}

// assistantRing is what is saved. An install that saved a provider before there
// were several reads as a keyring of that one.
func (s *Server) assistantRing(ctx context.Context) (*assistant.Keyring, error) {
	sealed, ok, err := s.Store.Setting(ctx, assistantRingKey)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if ok && sealed != "" {
		plain, err := s.Sealer.Open(sealed)
		if err != nil {
			return nil, errAssistantSealed
		}
		var ring assistant.Keyring
		if err := json.Unmarshal([]byte(plain), &ring); err != nil {
			return nil, errAssistantSealed
		}
		return &ring, nil
	}
	sealed, ok, err = s.Store.Setting(ctx, assistantSettingKey)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if !ok || sealed == "" {
		return assistant.RingOf(nil), nil
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return nil, errAssistantSealed
	}
	var settings assistant.Settings
	if err := json.Unmarshal([]byte(plain), &settings); err != nil {
		return nil, errAssistantSealed
	}
	return assistant.RingOf(&settings), nil
}

// saveAssistantRing seals the keyring and retires the setting it took over from.
func (s *Server) saveAssistantRing(ctx context.Context, ring *assistant.Keyring) error {
	plain, err := json.Marshal(ring)
	if err != nil {
		return httpx.Internal(err)
	}
	sealed, err := s.Sealer.Seal(string(plain))
	if err != nil {
		return httpx.Internal(err)
	}
	if err := s.Store.SetSetting(ctx, assistantRingKey, sealed); err != nil {
		return httpx.Internal(err)
	}
	if err := s.Store.SetSetting(ctx, assistantSettingKey, ""); err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// ringForChange is the keyring to change. One that cannot be opened with this
// install's master key is started over, which is how an administrator gets out
// of that: it is what the page's notice tells them to do.
func (s *Server) ringForChange(ctx context.Context) (*assistant.Keyring, error) {
	ring, err := s.assistantRing(ctx)
	if errors.Is(err, errAssistantSealed) {
		return assistant.RingOf(nil), nil
	}
	return ring, err
}

// saveAssistantProfile checks a setup with the provider — without asking the
// model anything — and saves it, so a mistyped key is said under the field it was
// typed in rather than under the first question somebody asks.
func (s *Server) saveAssistantProfile(r *http.Request, preset string, in assistant.Setup, action string, activate bool) (*assistant.Keyring, error) {
	httpx.SetAudit(r, action, preset, nil)
	s.assistant.ringMu.Lock()
	defer s.assistant.ringMu.Unlock()
	ring, err := s.ringForChange(r.Context())
	if err != nil {
		return nil, err
	}
	var before assistant.Settings
	if existing := ring.Find(preset); existing != nil {
		before = existing.Settings
	}
	profile, err := assistant.PrepareProfile(preset, in, ring)
	var field *assistant.FieldError
	if errors.As(err, &field) {
		return nil, &httpx.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: field.Message, Field: field.Field}
	}
	if err != nil {
		return nil, err
	}
	client, err := assistant.New(profile.Settings, s.assistant.options)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	if err := client.Probe(ctx); err != nil {
		return nil, assistantCheckFailed(err)
	}
	profile.UpdatedAt = time.Now().UTC()
	profile.UpdatedBy = httpx.MustPrincipal(r).Username()
	ring.Put(profile)
	if activate || ring.Active == "" {
		ring.Active = preset
	}
	if err := s.saveAssistantRing(r.Context(), ring); err != nil {
		return nil, err
	}
	detail := map[string]any{
		"provider": profile.Provider, "preset": preset, "model": profile.Model,
		"keyChanged": before.Key != profile.Key, "active": ring.Active == preset,
	}
	if u, err := url.Parse(profile.BaseURL); err == nil && u.Host != "" {
		detail["host"] = u.Host
	}
	httpx.SetAudit(r, action, preset, detail)
	return ring, nil
}

// handleAssistantProviderSave saves one provider. It becomes the active one when
// the form says so or when nothing else is.
func (s *Server) handleAssistantProviderSave(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		assistant.Setup
		Activate bool `json:"activate"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	ring, err := s.saveAssistantProfile(r, chi.URLParam(r, "preset"), in.Setup, "assistant.provider.save", in.Activate)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.RingView(ring)})
	return nil
}

// handleAssistantProviderRemove forgets one provider and its key.
func (s *Server) handleAssistantProviderRemove(w http.ResponseWriter, r *http.Request) error {
	preset := chi.URLParam(r, "preset")
	httpx.SetAudit(r, "assistant.provider.remove", preset, nil)
	s.assistant.ringMu.Lock()
	defer s.assistant.ringMu.Unlock()
	ring, err := s.ringForChange(r.Context())
	if err != nil {
		return err
	}
	if ring.Find(preset) == nil {
		return httpx.Err(http.StatusNotFound, "not_found", "that provider is not saved")
	}
	ring.Remove(preset)
	if err := s.saveAssistantRing(r.Context(), ring); err != nil {
		return err
	}
	httpx.SetAudit(r, "assistant.provider.remove", preset, map[string]any{"active": ring.Active})
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.RingView(ring)})
	return nil
}

// handleAssistantProviderActivate is the press that switches every page to a
// saved provider, and to another of its models when one is named. Conversations
// in progress end: what they hold was written for the model that was there.
func (s *Server) handleAssistantProviderActivate(w http.ResponseWriter, r *http.Request) error {
	preset := chi.URLParam(r, "preset")
	var in struct {
		Model string `json:"model"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	httpx.SetAudit(r, "assistant.provider.activate", preset, map[string]any{"model": in.Model})
	s.assistant.ringMu.Lock()
	defer s.assistant.ringMu.Unlock()
	ring, err := s.ringForChange(r.Context())
	if err != nil {
		return err
	}
	existing := ring.Find(preset)
	if existing == nil {
		return httpx.Err(http.StatusNotFound, "not_found", "that provider is not saved")
	}
	model := in.Model
	if model == "" {
		model = existing.Model
	}
	toolCalls := existing.ToolCalls
	profile, err := assistant.PrepareProfile(preset, assistant.Setup{
		Provider: existing.Provider, Model: model, BaseURL: existing.BaseURL, ToolCalls: &toolCalls,
	}, ring)
	var field *assistant.FieldError
	if errors.As(err, &field) {
		return &httpx.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: field.Message, Field: field.Field}
	}
	if err != nil {
		return err
	}
	client, err := assistant.New(profile.Settings, s.assistant.options)
	if err != nil {
		return httpx.Internal(err)
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	if err := client.Probe(ctx); err != nil {
		return assistantCheckFailed(err)
	}
	profile.UpdatedAt = time.Now().UTC()
	profile.UpdatedBy = httpx.MustPrincipal(r).Username()
	ring.Put(profile)
	ring.Active = preset
	if err := s.saveAssistantRing(r.Context(), ring); err != nil {
		return err
	}
	httpx.SetAudit(r, "assistant.provider.activate", preset, map[string]any{"provider": profile.Provider, "model": profile.Model})
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.RingView(ring)})
	return nil
}

// handleAssistantModels lists what a provider can answer with: the one saved, or
// the one being typed into the form, whose key is used for this request and kept
// nowhere. It costs nothing and asks no model anything.
func (s *Server) handleAssistantModels(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Preset  string `json:"preset"`
		BaseURL string `json:"baseUrl"`
		Key     string `json:"key"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	ring, err := s.ringForChange(r.Context())
	if err != nil {
		return err
	}
	// The form may not have a model yet, and listing is how it gets one.
	profile, err := assistant.PrepareProfile(in.Preset, assistant.Setup{Model: assistant.ModelToList(in.Preset), BaseURL: in.BaseURL, Key: in.Key}, ring)
	var field *assistant.FieldError
	if errors.As(err, &field) {
		return &httpx.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: field.Message, Field: field.Field}
	}
	if err != nil {
		return err
	}
	client, err := assistant.New(profile.Settings, s.assistant.options)
	if err != nil {
		return httpx.Internal(err)
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	models, err := client.Models(ctx)
	if err != nil {
		return assistantCheckFailed(err)
	}
	if models == nil {
		models = []assistant.Model{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"models": models})
	return nil
}

// handleAssistantPreferences sets the mode a conversation starts in.
func (s *Server) handleAssistantPreferences(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		DefaultMode assistant.Mode `json:"defaultMode"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if !in.DefaultMode.Valid() {
		return httpx.BadRequest("defaultMode is ask, changes or full")
	}
	httpx.SetAudit(r, "assistant.preferences", string(in.DefaultMode), nil)
	s.assistant.ringMu.Lock()
	defer s.assistant.ringMu.Unlock()
	ring, err := s.ringForChange(r.Context())
	if err != nil {
		return err
	}
	ring.DefaultMode = in.DefaultMode
	if err := s.saveAssistantRing(r.Context(), ring); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.RingView(ring)})
	return nil
}
