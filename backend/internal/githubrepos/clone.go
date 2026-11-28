package githubrepos

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
)

// Cloning onto this server fetches with a token that can do nothing but
// read one repository's contents for an hour: an installation token minted
// through the App's own key, downscoped to the repository and to
// contents: read. The person's own token is never handed to git. The token
// reaches git only through its environment, scoped to the exact remote, and
// is revoked as soon as the clone ends; the checkout's stored remote is the
// plain HTTPS address, and later fetches use the checkout owner's own
// credentials, as every other checkout's do.

// CloneURL is the address a repository is cloned from, built from GitHub's
// own spelling of its owner and name, never from the request.
func (s *Service) CloneURL(access *RepoAccess) string {
	return strings.TrimRight(s.GitURL, "/") + "/" + access.Owner + "/" + access.Name + ".git"
}

// NeedsToken reports whether the repository needs a credential to clone.
// Internal repositories are private to everyone outside their enterprise.
func NeedsToken(access *RepoAccess) bool {
	return access.Repository.Visibility != "public"
}

// CloneToken mints the downscoped installation token for a private
// repository's clone, and the function that revokes it. A public repository
// gets no token and a revoke that does nothing.
func (s *Service) CloneToken(ctx context.Context, access *RepoAccess) (string, func(), error) {
	if !NeedsToken(access) {
		return "", func() {}, nil
	}
	if !access.Installation.Grants("contents", "read") {
		return "", func() {}, &NotPermittedError{Reason: fmt.Sprintf("The GitHub App was not granted Contents: read on %s, so it cannot fetch %s. Accept the permission on GitHub, then refresh.", access.Installation.Account.Login, access.Repository.FullName)}
	}
	return s.repositoryToken(ctx, access, "read")
}

// PushToken mints an installation token that can do nothing but write one
// repository's contents, for one push of the assistant's workspace, and the
// function that revokes it. It is minted only for a person whose own GitHub
// account may push to the repository (Repository.Can.Push), so the token never
// reaches further than they do; the person's own token is never handed to git.
func (s *Service) PushToken(ctx context.Context, access *RepoAccess) (string, func(), error) {
	if !access.Repository.Can.Push {
		return "", func() {}, &NotPermittedError{Reason: fmt.Sprintf("%s cannot push to %s on GitHub.", access.Login, access.Repository.FullName)}
	}
	if !access.Installation.Grants("contents", "write") {
		return "", func() {}, &NotPermittedError{Reason: fmt.Sprintf("The GitHub App was not granted Contents: write on %s, so it cannot push to %s. Accept the permission on GitHub, then refresh.", access.Installation.Account.Login, access.Repository.FullName)}
	}
	return s.repositoryToken(ctx, access, "write")
}

// repositoryToken mints an installation token narrowed to one repository and to
// contents at level, and the function that revokes it.
func (s *Service) repositoryToken(ctx context.Context, access *RepoAccess, level string) (string, func(), error) {
	app, err := s.store.LoadApp(ctx)
	if err != nil {
		return "", func() {}, err
	}
	appClient, err := githubapp.NewClient(githubapp.Credentials{ID: app.ID, ClientID: app.ClientID, PrivateKey: app.privateKey})
	if err != nil {
		return "", func() {}, err
	}
	jwt, err := appClient.JWT()
	if err != nil {
		return "", func() {}, err
	}
	asApp := NewClient(s.APIURL, s.HTTP, func(context.Context) (string, error) { return jwt, nil })
	var minted struct {
		Token string `json:"token"`
	}
	body := DownscopedTokenRequest(access.Repository.ID)
	body["permissions"] = map[string]string{"contents": level}
	if _, err := asApp.Do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(access.Installation.ID, 10)+"/access_tokens", body, &minted); err != nil {
		return "", func() {}, err
	}
	if minted.Token == "" {
		return "", func() {}, &APIError{Status: http.StatusBadGateway, Kind: KindUnavailable, Message: "GitHub returned no installation token", Path: "/app/installations/{id}/access_tokens"}
	}
	token := minted.Token
	revoke := func() {
		ctx, cancel := backgroundContext()
		defer cancel()
		asToken := NewClient(s.APIURL, s.HTTP, func(context.Context) (string, error) { return token, nil })
		_, _ = asToken.Request(ctx, http.MethodDelete, "/installation/token", nil)
	}
	return token, revoke, nil
}

// DownscopedTokenRequest is the body that narrows an installation token to
// one repository and to reading its contents.
func DownscopedTokenRequest(repositoryID int64) map[string]any {
	return map[string]any{
		"repository_ids": []int64{repositoryID},
		"permissions":    map[string]string{"contents": "read"},
	}
}

// CloneEnvironment is the git environment that carries token for one clone
// of remote: git's GIT_CONFIG_COUNT/KEY/VALUE, which set configuration for
// this process only, never in a file and never in its arguments, so neither
// `ps` nor the checkout's .git/config ever shows it.
//
// credential.helper is reset to empty first, so no helper of the checkout
// owner's is asked for, or told to store, anything about this remote. The
// header is set under http.<remote>.extraHeader, which git sends only to
// that exact repository's address, not to another path on the same host and
// not to a host a redirect leads to.
func CloneEnvironment(remote, token string) map[string]string {
	env := map[string]string{
		"GIT_CONFIG_COUNT":   "1",
		"GIT_CONFIG_KEY_0":   "credential.helper",
		"GIT_CONFIG_VALUE_0": "",
	}
	if token == "" {
		return env
	}
	env["GIT_CONFIG_COUNT"] = "2"
	env["GIT_CONFIG_KEY_1"] = "http." + remote + ".extraHeader"
	env["GIT_CONFIG_VALUE_1"] = "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	return env
}
