package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/updates"
	"github.com/go-chi/chi/v5"
)

// The host's software: what is on it, what is behind, and adding or removing
// one.
//
// This used to be `/updates` and answered only the middle question, which is
// why the page above it was half a screen of pending versions and nothing
// else. It is `/packages` now, and the dashboard's own version — which lived
// on the same page and is a completely different subject — has the whole of
// `/dashboard` to itself.
func (s *Server) mountPackageRoutes(r chi.Router) {
	r.Route("/packages", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handlePackageInventory))
		r.Method(http.MethodGet, "/updates", s.handle(s.handlePackageUpdates))
		r.Method(http.MethodGet, "/search", s.handle(s.handlePackageSearch))

		// Applying updates restarts services and can change how the machine
		// boots, so it sits with the other irreversible operations.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/upgrade", s.handle(s.handlePackageUpgrade))
		})

		// Refreshing the repository index is neither destructive nor a way to
		// run anything: it fetches signed metadata and writes a cache. It sits
		// at service.control because it is still the machine being made to do
		// something — and because "what can I install" is a question a limited
		// operator should be able to bring up to date without being able to
		// act on the answer.
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/refresh", s.handle(s.handlePackageRefresh))
		})

		// Installing a package runs the maintainer's scripts as root, which is
		// arbitrary code execution on the host by any honest reading — the same
		// tier as `api.authoriseSpec`'s privileged container, and gated the
		// same way. Removing one is that plus a loss, so it is inside
		// s.destructive as well.
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/install", s.handle(s.handlePackageInstall))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPost, "/remove", s.handle(s.handlePackageRemove))
			})
		})

		// Last, so the static paths above are never shadowed by a package
		// called "search".
		r.Method(http.MethodGet, "/{name}", s.handle(s.handlePackageDetail))
		r.Method(http.MethodGet, "/{name}/usage", s.handle(s.handlePackageUsage))
	})
}

// notSupported renders "this host has no package manager" as information
// rather than as a fault, the way every other optional module does.
func notSupported(err error) error {
	if errors.Is(err, updates.ErrNotSupported) {
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", err.Error())
	}
	return nil
}

func (s *Server) handlePackageInventory(w http.ResponseWriter, r *http.Request) error {
	inv, err := s.modules.updates.Inventory(r.Context())
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, inv)
	return nil
}

func (s *Server) handlePackageUpdates(w http.ResponseWriter, r *http.Request) error {
	rep, err := s.modules.updates.Check(r.Context())
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, rep)
	return nil
}

// handlePackageSearch is what the install box calls on every keystroke.
//
// It is a read, and deliberately so: finding out what a repository offers is
// not a privilege, and gating it behind the capability that installs would
// mean a limited operator could not tell an admin what to install. The
// expensive part is bounded inside the service — a ranked cap on results, and
// a cached installed index so annotating them is not a database read per
// character typed.
func (s *Server) handlePackageSearch(w http.ResponseWriter, r *http.Request) error {
	found, err := s.modules.updates.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, found)
	return nil
}

func (s *Server) handlePackageDetail(w http.ResponseWriter, r *http.Request) error {
	detail, err := s.modules.updates.Describe(r.Context(), chi.URLParam(r, "name"))
	switch {
	case errors.Is(err, updates.ErrUnknownPackage):
		return httpx.ErrNotFound
	case err != nil:
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

func (s *Server) handlePackageUsage(w http.ResponseWriter, r *http.Request) error {
	usage, err := s.modules.updates.Usage(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, usage)
	return nil
}

// handlePackageUpgrade starts the upgrade and hands back the job to watch.
//
// This was a request that held the connection open for up to half an hour,
// which is indistinguishable from a broken dashboard — and if the browser gave
// up, the operator was left with no idea whether apt was still running. It is
// a job now: it survives the tab, and the transcript is there to read
// afterwards whether or not anybody watched it happen.
func (s *Server) handlePackageUpgrade(w http.ResponseWriter, r *http.Request) error {
	securityOnly := r.URL.Query().Get("security") == "true"
	phrase := "upgrade packages"
	if securityOnly {
		phrase = "install security updates"
	}
	if err := httpx.RequireTypedConfirmation(w, r, phrase); err != nil {
		return err
	}
	name, args, env, err := s.modules.updates.UpgradeCommand(r.Context(), securityOnly)
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "system.updates.apply", name, map[string]any{
		"securityOnly": securityOnly, "streamed": true,
	})

	title := "Upgrading packages with " + name
	if securityOnly {
		title = "Installing security updates with " + name
	}
	s.startJob(w, r, jobs.Spec{
		Kind: "updates.apply", Title: title, Target: name, Timeout: 2 * time.Hour,
	}, func(ctx context.Context, out jobs.Emitter) error {
		defer s.modules.updates.Invalidate()
		code, err := out.RunEnv(ctx, env, name, args...)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("%s exited %d — the last lines above say why", name, code)
		}
		// The reboot flag is only meaningful after the packages have landed,
		// and it is the one thing an operator needs to know that the
		// transcript does not say plainly.
		if rep, err := s.modules.updates.Check(ctx); err == nil && rep.RebootRequired {
			out.Status("A restart is required: the running kernel and libraries are still the old ones.")
		}
		return nil
	})
	return nil
}

// handlePackageRefresh fetches a new repository index.
//
// Everything else on this page reads the index already on disk, deliberately:
// a search that refreshed first would take a minute per keystroke, and keeping
// it current is the host's own timer's job. But that timer stops on plenty of
// servers, and a three-month-old index is a catalogue that is missing every
// package added since — silently, because nothing on screen said how old it
// was. `Inventory.IndexAge` is the half that says; this is the half that fixes
// it.
func (s *Server) handlePackageRefresh(w http.ResponseWriter, r *http.Request) error {
	name, args, env, err := s.modules.updates.RefreshCommand()
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "system.packages.refresh", name, nil)

	s.startJob(w, r, jobs.Spec{
		Kind: "packages.refresh", Title: "Refreshing the package index with " + name,
		Target: name, Timeout: 15 * time.Minute,
	}, func(ctx context.Context, out jobs.Emitter) error {
		defer s.modules.updates.Invalidate()
		code, err := out.RunEnv(ctx, env, name, args...)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("%s exited %d — the last lines above say why", name, code)
		}
		return nil
	})
	return nil
}

type packageRequest struct {
	Packages []string `json:"packages"`
	// Purge additionally deletes the configuration the package left in /etc.
	// It is the one part of a removal with no way back, which is why it — and
	// only it — asks for the typed phrase.
	Purge bool `json:"purge"`
}

func (s *Server) handlePackageInstall(w http.ResponseWriter, r *http.Request) error {
	var body packageRequest
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	name, args, env, err := s.modules.updates.InstallCommand(body.Packages)
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	target := strings.Join(body.Packages, ", ")
	httpx.SetAudit(r, "system.packages.install", target, map[string]any{"manager": name})

	s.startJob(w, r, jobs.Spec{
		Kind: "packages.install", Title: "Installing " + target, Target: target,
		Timeout: 1 * time.Hour,
	}, func(ctx context.Context, out jobs.Emitter) error {
		defer s.modules.updates.Invalidate()
		code, err := out.RunEnv(ctx, env, name, args...)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("%s exited %d — the last lines above say why", name, code)
		}
		// The commands a package installs are almost never its own name, and
		// this is the moment somebody is actually looking. Saying it here
		// costs one file listing and saves the search that follows.
		for _, pkg := range body.Packages {
			usage, err := s.modules.updates.Usage(ctx, pkg)
			if err != nil || len(usage.Commands) == 0 {
				continue
			}
			out.Status("%s installed the command%s %s.",
				pkg, plural(len(usage.Commands)), strings.Join(usage.Commands, ", "))
		}
		return nil
	})
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// handlePackageRemove takes packages off the host.
//
// It is destructive and confirmed, but the phrase is asked for only when the
// configuration goes too. That is invariant 3's frequency test applied at the
// call site, the same way handleFileDelete narrows to `recursive`: an ordinary
// removal is undone by installing the package again from the same repository
// it came from, and a phrase in front of something with a path back is a
// phrase that teaches people to type phrases. Deleting the /etc files somebody
// spent an afternoon on has no path back at all.
func (s *Server) handlePackageRemove(w http.ResponseWriter, r *http.Request) error {
	var body packageRequest
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	target := strings.Join(body.Packages, ", ")
	if body.Purge {
		if err := httpx.RequireTypedConfirmation(w, r, "purge "+target); err != nil {
			return err
		}
	}
	name, args, env, err := s.modules.updates.RemoveCommand(r.Context(), body.Packages, body.Purge)
	if err != nil {
		if e := notSupported(err); e != nil {
			return e
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "system.packages.remove", target, map[string]any{
		"manager": name, "purge": body.Purge,
	})

	title := "Removing " + target
	if body.Purge {
		title = "Removing " + target + " and its configuration"
	}
	s.startJob(w, r, jobs.Spec{
		Kind: "packages.remove", Title: title, Target: target, Timeout: 1 * time.Hour,
	}, func(ctx context.Context, out jobs.Emitter) error {
		defer s.modules.updates.Invalidate()
		code, err := out.RunEnv(ctx, env, name, args...)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("%s exited %d — the last lines above say why", name, code)
		}
		return nil
	})
	return nil
}
