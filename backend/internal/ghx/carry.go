package ghx

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CarryLogin signs the account that owns dir in to GitHub as the dashboard's
// own account is, when it is not signed in itself.
//
// It exists for the checkout selfupdate has just given away. A checkout that
// was root's used root's login — the one an operator made with `sudo gh auth
// login` or from this page before the handover — and once it is another
// account's, gh runs as that account and finds nothing: the page reads signed
// out and every push is anonymous. Copying the token grants nothing new, for
// the reason the handover does: the account that owns the checkout's
// directory can already replace what the next upgrade builds as root. An
// account with a login of its own keeps it.
//
// It reports whether it signed anybody in.
func (s *Service) CarryLogin(ctx context.Context, dir string) (bool, error) {
	if dir == "" || ownerName(dir) == "" || !s.Available() {
		return false, nil
	}
	if acc, err := s.Status(ctx, dir); err != nil || acc.LoggedIn {
		return false, err
	}
	root, err := s.Status(ctx, "")
	if err != nil || !root.LoggedIn {
		return false, err
	}
	host := root.Host
	if host == "" {
		host = DefaultHost
	}
	out, err := s.run(ctx, "", "", "auth", "token", "--hostname", host)
	token := strings.TrimSpace(out)
	if err != nil || token == "" || strings.ContainsAny(token, " \n") {
		return false, errors.New("could not read the dashboard's own GitHub token")
	}
	if _, err := s.LoginWithToken(ctx, dir, host, token); err != nil {
		return false, fmt.Errorf("sign %s in: %w", ownerName(dir), err)
	}
	return true, nil
}
