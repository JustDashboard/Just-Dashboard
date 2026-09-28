package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// acmeAuthorityView is an authority the issue form offers, with what this
// host has for it. Whether an EAB is saved is said; the key never is.
type acmeAuthorityView struct {
	proxysvc.ACMEAuthorityOption
	EABSaved bool `json:"eabSaved"`
	Account  bool `json:"account"`
}

type acmeAccountsView struct {
	Accounts    []proxysvc.ACMEAccountEntry `json:"accounts"`
	Authorities []acmeAuthorityView         `json:"authorities"`
	// Default is whom an issuance orders from when the form names nobody:
	// Let's Encrypt, or what JD_ACME_DIRECTORY configures.
	Default proxysvc.IssueAuthority `json:"default"`
	// Error is why the contacts could not be asked; the accounts on disk
	// are still listed.
	Error string `json:"error,omitempty"`
}

// handleCertAccounts lists certbot's ACME accounts and the authorities the
// issue form offers.
func (s *Server) handleCertAccounts(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	view := acmeAccountsView{Authorities: []acmeAuthorityView{}}
	accounts, err := proxysvc.ACMEAccounts(ctx)
	view.Accounts = accounts
	if err != nil {
		view.Error = err.Error()
	}
	saved, err := s.savedEABDirectories(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	for _, option := range proxysvc.ACMEAuthorities() {
		authority, err := proxysvc.IssueAuthorityFor(proxysvc.IssueRequest{CA: option.Key})
		if err != nil {
			return httpx.Internal(err)
		}
		view.Authorities = append(view.Authorities, acmeAuthorityView{
			ACMEAuthorityOption: option, EABSaved: saved[option.Directory], Account: authority.Account,
		})
	}
	if view.Default, err = proxysvc.IssueAuthorityFor(proxysvc.IssueRequest{}); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// handleCertAccountEmail changes an account's contact with its authority,
// streamed like every other certbot run and exclusive with them.
func (s *Server) handleCertAccountEmail(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Server string `json:"server"`
		ID     string `json:"id"`
		Email  string `json:"email"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	account, args, err := proxysvc.UpdateAccountEmailArgs(req.Server, req.ID, req.Email)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "certificates.account.email", account.Server,
		map[string]any{"account": account.ID, "email": req.Email, "streamed": true})
	return s.startCertbotJob(w, r, jobs.Spec{
		Kind: "certbot.account", Title: "Changing the contact of the " + account.Authority + " account",
		Target: account.Server, Timeout: 2 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		cmd, err := proxysvc.CertbotCommand(ctx, nil, args...)
		if err != nil {
			return err
		}
		code, err := out.RunCmd(cmd, append([]string{"certbot"}, args...))
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("certbot exited %d — the last lines above say why", code)
		}
		return nil
	})
}

// issueBinding is the External Account Binding an issuance carries: the one
// to hand certbot, which registers an account with it, and the one typed
// into the form, to save sealed once the request is accepted.
type issueBinding struct {
	use, save *proxysvc.EAB
}

// planIssueBinding decides the binding for a planned issuance. It is used
// only to register an account, so with an account at the authority a saved
// one stays in the database.
func (s *Server) planIssueBinding(ctx context.Context, req proxysvc.IssueRequest, authority proxysvc.IssueAuthority) (issueBinding, error) {
	var binding issueBinding
	if req.EABKeyID != "" || req.EABHMACKey != "" {
		if authority.LetsEncrypt {
			return binding, httpx.BadRequest("Let's Encrypt registers accounts without External Account Binding")
		}
		eab, err := proxysvc.CheckEAB(req.EABKeyID, req.EABHMACKey)
		if err != nil {
			return binding, httpx.BadRequest("%v", err)
		}
		binding.save = &eab
		if !authority.Account {
			binding.use = &eab
		}
		return binding, nil
	}
	if authority.Account || authority.LetsEncrypt {
		return binding, nil
	}
	saved, err := s.savedEAB(ctx, authority.Server)
	if err != nil {
		return binding, httpx.Internal(err)
	}
	if saved == nil && authority.EABRequired {
		return binding, httpx.BadRequest("%s registers an account only with External Account Binding: paste the key ID and HMAC key from its console", authority.Name)
	}
	binding.use = saved
	return binding, nil
}

func (s *Server) savedEAB(ctx context.Context, directory string) (*proxysvc.EAB, error) {
	var keyID, sealed string
	err := s.Store.DB.QueryRowContext(ctx, `SELECT key_id, hmac_enc FROM acme_eab WHERE directory=?`, directory).Scan(&keyID, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hmacKey, err := s.Sealer.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("the saved EAB key for %s cannot be opened: %w", directory, err)
	}
	return &proxysvc.EAB{KeyID: keyID, HMACKey: hmacKey}, nil
}

func (s *Server) saveEAB(ctx context.Context, directory string, eab proxysvc.EAB) error {
	sealed, err := s.Sealer.Seal(eab.HMACKey)
	if err != nil {
		return err
	}
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO acme_eab(directory, key_id, hmac_enc, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(directory) DO UPDATE SET key_id=excluded.key_id, hmac_enc=excluded.hmac_enc, updated_at=excluded.updated_at`,
		directory, eab.KeyID, sealed, time.Now().Unix())
	return err
}

func (s *Server) savedEABDirectories(ctx context.Context) (map[string]bool, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT directory FROM acme_eab`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	saved := map[string]bool{}
	for rows.Next() {
		var directory string
		if err := rows.Scan(&directory); err != nil {
			return nil, err
		}
		saved[directory] = true
	}
	return saved, rows.Err()
}

// withBinding saves a typed binding and hands certbot the one to register
// with, for the length of run: the file is removed when run returns.
func (s *Server) withBinding(ctx context.Context, out jobs.Emitter, authority proxysvc.IssueAuthority, binding issueBinding, args []string, run func([]string) error) error {
	if binding.save != nil {
		if err := s.saveEAB(ctx, authority.Server, *binding.save); err != nil {
			return fmt.Errorf("the EAB key for %s could not be saved: %w", authority.Name, err)
		}
		out.Status("Saved the External Account Binding for %s, sealed with the dashboard's key.", authority.Name)
	}
	if binding.use == nil {
		return run(args)
	}
	path, cleanup, err := proxysvc.CertbotEABConfig(ctx, *binding.use)
	if err != nil {
		return err
	}
	defer cleanup()
	out.Status("certbot registers the account with %s through its External Account Binding, read from %s: a file only root can read, removed when the run ends.", authority.Name, path)
	return run(append(args[:len(args):len(args)], "--config", path))
}
