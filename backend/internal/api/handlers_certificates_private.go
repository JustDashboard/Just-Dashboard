package api

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountPrivateCertificateRoutes is the certificates this server makes itself:
// a key and signing request for an authority to sign, a self-signed pair, and
// the local CA with the certificates it issues.
//
// The request list, the local CA's state and its root certificate are open to
// every signed-in account: none is a secret — a root is made to be installed
// on devices, by whoever uses them — and none reaches the network. The keys
// never leave through any route.
func (s *Server) mountPrivateCertificateRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/csr", s.handle(s.handleSigningRequests))
	r.Method(http.MethodGet, "/local-ca", s.handle(s.handleLocalCA))
	r.Method(http.MethodGet, "/local-ca/root.pem", s.handle(s.handleLocalCARoot))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/csr", s.handle(s.handleSigningRequestCreate))
		r.Method(http.MethodPost, "/csr/{name}/complete", s.handle(s.handleSigningRequestComplete))
		r.Method(http.MethodPost, "/self-signed", s.handle(s.handleSelfSigned))
		r.Method(http.MethodPost, "/local-ca", s.handle(s.handleLocalCACreate))
		r.Method(http.MethodPost, "/local-ca/issue", s.handle(s.handleLocalCAIssue))
		s.destructive(r, func(r chi.Router) {
			// Discarding a request deletes the only copy of its key; the
			// request is made again in a minute, so no phrase.
			r.Method(http.MethodDelete, "/csr/{name}", s.handle(s.handleSigningRequestDiscard))
		})
	})
}

// privateCertificateError answers a failure in the operator's words: what
// they can correct is a 400, a name in use a 409 naming what is there, and a
// file the server could not write a 500 that says which.
func privateCertificateError(what string, err error) error {
	var input *proxysvc.CertificateInputError
	var exists *proxysvc.ExistingImportError
	switch {
	case errors.As(err, &exists):
		return httpx.Err(http.StatusConflict, "certificate_exists", err.Error())
	case errors.Is(err, proxysvc.ErrRequestExists):
		return httpx.Err(http.StatusConflict, "request_exists", err.Error())
	case errors.Is(err, proxysvc.ErrLocalCAExists):
		return httpx.Err(http.StatusConflict, "local_ca_exists", err.Error())
	case errors.Is(err, proxysvc.ErrNoLocalCA):
		return httpx.Err(http.StatusConflict, "no_local_ca", err.Error())
	case errors.Is(err, proxysvc.ErrRequestNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.As(err, &input):
		return httpx.BadRequest("%v", err)
	}
	return httpx.Err(http.StatusInternalServerError, "write_failed", what+": "+err.Error())
}

func (s *Server) handleSigningRequests(w http.ResponseWriter, r *http.Request) error {
	requests, err := proxysvc.ListSigningRequests()
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "read_failed", err.Error()).Retry()
	}
	httpx.JSON(w, http.StatusOK, requests)
	return nil
}

func (s *Server) handleSigningRequestCreate(w http.ResponseWriter, r *http.Request) error {
	var in proxysvc.SigningRequestInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	request, err := proxysvc.CreateSigningRequest(in)
	if err != nil {
		return privateCertificateError("The key and request could not be saved", err)
	}
	httpx.SetAudit(r, "certificates.csr.create", request.Name,
		map[string]any{"domains": request.Domains, "keyType": request.KeyType})
	httpx.JSON(w, http.StatusCreated, request)
	return nil
}

type signingRequestCompletion struct {
	Certificate string `json:"certificate"`
	// Replace overwrites a certificate already kept under the request's
	// name, keeping it as .bak — how a bought certificate is renewed.
	Replace bool `json:"replace"`
}

func (s *Server) handleSigningRequestComplete(w http.ResponseWriter, r *http.Request) error {
	var in signingRequestCompletion
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	res, err := proxysvc.CompleteSigningRequest(httpx.URLParam(r, "name"), in.Certificate, in.Replace)
	if err != nil {
		return privateCertificateError("The certificate could not be saved", err)
	}
	httpx.SetAudit(r, "certificates.csr.complete", res.Name,
		map[string]any{"domains": res.Cert.Domains, "expires": res.Cert.NotAfter, "replaced": res.Replaced})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleSigningRequestDiscard(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	if err := proxysvc.DiscardSigningRequest(name); err != nil {
		return privateCertificateError("The request could not be removed", err)
	}
	httpx.SetAudit(r, "certificates.csr.discard", name, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleSelfSigned(w http.ResponseWriter, r *http.Request) error {
	var in proxysvc.PrivateCertificateRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	res, err := proxysvc.SelfSignCertificate(in)
	if err != nil {
		return privateCertificateError("The certificate could not be saved", err)
	}
	httpx.SetAudit(r, "certificates.self-signed", res.Name,
		map[string]any{"domains": res.Cert.Domains, "expires": res.Cert.NotAfter, "replaced": res.Replaced})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// localCAReading is the local CA with what its daily renewal last did and
// when it next looks.
type localCAReading struct {
	*proxysvc.LocalCA
	LastCheck *proxysvc.LocalCACheck `json:"lastCheck,omitempty"`
	NextCheck time.Time              `json:"nextCheck,omitzero"`
}

func (s *Server) handleLocalCA(w http.ResponseWriter, r *http.Request) error {
	state, err := s.modules.proxy.LocalCAState()
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "read_failed", err.Error()).Retry()
	}
	reading := localCAReading{LocalCA: state}
	if renewal := s.modules.proxyExtras.localCA; renewal != nil {
		reading.LastCheck, reading.NextCheck = renewal.reading()
	}
	httpx.JSON(w, http.StatusOK, reading)
	return nil
}

func (s *Server) handleLocalCACreate(w http.ResponseWriter, r *http.Request) error {
	state, err := proxysvc.CreateLocalCA()
	if err != nil {
		return privateCertificateError("The local CA could not be created", err)
	}
	httpx.SetAudit(r, "certificates.local-ca.create", state.Name,
		map[string]any{"fingerprint": state.Fingerprint, "expires": state.NotAfter})
	httpx.JSON(w, http.StatusCreated, state)
	return nil
}

func (s *Server) handleLocalCAIssue(w http.ResponseWriter, r *http.Request) error {
	var in proxysvc.PrivateCertificateRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	res, err := proxysvc.IssueFromLocalCA(in)
	if err != nil {
		return privateCertificateError("The certificate could not be saved", err)
	}
	httpx.SetAudit(r, "certificates.local-ca.issue", res.Name,
		map[string]any{"domains": res.Cert.Domains, "expires": res.Cert.NotAfter, "replaced": res.Replaced})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// handleLocalCARoot hands out the root to install on a device, as the type
// phones and desktops offer to install from a download.
func (s *Server) handleLocalCARoot(w http.ResponseWriter, r *http.Request) error {
	root, filename, err := proxysvc.LocalCARoot()
	if errors.Is(err, proxysvc.ErrNoLocalCA) {
		return httpx.Err(http.StatusNotFound, "no_local_ca", err.Error())
	}
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "read_failed", err.Error())
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(root)
	return nil
}

// localCARenewal renews the local CA's certificates once a day. The first
// check comes a few minutes after start rather than at it — a dashboard
// restarted daily would otherwise never reach its twenty-four hours — and
// out of the way of everything else a start does.
type localCARenewal struct {
	proxy    *proxysvc.Service
	record   func(check *proxysvc.LocalCACheck)
	log      *slog.Logger
	now      func() time.Time
	delay    time.Duration
	interval time.Duration

	mu     sync.Mutex
	last   *proxysvc.LocalCACheck
	next   time.Time
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Server) newLocalCARenewal() *localCARenewal {
	return &localCARenewal{
		proxy: s.modules.proxy,
		log:   s.Log,
		now:   time.Now,
		delay: 5 * time.Minute, interval: 24 * time.Hour,
		// A renewal is a change nobody asked for at that moment, so it is
		// written where every other change is read back from.
		record: func(check *proxysvc.LocalCACheck) {
			if len(check.Renewed) == 0 && len(check.Failed) == 0 && check.Error == "" {
				return
			}
			target := strings.Join(append(append([]string{}, check.Renewed...), check.Failed...), ", ")
			detail := strings.Join(check.Reloaded, ", ")
			if check.Error != "" {
				detail = check.Error
			}
			s.Audit.Record(context.Background(), audit.Entry{
				Actor: "system", Action: "certificates.local-ca.renew", Target: target,
				Success: check.Error == "" && len(check.Failed) == 0, Detail: detail,
			})
		},
	}
}

func (l *localCARenewal) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	l.mu.Lock()
	l.cancel, l.done = cancel, make(chan struct{})
	l.next = l.now().Add(l.delay)
	l.mu.Unlock()
	go func() {
		defer close(l.done)
		timer := time.NewTimer(l.delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			pass, stop := context.WithTimeout(ctx, 2*time.Minute)
			l.check(pass)
			stop()
			timer.Reset(l.interval)
		}
	}()
}

func (l *localCARenewal) Stop() {
	l.mu.Lock()
	cancel, done := l.cancel, l.done
	l.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// check is one pass: renew what is due and say what happened.
func (l *localCARenewal) check(ctx context.Context) *proxysvc.LocalCACheck {
	check := l.proxy.RenewLocalCALeaves(ctx, l.now())
	l.mu.Lock()
	l.last = check
	l.next = l.now().Add(l.interval)
	l.mu.Unlock()
	if l.record != nil {
		l.record(check)
	}
	if (check.Error != "" || len(check.Failed) > 0) && l.log != nil {
		l.log.Warn("the local CA's renewal needs attention", "failed", check.Failed, "error", check.Error)
	}
	return check
}

// reading is the last pass and the next, for the page; the next is unknown
// until the loop has started.
func (l *localCARenewal) reading() (*proxysvc.LocalCACheck, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last, l.next
}
