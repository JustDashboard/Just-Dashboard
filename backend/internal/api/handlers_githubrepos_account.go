package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The connected account itself on the GitHub page, under
// /github/connections/{connection}: its profile with the contribution
// calendar, its pull requests and issues across every repository, and what
// it did lately.
//
// All four are reads as the person themselves, with their own token, so
// each needs only a session and none is audited. Each is answered from
// memory for a short while (the profile five minutes, the rest one), since
// the page shows them on every visit and GitHub's search allows thirty
// requests a minute; ?refresh=1 asks GitHub again.
//
// The login a search is about is always the connection's own. A request
// chooses a filter, a state and a page, each checked before GitHub is asked.
func (s *Server) mountGitHubAccountReads(r chi.Router) {
	r.Method(http.MethodGet, "/connections/{connection}/profile", s.handle(s.handleGitHubProfile))
	r.Method(http.MethodGet, "/connections/{connection}/pulls", s.handle(s.handleGitHubInboxPulls))
	r.Method(http.MethodGet, "/connections/{connection}/issues", s.handle(s.handleGitHubInboxIssues))
	r.Method(http.MethodGet, "/connections/{connection}/events", s.handle(s.handleGitHubEvents))
}

// ghAccountRead is the shape of the four: the connection's id, the refresh
// flag, ask, answer 200.
func ghAccountRead[T any](w http.ResponseWriter, r *http.Request, read func(connection int64, refresh bool) (T, error)) error {
	id, err := githubConnectionID(r)
	if err != nil {
		return err
	}
	refresh, err := httpx.QueryBool(r, "refresh")
	if err != nil {
		return err
	}
	out, err := read(id, refresh)
	if err != nil {
		return githubPageErr(err)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleGitHubProfile(w http.ResponseWriter, r *http.Request) error {
	return ghAccountRead(w, r, func(connection int64, refresh bool) (githubrepos.Profile, error) {
		return s.modules.githubRepos.Profile(r.Context(), githubUser(r), connection, refresh)
	})
}

func (s *Server) handleGitHubInboxPulls(w http.ResponseWriter, r *http.Request) error {
	return s.githubInbox(w, r, githubrepos.InboxPulls)
}

func (s *Server) handleGitHubInboxIssues(w http.ResponseWriter, r *http.Request) error {
	return s.githubInbox(w, r, githubrepos.InboxIssues)
}

// githubInbox reads the filter, state and page before the connection is
// looked up, so a value GitHub's search would not take costs no request.
func (s *Server) githubInbox(w http.ResponseWriter, r *http.Request, kind string) error {
	values := r.URL.Query()
	query, err := githubrepos.ParseInboxQuery(kind, values.Get("filter"), values.Get("state"), values.Get("page"))
	if err != nil {
		return githubPageErr(err)
	}
	return ghAccountRead(w, r, func(connection int64, refresh bool) (githubrepos.InboxPage, error) {
		return s.modules.githubRepos.Inbox(r.Context(), githubUser(r), connection, query, refresh)
	})
}

func (s *Server) handleGitHubEvents(w http.ResponseWriter, r *http.Request) error {
	return ghAccountRead(w, r, func(connection int64, refresh bool) (githubrepos.EventList, error) {
		return s.modules.githubRepos.Events(r.Context(), githubUser(r), connection, refresh)
	})
}
