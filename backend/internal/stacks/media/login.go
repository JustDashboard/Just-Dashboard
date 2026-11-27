package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// loginSteps are the steps that bring an application to the stack's login:
// once every one of them is done, the previous login is no longer needed.
var loginSteps = []string{"jellyfin.setup", "qbittorrent.auth", "radarr.auth", "sonarr.auth", "prowlarr.auth", "bazarr.auth"}

// stackLogin is the one password every application signs in with as admin.
//
// A stack made before there was one adopts Jellyfin's administrator
// password, which Seerr's owner also signs in with: the person's Jellyfin
// and Seerr sessions and saved passwords keep working, and every other
// application is brought to it on this pass. A new stack takes the password
// the person chose when deploying, if they chose one the dashboard's rule
// accepts, and otherwise generates one. From then on only the route that
// changes it does.
func stackLogin(ctx context.Context, env stacks.Env) (string, error) {
	if value, found, err := env.LookupCredential(ctx, CredentialLogin); err != nil || (found && value != "") {
		return value, err
	}
	if value, found, err := env.LookupCredential(ctx, legacyJellyfinAdmin); err != nil {
		return "", err
	} else if found && value != "" {
		env.Logf("the stack's one login takes Jellyfin's administrator password, so Jellyfin and Seerr sign in as before")
		return value, env.SetCredential(ctx, CredentialLogin, value)
	}
	if value, err := env.Variable(ctx, VariableLoginPassword); err == nil && value != "" {
		if err := stacks.ValidateLogin(value); err != nil {
			env.Logf("the password chosen for every app was not used (%v); the stack made one instead", err)
		} else {
			return value, env.SetCredential(ctx, CredentialLogin, value)
		}
	}
	return env.Credential(ctx, CredentialLogin)
}

// loginCandidates are the passwords an application may still hold, newest
// first: the login, the one before its last change, and the application's
// own from before there was one login.
func loginCandidates(ctx context.Context, env stacks.Env, legacy string) (login string, older []string, err error) {
	if login, err = stackLogin(ctx, env); err != nil {
		return "", nil, err
	}
	for _, name := range []string{CredentialLoginPrevious, legacy} {
		if name == "" {
			continue
		}
		value, found, err := env.LookupCredential(ctx, name)
		if err != nil {
			return "", nil, err
		}
		if found && value != "" && value != login {
			older = append(older, value)
		}
	}
	return login, older, nil
}

// forgetLegacy removes an application's own password from before the one
// login, once the application answers to the login.
func forgetLegacy(ctx context.Context, env stacks.Env, name string) error {
	if _, found, err := env.LookupCredential(ctx, name); err != nil || !found {
		return err
	}
	env.Logf("removed the stack's old separate password for this app; it signs in with the one login")
	return env.DeleteCredential(ctx, name)
}

// loginDigest is what a marker records of a password: enough to tell it was
// this one, and nothing a page could show.
func loginDigest(scope, password string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + password))
	return hex.EncodeToString(sum[:])
}

// loginVerify runs after every application has been brought to the login:
// the previous one is then nothing any of them still holds.
func loginVerify(ctx context.Context, env stacks.Env) (stacks.Result, error) {
	if _, err := stackLogin(ctx, env); err != nil {
		return stacks.Result{}, err
	}
	if _, found, err := env.LookupCredential(ctx, CredentialLoginPrevious); err != nil {
		return stacks.Result{}, err
	} else if found {
		if err := env.DeleteCredential(ctx, CredentialLoginPrevious); err != nil {
			return stacks.Result{}, err
		}
		env.Logf("every app takes the new password; the previous one is no longer kept")
	}
	return stacks.Result{Message: "Every app signs in as " + AdminUsername + " with the stack's one password"}, nil
}
