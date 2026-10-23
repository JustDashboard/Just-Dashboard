package selfupdate

import (
	"context"
	"path/filepath"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Giving the checkout to the account whose directory it sits in.
//
// An upgrade's handBack returns what its root `git pull` wrote to the
// checkout's owner, but a checkout cloned with sudo into somebody's home has
// no owner but root to return it to. git then refuses that account the
// repository ("dubious ownership"), and with it `git worktree add`; an agent
// told to work in a worktree clones the repository beside it instead, and the
// Git page lists the clone as a second repository rather than as a worktree
// of the first.
//
// Handing the tree to the owner of the directory holding it grants nothing:
// whoever owns that directory can already rename the checkout away and put
// their own in its place, which is what the next upgrade would build. A
// checkout in a root-owned directory — /opt, /srv — is left exactly as it is.

// adoptCheckout gives the install checkout's root-owned files to the owner of
// its parent directory. It is quiet by design: there is nothing for an
// operator to decide, and an install it does not apply to is the common case.
func (s *Service) adoptCheckout(ctx context.Context) {
	loc, err := s.Location(ctx)
	if err != nil || loc.Visible == "" {
		return
	}
	owner, ok := hostexec.OwnerOf(filepath.Dir(loc.Visible))
	if !ok {
		return
	}
	// An upgrade writes into the same tree; the next start settles whatever
	// it left behind.
	release, err := LockLifecycle(s.dataDir)
	if err != nil {
		return
	}
	defer release()
	if CheckLifecycleIdle(s.dataDir) != nil {
		return
	}
	n, err := hostexec.Adopt(loc.Visible, owner)
	if s.log == nil {
		return
	}
	if err != nil {
		s.log.Warn("could not give the install checkout to the owner of its directory",
			"dir", loc.Dir, "owner", owner.Name, "err", err)
	} else if n > 0 {
		s.log.Info("gave the install checkout to the owner of its directory",
			"dir", loc.Dir, "owner", owner.Name, "entries", n)
	}
}
