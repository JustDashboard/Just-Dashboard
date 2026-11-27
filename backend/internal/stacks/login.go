package stacks

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// The one login a stack's applications sign in with, and the one before the
// last change, kept until every application has been brought to the new
// one: a pass cut short halfway still signs in to those it had not reached.
const (
	CredentialLogin         = "login"
	CredentialLoginPrevious = "login-previous"
)

// maxLoginLength bounds a password every application of a stack is given in
// a form, a JSON body and a settings file.
const maxLoginLength = 128

// ValidateLogin is the dashboard's own password rule, with what an
// application's settings file can hold: no control character, at most 128
// characters.
func ValidateLogin(password string) error {
	if len(password) > maxLoginLength {
		return errors.New("password must be at most 128 characters")
	}
	if strings.ContainsFunc(password, unicode.IsControl) {
		return errors.New("password cannot contain control characters")
	}
	return auth.ValidatePasswordStrength(password)
}

// ChangeLogin stores a new login and keeps the current one as the previous,
// unless the current one is the new one already. An earlier previous that no
// pass cleared is replaced: every application but qBittorrent is changed
// with its API key whatever password it holds, and qBittorrent, left on a
// password two changes back, asks the person to set the new one.
func ChangeLogin(ctx context.Context, credentials CredentialStore, password string) error {
	current, found, err := credentials.Get(ctx, CredentialLogin)
	if err != nil {
		return err
	}
	if found && current == password {
		return nil
	}
	if found && current != "" {
		if err := credentials.Put(ctx, CredentialLoginPrevious, current); err != nil {
			return err
		}
	}
	return credentials.Put(ctx, CredentialLogin, password)
}
