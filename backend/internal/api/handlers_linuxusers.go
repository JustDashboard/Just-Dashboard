package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountLinuxUserRoutes(r chi.Router) {
	r.Route("/system-users", func(r chi.Router) {
		// Creating a host account hands somebody a way onto the machine, so
		// the whole section requires system-admin, reads included: the list
		// exposes shells, groups and key counts.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleSysUserList))
		r.With(httpx.RequireSession).Method(http.MethodPost, "/", s.handle(s.handleSysUserCreate))

		r.Route("/{name}", func(r chi.Router) {
			r.Use(s.guardSystemAccount)
			r.Method(http.MethodGet, "/", s.handle(s.handleSysUserGet))
			r.Method(http.MethodPatch, "/", s.handle(s.handleSysUserUpdate))

			r.Method(http.MethodGet, "/activity", s.handle(s.handleSysUserActivity))
			r.Method(http.MethodGet, "/disk", s.handle(s.handleSysUserDisk))
			r.Method(http.MethodGet, "/history", s.handle(s.handleSysUserHistory))
			r.Method(http.MethodGet, "/removal", s.handle(s.handleSysUserRemoval))
			r.Method(http.MethodGet, "/limits", s.handle(s.handleSysUserLimits))
			r.Method(http.MethodPut, "/limits", s.handle(s.handleSysUserLimitsSet))
			r.Method(http.MethodGet, "/sudo", s.handle(s.handleSysUserSudo))
			r.Method(http.MethodPut, "/sudo", s.handle(s.handleSysUserSudoSet))
			r.Method(http.MethodPost, "/failed-attempts/reset", s.handle(s.handleSysUserFailedReset))

			// A password or a private key that leaves the dashboard is a way
			// onto the machine, so a long-lived API key may not mint one.
			r.With(httpx.RequireSession).Method(http.MethodPost, "/password", s.handle(s.handleSysUserPassword))
			r.With(httpx.RequireSession).Method(http.MethodPost, "/keys/generate", s.handle(s.handleSSHKeyGenerate))

			r.Method(http.MethodGet, "/keys", s.handle(s.handleSSHKeyList))
			r.Method(http.MethodPost, "/keys", s.handle(s.handleSSHKeyAdd))
			r.Method(http.MethodPatch, "/keys", s.handle(s.handleSSHKeyUpdate))
			r.Method(http.MethodPost, "/keys/import", s.handle(s.handleSSHKeyImport))
			r.Method(http.MethodPost, "/keys/copy", s.handle(s.handleSSHKeyCopy))

			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/", s.handle(s.handleSysUserDelete))
				r.Method(http.MethodDelete, "/keys", s.handle(s.handleSSHKeyRemove))
				r.Method(http.MethodDelete, "/password", s.handle(s.handleSysUserPasswordRemove))
				r.Method(http.MethodDelete, "/sessions", s.handle(s.handleSysUserSessionsEnd))
				r.Method(http.MethodDelete, "/sudo/rules", s.handle(s.handleSysUserSudoRuleRemove))
			})
		})
	})

	// Everything about accounts that is not one account. It is a tree of its own
	// because anything under /system-users would shadow an account of that name.
	r.Route("/system-accounts", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/groups", s.handle(s.handleSysGroupList))
		r.Method(http.MethodPost, "/groups", s.handle(s.handleSysGroupCreate))
		r.Method(http.MethodPatch, "/groups/{group}", s.handle(s.handleSysGroupUpdate))
		r.Method(http.MethodGet, "/defaults", s.handle(s.handleSysDefaults))
		r.Method(http.MethodPut, "/defaults", s.handle(s.handleSysDefaultsSet))
		r.Method(http.MethodGet, "/templates", s.handle(s.handleSysTemplates))
		r.Method(http.MethodPut, "/templates", s.handle(s.handleSysTemplatesSet))
		r.Method(http.MethodPost, "/bulk", s.handle(s.handleSysBulk))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/groups/{group}", s.handle(s.handleSysGroupDelete))
			r.Method(http.MethodPost, "/bulk/delete", s.handle(s.handleSysBulkDelete))
		})
	})
}

// spendDestructive is s.destructive for a route that is only destructive for some
// of what its body can say: ending sessions is the same act behind PATCH as behind
// the DELETE that is wrapped, and has to cost the same.
func (s *Server) spendDestructive(r *http.Request) error {
	p := httpx.MustPrincipal(r)
	if !p.Can(auth.CapDestructive) {
		return httpx.Err(http.StatusForbidden, "forbidden", "your role does not permit this")
	}
	if !s.destrLim.Allow(p.Username() + "|systemuser") {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many destructive actions; try again shortly")
	}
	return nil
}

func mapLinuxUserError(err error) error {
	switch {
	case errors.Is(err, linuxusers.ErrNotFound):
		return httpx.ErrNotFound
	case errors.Is(err, linuxusers.ErrProtected):
		return httpx.Err(http.StatusForbidden, "protected_account", err.Error())
	case errors.Is(err, linuxusers.ErrInvalidUser):
		return httpx.Err(http.StatusBadRequest, "invalid_username", err.Error())
	case errors.Is(err, linuxusers.ErrLastWayIn):
		return httpx.Err(http.StatusConflict, "last_way_in", err.Error())
	case errors.Is(err, linuxusers.ErrNothingToUnlock):
		return httpx.Err(http.StatusConflict, "nothing_to_unlock", err.Error())
	case errors.Is(err, linuxusers.ErrAccountBusy):
		return httpx.Err(http.StatusConflict, "account_busy", err.Error())
	case errors.Is(err, linuxusers.ErrSharedNumber):
		return httpx.Err(http.StatusConflict, "shares_number", err.Error())
	default:
		return httpx.BadRequest("%v", err)
	}
}

// guardSystemAccount holds a daemon's account read-only unless the request says
// it means it. A package made that account and expects it as it left it, and a
// page that offers the same verbs on `_apt` as on a person is how one gets
// deleted by accident. Root is not in this class: its password and keys are the
// operator's, and what must not happen to it is refused in linuxusers itself.
func (s *Server) guardSystemAccount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.URL.Query().Get("allowSystem") == "true" {
			next.ServeHTTP(w, r)
			return
		}
		user, err := s.modules.linuxUsers.Lookup(chi.URLParam(r, "name"))
		// By name: a second uid 0 is not root, it is an account pretending to be, and
		// it gets the same care as anything else below uid 1000.
		if err == nil && user.System && user.Username != "root" {
			httpx.WriteError(w, r, httpx.Err(http.StatusForbidden, "system_account",
				fmt.Sprintf("%s belongs to the system: turn on editing for it first", user.Username)))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSysUserList(w http.ResponseWriter, r *http.Request) error {
	users, err := s.modules.linuxUsers.List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	// Root stays in the list however the filter is set. It is a system account
	// by its number and the most important account on the machine by every other
	// measure: leaving it out is how the page could read "0 have sign-in routes"
	// on a host where root signs in with a password.
	if r.URL.Query().Get("system") != "true" {
		filtered := users[:0]
		for _, u := range users {
			if !u.System || u.UID == 0 {
				filtered = append(filtered, u)
			}
		}
		users = filtered
	}
	httpx.JSON(w, http.StatusOK, users)
	return nil
}

// accountDetail is one account with what only a single account's read can
// afford: pam_faillock's count, and the shells its picker offers.
type accountDetail struct {
	linuxusers.User
	FailedAttempts linuxusers.FailedAttempts `json:"failedAttempts"`
	Shells         []string                  `json:"shells"`
}

func (s *Server) handleSysUserGet(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	u, err := s.modules.linuxUsers.Get(r.Context(), name)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, accountDetail{
		User:           *u,
		FailedAttempts: s.modules.linuxUsers.FailedAttempts(r.Context(), name),
		Shells:         s.modules.linuxUsers.Shells(),
	})
	return nil
}

type createSysUserRequest struct {
	Username string   `json:"username"`
	Comment  string   `json:"comment"`
	Shell    string   `json:"shell"`
	Groups   []string `json:"groups"`
	System   bool     `json:"system"`
	NoHome   bool     `json:"noHome"`
	SSHKey   string   `json:"sshKey"`
	// GitHubUser imports the keys GitHub publishes for that login.
	GitHubUser string `json:"githubUser"`
	// Password sets one; GeneratePassword makes one and returns it once.
	Password         string                `json:"password"`
	GeneratePassword bool                  `json:"generatePassword"`
	MustChange       bool                  `json:"mustChangePassword"`
	Sudo             *linuxusers.SudoGrant `json:"sudo"`
	// NoSignIn is the operator saying a person-shaped account nobody can sign in
	// to is what they want. Without it such an account is refused.
	NoSignIn bool `json:"noSignIn"`
}

// handleSysUserCreate validates everything it can before the account exists, so
// a typo leaves nothing behind, and takes the account back out if anything after
// it fails: a half-made account with no way in is what this page used to leave.
func (s *Server) handleSysUserCreate(w http.ResponseWriter, r *http.Request) error {
	var req createSysUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.SSHKey != "" {
		if _, err := linuxusers.ValidatePublicKey(req.SSHKey); err != nil {
			return httpx.BadRequest("ssh key rejected: %v", err)
		}
	}
	github := []string{}
	if req.GitHubUser != "" {
		keys, err := linuxusers.FetchGitHubKeys(ctx, req.GitHubUser)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		if len(keys) == 0 {
			return httpx.BadRequest("%s has no public keys on GitHub", req.GitHubUser)
		}
		github = keys
	}
	password := req.Password
	generated := ""
	if req.GeneratePassword {
		var err error
		if generated, err = linuxusers.GeneratePassword(); err != nil {
			return httpx.Internal(err)
		}
		password = generated
	}
	if err := linuxusers.CheckNewPassword(password); err != nil {
		return httpx.BadRequest("%v", err)
	}
	if req.Sudo != nil {
		if err := linuxusers.CheckGrant(req.Username, *req.Sudo); err != nil {
			return httpx.BadRequest("%v", err)
		}
	}
	login := !req.System && (req.Shell == "" || linuxusers.IsLoginShell(req.Shell))
	if login && !req.NoSignIn && req.SSHKey == "" && len(github) == 0 && password == "" {
		return httpx.Err(http.StatusBadRequest, "no_way_in",
			"give this account a key or a password, or say that nobody signs in to it directly")
	}
	if req.MustChange && password == "" {
		return httpx.BadRequest("a password has to be set before it can be required to change")
	}
	// A home kept from an earlier delete comes with this name: its files, and the
	// keys in it, which sign in as the new account from its first second. Undoing a
	// failed create must not take that home with it, and the operator has to be told.
	inherited := ""
	if !req.NoHome {
		inherited = s.modules.linuxUsers.ExistingHome(req.Username)
	}
	if err := s.modules.linuxUsers.Create(ctx, linuxusers.CreateOptions{
		Username: req.Username, Comment: req.Comment, Shell: req.Shell,
		Groups: req.Groups, System: req.System, NoHome: req.NoHome,
	}); err != nil {
		return mapLinuxUserError(err)
	}
	finish := func() error {
		if req.SSHKey != "" {
			if _, err := s.modules.linuxUsers.AddKey(req.Username, req.SSHKey); err != nil {
				return fmt.Errorf("the ssh key could not be installed: %w", err)
			}
		}
		label := "github:" + req.GitHubUser
		for _, line := range github {
			if _, err := s.modules.linuxUsers.AddKeyWith(req.Username, line, linuxusers.KeyEdit{Comment: &label}); err != nil &&
				!linuxusers.IsAlreadyAuthorised(err) {
				return fmt.Errorf("a key from GitHub could not be installed: %w", err)
			}
		}
		if password != "" {
			if err := s.modules.linuxUsers.SetPassword(ctx, req.Username, password); err != nil {
				return err
			}
		}
		if req.MustChange {
			if err := s.modules.linuxUsers.RequirePasswordChange(ctx, req.Username); err != nil {
				return err
			}
		}
		if req.Sudo != nil && req.Sudo.Level != linuxusers.SudoNone {
			return s.modules.linuxUsers.SetSudoGrant(ctx, req.Username, *req.Sudo)
		}
		return nil
	}
	if err := finish(); err != nil {
		home := linuxusers.HomeDelete
		if inherited != "" {
			home = linuxusers.HomeKeep
		}
		_, undo := s.modules.linuxUsers.Delete(context.WithoutCancel(ctx), req.Username,
			linuxusers.RemovalOptions{Home: home, EndSessions: true})
		if undo != nil {
			return httpx.BadRequest("%v, and the account could not be taken back out: %v", err, undo)
		}
		return httpx.BadRequest("%v, so the account was not created", err)
	}
	u, err := s.modules.linuxUsers.Get(ctx, req.Username)
	if err != nil {
		return mapLinuxUserError(err)
	}
	detail := map[string]any{
		"groups": req.Groups, "shell": req.Shell, "withKey": req.SSHKey != "" || len(github) > 0,
		"withPassword": password != "",
	}
	if req.GitHubUser != "" {
		detail["github"] = req.GitHubUser
	}
	if req.Sudo != nil {
		detail["sudo"] = req.Sudo.Level
	}
	httpx.SetAudit(r, "system.user.create", req.Username, detail)
	httpx.JSON(w, http.StatusCreated, struct {
		*linuxusers.User
		GeneratedPassword string `json:"generatedPassword,omitempty"`
		InheritedHome     string `json:"inheritedHome,omitempty"`
	}{u, generated, inherited})
	return nil
}

type updateSysUserRequest struct {
	Locked *bool     `json:"locked,omitempty"`
	Shell  *string   `json:"shell,omitempty"`
	Groups *[]string `json:"groups,omitempty"`

	Disabled *bool `json:"disabled,omitempty"`
	// EndSessions goes with Disabled: an expired account's running shell is not
	// stopped by the date passing.
	EndSessions        bool                    `json:"endSessions,omitempty"`
	ExpiresOn          *string                 `json:"expiresOn,omitempty"`
	MustChangePassword bool                    `json:"mustChangePassword,omitempty"`
	Aging              *linuxusers.AgingChange `json:"aging,omitempty"`

	linuxusers.ProfileChange
}

func (s *Server) handleSysUserUpdate(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	ctx := r.Context()
	var req updateSysUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	users := s.modules.linuxUsers
	if req.Disabled != nil && *req.Disabled && req.EndSessions {
		if err := s.spendDestructive(r); err != nil {
			return err
		}
	}
	// Set before the first step: this applies several things in turn, and a failure
	// part-way must still leave a record of what was being attempted.
	httpx.SetAudit(r, "system.user.update", name, req)
	if req.Locked != nil {
		if err := users.SetLocked(ctx, name, *req.Locked); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.Shell != nil {
		if err := users.SetShell(ctx, name, *req.Shell); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.Groups != nil {
		if err := users.SetGroups(ctx, name, *req.Groups); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.Disabled != nil {
		if err := users.SetDisabled(ctx, name, *req.Disabled); err != nil {
			return mapLinuxUserError(err)
		}
		if *req.Disabled && req.EndSessions {
			if err := users.EndSessions(ctx, name); err != nil {
				return mapLinuxUserError(err)
			}
		}
	}
	if req.ExpiresOn != nil {
		if err := users.SetExpiry(ctx, name, *req.ExpiresOn); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.Aging != nil {
		if err := users.SetAging(ctx, name, *req.Aging); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.MustChangePassword {
		if err := users.RequirePasswordChange(ctx, name); err != nil {
			return mapLinuxUserError(err)
		}
	}
	final, err := users.UpdateProfile(ctx, name, req.ProfileChange)
	if err != nil {
		return mapLinuxUserError(err)
	}
	u, err := users.Get(ctx, final)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.update", name, req)
	httpx.JSON(w, http.StatusOK, u)
	return nil
}

type passwordRequest struct {
	Password   string `json:"password"`
	Generate   bool   `json:"generate"`
	MustChange bool   `json:"mustChange"`
}

// handleSysUserPassword is what Unlock could never be. The password appears in
// no audit entry and no log; a generated one is in this one response and then
// exists nowhere but the shadow file, as a hash.
func (s *Server) handleSysUserPassword(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req passwordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	password := req.Password
	generated := ""
	if req.Generate {
		var err error
		if generated, err = linuxusers.GeneratePassword(); err != nil {
			return httpx.Internal(err)
		}
		password = generated
	}
	ctx := r.Context()
	httpx.SetAudit(r, "system.user.password.set", name,
		map[string]any{"generated": req.Generate, "mustChange": req.MustChange})
	if err := s.modules.linuxUsers.SetPassword(ctx, name, password); err != nil {
		return mapLinuxUserError(err)
	}
	if req.MustChange {
		if err := s.modules.linuxUsers.RequirePasswordChange(ctx, name); err != nil {
			return mapLinuxUserError(err)
		}
	}
	httpx.SetAudit(r, "system.user.password.set", name,
		map[string]any{"generated": req.Generate, "mustChange": req.MustChange})
	u, err := s.modules.linuxUsers.Get(ctx, name)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, struct {
		*linuxusers.User
		GeneratedPassword string `json:"generatedPassword,omitempty"`
	}{u, generated})
	return nil
}

func (s *Server) handleSysUserPasswordRemove(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if err := s.modules.linuxUsers.RemovePassword(r.Context(), name); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.password.remove", name, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleSysUserFailedReset(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if err := s.modules.linuxUsers.ResetFailedAttempts(r.Context(), name); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.failed-attempts.reset", name, nil)
	httpx.NoContent(w)
	return nil
}

// handleSysUserDelete takes the operator's answer to the preview. Removing a home
// with no archive is the one choice nothing brings back, so it alone takes the
// typed name, enforced here (invariant 3).
func (s *Server) handleSysUserDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	query := r.URL.Query()
	opts := linuxusers.RemovalOptions{
		Home:        query.Get("home"),
		EndSessions: query.Get("endSessions") == "true",
		TransferTo:  query.Get("transferTo"),
	}
	// The route's older spelling, still accepted, means the same destruction and
	// so takes the same guard.
	if query.Get("removeHome") == "true" && opts.Home == "" {
		opts.Home = linuxusers.HomeDelete
	}
	if opts.Home == linuxusers.HomeDelete {
		if err := httpx.RequireTypedConfirmation(w, r, name); err != nil {
			return err
		}
	}
	// A daemon's account is held read-only everywhere else, and being handed
	// another account's files is a change to it.
	if opts.TransferTo != "" && query.Get("allowSystem") != "true" {
		if target, err := s.modules.linuxUsers.Lookup(opts.TransferTo); err == nil && target.System {
			return httpx.Err(http.StatusForbidden, "system_account",
				fmt.Sprintf("%s belongs to the system: turn on editing for it first", target.Username))
		}
	}
	httpx.SetAudit(r, "system.user.delete", name, map[string]any{
		"home": opts.Home, "endSessions": opts.EndSessions, "transferTo": opts.TransferTo,
	})
	result, err := s.modules.linuxUsers.Delete(r.Context(), name, opts)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.delete", name, map[string]any{
		"home": opts.Home, "endSessions": opts.EndSessions, "transferTo": opts.TransferTo,
		"archive": result.Archive,
	})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleSysUserRemoval(w http.ResponseWriter, r *http.Request) error {
	removal, err := s.modules.linuxUsers.PreviewRemoval(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, removal)
	return nil
}

// --- what the account is doing and has done ---------------------------------

type accountActivity struct {
	Sessions  []netsec.LoginSession   `json:"sessions"`
	Processes int                     `json:"processes"`
	Busiest   []linuxusers.ProcessRow `json:"busiest"`
	CronJobs  int                     `json:"cronJobs"`
}

func (s *Server) handleSysUserActivity(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	ctx := r.Context()
	if _, err := s.modules.linuxUsers.Lookup(name); err != nil {
		return mapLinuxUserError(err)
	}
	out := accountActivity{Sessions: []netsec.LoginSession{}, Busiest: []linuxusers.ProcessRow{}}
	if sessions, err := s.modules.netsec.Sessions(ctx); err == nil {
		for _, session := range sessions {
			if session.User == name {
				out.Sessions = append(out.Sessions, session)
			}
		}
	}
	if count, busiest, err := s.modules.linuxUsers.Processes(ctx, name, 5); err == nil {
		out.Processes, out.Busiest = count, busiest
	}
	if s.modules.cron != nil && s.modules.cron.Available() {
		if crontab, err := s.modules.cron.UserCrontab(ctx, name); err == nil && crontab != nil {
			out.CronJobs = len(crontab.Jobs)
		}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleSysUserDisk(w http.ResponseWriter, r *http.Request) error {
	bytes, err := s.modules.linuxUsers.DiskUsage(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]int64{"bytes": bytes})
	return nil
}

// accountHistorySample is how far back wtmp and btmp are read to find one
// account's rows. The whole host writes to them, and a public host's btmp is
// mostly strangers.
const accountHistorySample = 5000

func (s *Server) handleSysUserHistory(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	ctx := r.Context()
	if _, err := s.modules.linuxUsers.Lookup(name); err != nil {
		return mapLinuxUserError(err)
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	if limit < 1 || limit > 500 {
		limit = 50
	}
	out := map[string]any{"logins": []netsec.LoginRecord{}, "failed": []netsec.LoginRecord{}}
	if records, err := s.modules.netsec.LoginHistory(ctx, accountHistorySample); err == nil {
		out["logins"] = loginsOf(records, name, limit)
	}
	if records, err := s.modules.netsec.FailedLogins(ctx, accountHistorySample); err == nil {
		out["failed"] = loginsOf(records, name, limit)
	}
	out["sudo"] = s.modules.linuxUsers.SudoHistory(ctx, name, limit)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func loginsOf(records []netsec.LoginRecord, name string, limit int) []netsec.LoginRecord {
	out := []netsec.LoginRecord{}
	for _, record := range records {
		if record.User == name && record.Kind == "login" {
			out = append(out, record)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}

// handleSysUserSessionsEnd ends one sign-in by its process, or everything the
// account is running when none is named.
func (s *Server) handleSysUserSessionsEnd(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	ctx := r.Context()
	if raw := r.URL.Query().Get("pid"); raw != "" {
		pid, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return httpx.BadRequest("pid must be a number")
		}
		sessions, err := s.modules.netsec.Sessions(ctx)
		if err != nil {
			return httpx.Internal(err)
		}
		// The account is in the URL and the process in the query: ask that they
		// agree, so this route cannot be pointed at somebody else's login.
		owned := false
		for _, session := range sessions {
			if session.User == name && int64(session.PID) == pid {
				owned = true
			}
		}
		if !owned {
			return httpx.BadRequest("%s has no sign-in running as process %d", name, pid)
		}
		if _, err := s.modules.netsec.Disconnect(ctx, int32(pid)); err != nil {
			return httpx.BadRequest("%v", err)
		}
		httpx.SetAudit(r, "system.user.session.end", name, map[string]any{"pid": pid})
		httpx.NoContent(w)
		return nil
	}
	if err := s.modules.linuxUsers.EndSessions(ctx, name); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sessions.end", name, nil)
	httpx.NoContent(w)
	return nil
}

// --- sudo ---------------------------------------------------------------------

func (s *Server) handleSysUserSudo(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	rules, err := s.modules.linuxUsers.RulesFor(r.Context(), name)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"rules":   rules,
		"managed": s.modules.linuxUsers.ManagedGrant(name),
	})
	return nil
}

func (s *Server) handleSysUserSudoSet(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req linuxusers.SudoGrant
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Level == linuxusers.SudoNone {
		if err := s.spendDestructive(r); err != nil {
			return err
		}
	}
	if err := s.modules.linuxUsers.SetSudoGrant(r.Context(), name, req); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sudo.set", name,
		map[string]any{"level": req.Level, "commands": req.Commands})
	return s.handleSysUserSudo(w, r)
}

func (s *Server) handleSysUserSudoRuleRemove(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	id := r.URL.Query().Get("id")
	if id == "" {
		return httpx.BadRequest("id query parameter is required")
	}
	if err := s.modules.linuxUsers.RemoveSudoRule(r.Context(), name, id); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sudo.rule.remove", name, map[string]any{"rule": id})
	httpx.NoContent(w)
	return nil
}

// --- limits -------------------------------------------------------------------

func (s *Server) handleSysUserLimits(w http.ResponseWriter, r *http.Request) error {
	limits, err := s.modules.linuxUsers.Limits(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, limits)
	return nil
}

type limitsRequest struct {
	CPUPercent  int    `json:"cpuPercent"`
	MemoryBytes int64  `json:"memoryBytes"`
	Tasks       int    `json:"tasks"`
	SoftBytes   *int64 `json:"softBytes,omitempty"`
	HardBytes   *int64 `json:"hardBytes,omitempty"`
}

func (s *Server) handleSysUserLimitsSet(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	ctx := r.Context()
	var req limitsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "system.user.limits.set", name, req)
	if err := s.modules.linuxUsers.SetLimits(ctx, name, linuxusers.AccountLimits{
		CPUPercent: req.CPUPercent, MemoryBytes: req.MemoryBytes, Tasks: req.Tasks,
	}); err != nil {
		return mapLinuxUserError(err)
	}
	if req.HardBytes != nil || req.SoftBytes != nil {
		var soft, hard int64
		if req.SoftBytes != nil {
			soft = *req.SoftBytes
		}
		if req.HardBytes != nil {
			hard = *req.HardBytes
		}
		if err := s.modules.linuxUsers.SetQuota(ctx, name, soft, hard); err != nil {
			return mapLinuxUserError(err)
		}
	}
	httpx.SetAudit(r, "system.user.limits.set", name, req)
	return s.handleSysUserLimits(w, r)
}

// --- keys ---------------------------------------------------------------------

func (s *Server) handleSSHKeyList(w http.ResponseWriter, r *http.Request) error {
	keys, path, err := s.modules.linuxUsers.KeysWithUse(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": path, "keys": keys})
	return nil
}

type sshKeyRequest struct {
	Key string `json:"key"`
	linuxusers.KeyEdit
}

func (s *Server) handleSSHKeyAdd(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req sshKeyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	key, err := s.modules.linuxUsers.AddKeyWith(name, req.Key, req.KeyEdit)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sshkey.add", name,
		map[string]any{"fingerprint": key.Fingerprint, "type": key.Type, "comment": key.Comment, "options": key.Options})
	httpx.JSON(w, http.StatusCreated, key)
	return nil
}

type sshKeyUpdateRequest struct {
	Fingerprint string `json:"fingerprint"`
	linuxusers.KeyEdit
}

func (s *Server) handleSSHKeyUpdate(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req sshKeyUpdateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	key, err := s.modules.linuxUsers.UpdateKey(name, req.Fingerprint, req.KeyEdit)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sshkey.update", name,
		map[string]any{"fingerprint": key.Fingerprint, "comment": key.Comment, "options": key.Options})
	httpx.JSON(w, http.StatusOK, key)
	return nil
}

func (s *Server) handleSSHKeyImport(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req struct {
		GitHubUser string `json:"githubUser"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	result, err := s.modules.linuxUsers.ImportGitHubKeys(r.Context(), name, req.GitHubUser)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sshkey.import", name,
		map[string]any{"github": req.GitHubUser, "added": len(result.Added), "skipped": result.Skipped})
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleSSHKeyGenerate(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req struct {
		Comment    string `json:"comment"`
		Passphrase string `json:"passphrase"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	generated, err := s.modules.linuxUsers.GenerateKey(name, req.Comment, req.Passphrase)
	if err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sshkey.generate", name, map[string]any{
		"fingerprint": generated.Key.Fingerprint, "comment": generated.Key.Comment,
		"protected": req.Passphrase != "",
	})
	httpx.JSON(w, http.StatusCreated, generated)
	return nil
}

func (s *Server) handleSSHKeyCopy(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req struct {
		Fingerprint string   `json:"fingerprint"`
		To          []string `json:"to"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// The middleware read the account in the URL. Each target is a write to an
	// account too, and is held to the same rule.
	allowSystem := r.URL.Query().Get("allowSystem") == "true"
	targets := make([]string, 0, len(req.To))
	refused := map[string]string{}
	for _, target := range req.To {
		user, err := s.modules.linuxUsers.Lookup(target)
		switch {
		case err != nil:
			refused[target] = err.Error()
		case user.System && user.UID != 0 && !allowSystem:
			refused[target] = target + " belongs to the system"
		default:
			targets = append(targets, target)
		}
	}
	results, err := s.modules.linuxUsers.CopyKey(name, req.Fingerprint, targets)
	if err != nil {
		return mapLinuxUserError(err)
	}
	for target, why := range refused {
		results[target] = why
	}
	httpx.SetAudit(r, "system.user.sshkey.copy", name,
		map[string]any{"fingerprint": req.Fingerprint, "to": req.To})
	httpx.JSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

func (s *Server) handleSSHKeyRemove(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	fingerprint := r.URL.Query().Get("fingerprint")
	if fingerprint == "" {
		return httpx.BadRequest("fingerprint query parameter is required")
	}
	// No typed phrase: an authorised key is a public key, and putting one back
	// is a paste. Deleting the account it belongs to is the route above.
	if err := s.modules.linuxUsers.RevokeKey(r.Context(), name, fingerprint); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.user.sshkey.remove", name, map[string]any{"fingerprint": fingerprint})
	httpx.NoContent(w)
	return nil
}

// --- groups -------------------------------------------------------------------

func (s *Server) handleSysGroupList(w http.ResponseWriter, r *http.Request) error {
	groups, err := s.modules.linuxUsers.ListGroups(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, groups)
	return nil
}

func (s *Server) handleSysGroupCreate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name   string `json:"name"`
		GID    *int   `json:"gid"`
		System bool   `json:"system"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	gid := -1
	if req.GID != nil {
		gid = *req.GID
	}
	if err := s.modules.linuxUsers.CreateGroup(r.Context(), req.Name, gid, req.System); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.group.create", req.Name, map[string]any{"gid": gid, "system": req.System})
	httpx.NoContent(w)
	return nil
}

// guardSystemGroup is guardSystemAccount for a group: a package made `docker` and
// expects it to keep its name, number and existence. Who is in it is the operator's
// to change, and adding somebody to a system group is most of what this route is for.
func (s *Server) guardSystemGroup(r *http.Request, name string) error {
	if r.URL.Query().Get("allowSystem") == "true" {
		return nil
	}
	group, err := s.modules.linuxUsers.Group(r.Context(), name)
	if err != nil {
		return mapLinuxUserError(err)
	}
	if group.System {
		return httpx.Err(http.StatusForbidden, "system_group",
			fmt.Sprintf("%s belongs to the system: turn on editing for it first", name))
	}
	return nil
}

func (s *Server) handleSysGroupUpdate(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "group")
	ctx := r.Context()
	var req struct {
		NewName *string   `json:"newName,omitempty"`
		GID     *int      `json:"gid,omitempty"`
		Members *[]string `json:"members,omitempty"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.NewName != nil || req.GID != nil {
		if err := s.guardSystemGroup(r, name); err != nil {
			return err
		}
	}
	users := s.modules.linuxUsers
	httpx.SetAudit(r, "system.group.update", name, req)
	if req.Members != nil {
		if err := users.SetGroupMembers(ctx, name, *req.Members); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.GID != nil {
		if err := users.SetGroupID(ctx, name, *req.GID); err != nil {
			return mapLinuxUserError(err)
		}
	}
	if req.NewName != nil && *req.NewName != name {
		if err := users.RenameGroup(ctx, name, *req.NewName); err != nil {
			return mapLinuxUserError(err)
		}
	}
	httpx.SetAudit(r, "system.group.update", name, req)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleSysGroupDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "group")
	if err := s.guardSystemGroup(r, name); err != nil {
		return err
	}
	if err := s.modules.linuxUsers.DeleteGroup(r.Context(), name); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.group.delete", name, nil)
	httpx.NoContent(w)
	return nil
}

// --- defaults -----------------------------------------------------------------

func (s *Server) handleSysDefaults(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.linuxUsers.Defaults())
	return nil
}

func (s *Server) handleSysDefaultsSet(w http.ResponseWriter, r *http.Request) error {
	var req linuxusers.DefaultsChange
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.modules.linuxUsers.SetDefaults(req); err != nil {
		return mapLinuxUserError(err)
	}
	httpx.SetAudit(r, "system.accounts.defaults.set", "", req)
	return s.handleSysDefaults(w, r)
}

// --- templates ----------------------------------------------------------------

// accountTemplatesSetting is where the operator's own templates live. A template
// holds nothing secret: a shell, groups and how much of root, never a password.
const accountTemplatesSetting = "system_account_templates"

func (s *Server) customAccountTemplates(ctx context.Context) ([]linuxusers.Template, error) {
	raw, ok, err := s.Store.Setting(ctx, accountTemplatesSetting)
	if err != nil {
		return nil, err
	}
	out := []linuxusers.Template{}
	if !ok || raw == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("stored account templates could not be read: %w", err)
	}
	return out, nil
}

func (s *Server) handleSysTemplates(w http.ResponseWriter, r *http.Request) error {
	custom, err := s.customAccountTemplates(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, append(linuxusers.BuiltinTemplates(), custom...))
	return nil
}

// handleSysTemplatesSet replaces the operator's templates; the built-in ones are
// not theirs to change and are not stored.
func (s *Server) handleSysTemplatesSet(w http.ResponseWriter, r *http.Request) error {
	var req []linuxusers.Template
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	clean, err := linuxusers.CleanTemplates(req)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return httpx.Internal(err)
	}
	if err := s.Store.SetSetting(r.Context(), accountTemplatesSetting, string(encoded)); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "system.accounts.templates.set", "", map[string]any{"count": len(clean)})
	return s.handleSysTemplates(w, r)
}

// --- bulk ---------------------------------------------------------------------

// bulkResult is one account's outcome. A bulk action is many actions: one that
// fails does not undo or stop the rest, and the page says which.
type bulkResult struct {
	Username string `json:"username"`
	Error    string `json:"error,omitempty"`
}

// bulkTarget refuses what the single-account routes' middleware would have:
// system accounts are not swept up in a selection.
func (s *Server) bulkTarget(name string) error {
	user, err := s.modules.linuxUsers.Lookup(name)
	if err != nil {
		return err
	}
	if user.System {
		return fmt.Errorf("%s belongs to the system", name)
	}
	return nil
}

func (s *Server) handleSysBulk(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Action    string   `json:"action"`
		Usernames []string `json:"usernames"`
		Group     string   `json:"group"`
		// EndSessions goes with disable, as on one account.
		EndSessions bool `json:"endSessions"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	users := s.modules.linuxUsers
	apply := func(name string) error {
		switch req.Action {
		case "lock":
			return users.SetLocked(ctx, name, true)
		case "disable":
			if err := users.SetDisabled(ctx, name, true); err != nil {
				return err
			}
			if req.EndSessions {
				return users.EndSessions(ctx, name)
			}
			return nil
		case "enable":
			return users.SetDisabled(ctx, name, false)
		case "end-sessions":
			return users.EndSessions(ctx, name)
		case "add-group", "remove-group":
			account, err := users.Get(ctx, name)
			if err != nil {
				return err
			}
			next := []string{}
			for _, group := range account.Groups {
				if group != req.Group {
					next = append(next, group)
				}
			}
			if req.Action == "add-group" {
				next = append(next, req.Group)
			}
			return users.SetGroups(ctx, name, next)
		}
		return fmt.Errorf("unknown action %q", req.Action)
	}
	switch req.Action {
	case "lock", "disable", "enable", "end-sessions", "add-group", "remove-group":
	default:
		return httpx.BadRequest("unknown action %q", req.Action)
	}
	if req.Action == "end-sessions" || (req.Action == "disable" && req.EndSessions) {
		if err := s.spendDestructive(r); err != nil {
			return err
		}
	}
	results := s.bulkRun(req.Usernames, apply)
	httpx.SetAudit(r, "system.user.bulk."+req.Action, strings.Join(req.Usernames, ","),
		map[string]any{"group": req.Group, "results": results})
	httpx.JSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

func (s *Server) bulkRun(usernames []string, apply func(string) error) []bulkResult {
	results := make([]bulkResult, 0, len(usernames))
	for _, name := range usernames {
		result := bulkResult{Username: name}
		if err := s.bulkTarget(name); err != nil {
			result.Error = err.Error()
		} else if err := apply(name); err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results
}

// handleSysBulkDelete keeps every home: deleting many at once is not where the
// one unrecoverable choice gets made.
func (s *Server) handleSysBulkDelete(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Usernames   []string `json:"usernames"`
		EndSessions bool     `json:"endSessions"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	results := s.bulkRun(req.Usernames, func(name string) error {
		_, err := s.modules.linuxUsers.Delete(ctx, name, linuxusers.RemovalOptions{
			Home: linuxusers.HomeKeep, EndSessions: req.EndSessions,
		})
		return err
	})
	httpx.SetAudit(r, "system.user.bulk.delete", strings.Join(req.Usernames, ","),
		map[string]any{"results": results})
	httpx.JSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}
