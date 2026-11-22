package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/go-chi/chi/v5"
)

// PLACEHOLDER owned by lane B-core: replace this whole file. See
// /home/ubuntu/just-dashboard-github-2026-11-22/CONTRACT.md for every route
// these two functions mount, and resolveGitHubRepoAccess's contract.

func (s *Server) mountGitHubAccountRoutes(r chi.Router) {}

func (s *Server) mountGitHubRepositoryRoutes(r chi.Router) {}

func (s *Server) resolveGitHubRepoAccess(r *http.Request) (*githubrepos.RepoAccess, error) {
	return nil, githubrepos.ErrNotConfigured
}

// forgetGitHubRepo drops what is cached about one repository after a change
// to it (its access, the connection's repository list).
func (s *Server) forgetGitHubRepo(access *githubrepos.RepoAccess) {}
