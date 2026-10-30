package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/secretstore"
)

// Secret stores: the connections to Vault, OpenBao, Infisical, Doppler, AWS
// Secrets Manager and Azure Key Vault that deployment variables reference as
// ${{store.NAME/path#KEY}} (a whole Secrets Manager or Key Vault secret as
// ${{store.NAME/secret-name}}). The list
// holds no credential and no value, so anybody who may read a project's
// variables may read it; adding, changing and testing one handle a
// credential, so they take an administrator in a session, as a notification
// channel's do; removing one is destructive.

func (s *Server) mountSecretStoreReads(r chi.Router) {
	r.Method(http.MethodGet, "/secret-stores", s.handle(s.handleSecretStores))
}

func (s *Server) mountSecretStoreWrites(r chi.Router) {
	r.Method(http.MethodPost, "/secret-stores", s.handle(s.handleSecretStoreCreate))
	r.Method(http.MethodPut, "/secret-stores/{store}", s.handle(s.handleSecretStoreUpdate))
	r.Method(http.MethodPost, "/secret-stores/{store}/test", s.handle(s.handleSecretStoreTest))
}

func (s *Server) mountSecretStoreRemoval(r chi.Router) {
	r.With(httpx.RequireSession).Method(http.MethodDelete, "/secret-stores/{store}", s.handle(s.handleSecretStoreDelete))
}

// secretStoreView is a connection as the page reads it, with the variables
// that reference it.
type secretStoreView struct {
	secretstore.Store
	UsedBy []deploy.SecretStoreUse `json:"usedBy"`
}

func mapSecretStoreError(err error) error {
	switch {
	case errors.Is(err, secretstore.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, secretstore.ErrNameTaken):
		return httpx.Err(http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, secretstore.ErrInvalid):
		return httpx.BadRequest("%v", err)
	case errors.Is(err, deploy.ErrSecretStoreInUse):
		return httpx.Err(http.StatusConflict, "secret_store_in_use", err.Error())
	default:
		return httpx.Internal(err)
	}
}

func secretStoreParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "store"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("invalid secret store id")
	}
	return id, nil
}

func (s *Server) secretStoreViews(r *http.Request, stores ...secretstore.Store) ([]secretStoreView, error) {
	uses, err := s.modules.deployPlanning.SecretStoreUses(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]secretStoreView, 0, len(stores))
	for _, item := range stores {
		used := uses[item.Name]
		if used == nil {
			used = []deploy.SecretStoreUse{}
		}
		out = append(out, secretStoreView{Store: item, UsedBy: used})
	}
	return out, nil
}

func (s *Server) handleSecretStores(w http.ResponseWriter, r *http.Request) error {
	stores, err := s.modules.secretStores.List(r.Context())
	if err != nil {
		return mapSecretStoreError(err)
	}
	views, err := s.secretStoreViews(r, stores...)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, views)
	return nil
}

// secretStoreAudit is what an entry says of a connection: where it points
// and how it signs in, never a credential. The keys avoid the words the
// audit log redacts on sight ("credential", "token"), since what they hold
// here is the names of what is set, which is the useful half of the record.
func secretStoreAudit(item *secretstore.Store, extra map[string]any) map[string]any {
	detail := map[string]any{
		"kind": item.Kind, "address": item.Endpoint, "allowPreviews": item.AllowPreviews,
		"signInFields": item.CredentialNames,
	}
	if item.Settings.Auth != "" {
		detail["auth"] = item.Settings.Auth
	}
	for key, value := range extra {
		detail[key] = value
	}
	return detail
}

func (s *Server) handleSecretStoreCreate(w http.ResponseWriter, r *http.Request) error {
	var req secretstore.Write
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	item, err := s.modules.secretStores.Create(r.Context(), req, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapSecretStoreError(err)
	}
	httpx.SetAudit(r, "deploy.secret_store.create", item.Name, secretStoreAudit(item, nil))
	views, err := s.secretStoreViews(r, *item)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusCreated, views[0])
	return nil
}

func (s *Server) handleSecretStoreUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := secretStoreParam(r)
	if err != nil {
		return err
	}
	var req secretstore.Write
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	item, replaced, err := s.modules.secretStores.Update(r.Context(), id, req, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapSecretStoreError(err)
	}
	httpx.SetAudit(r, "deploy.secret_store.update", item.Name, secretStoreAudit(item, map[string]any{"signInReplaced": replaced}))
	views, err := s.secretStoreViews(r, *item)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, views[0])
	return nil
}

// handleSecretStoreTest signs in and, given a path and key, reads it. A
// store that refuses is a result with its own words, answered 200 so the
// page can show it beside the connection; the audit entry says whether it
// passed and what the store answered, which never holds a value.
func (s *Server) handleSecretStoreTest(w http.ResponseWriter, r *http.Request) error {
	id, err := secretStoreParam(r)
	if err != nil {
		return err
	}
	var probe secretstore.Probe
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &probe); err != nil {
			return err
		}
	}
	item, err := s.modules.secretStores.Get(r.Context(), id)
	if err != nil {
		return mapSecretStoreError(err)
	}
	ctx, cancel := timeoutCtx(r, 40*time.Second)
	defer cancel()
	result, err := s.modules.secretStores.Test(ctx, id, &probe)
	if err != nil {
		return mapSecretStoreError(err)
	}
	detail := map[string]any{"ok": result.OK, "message": result.Message}
	switch {
	case probe.Key != "":
		detail["probe"] = probe.Path + "#" + probe.Key
	case probe.Path != "":
		// A whole Secrets Manager or Key Vault secret has no key.
		detail["probe"] = probe.Path
	}
	httpx.SetAudit(r, "deploy.secret_store.test", item.Name, detail)
	if !result.OK {
		httpx.AuditFailed(r)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleSecretStoreDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := secretStoreParam(r)
	if err != nil {
		return err
	}
	item, err := s.modules.secretStores.Get(r.Context(), id)
	if err != nil {
		return mapSecretStoreError(err)
	}
	httpx.SetAudit(r, "deploy.secret_store.delete", item.Name, map[string]any{"kind": item.Kind, "address": item.Endpoint})
	if err := s.modules.deployPlanning.SecretStoreInUse(r.Context(), item.Name); err != nil {
		return mapSecretStoreError(err)
	}
	if err := s.modules.secretStores.Delete(r.Context(), id); err != nil {
		return mapSecretStoreError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
