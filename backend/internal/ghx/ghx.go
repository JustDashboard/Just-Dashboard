// Package ghx is the GitHub half of the git page: which account this server is
// signed in as, how it got signed in, and the one operation git itself has no
// verb for — opening a pull request.
//
// Everything here drives the `gh` binary with an explicit argument vector,
// never through a shell, and runs it in the repository's directory as the
// account that owns it — the same rule gitx follows, and for a reason that is
// load-bearing rather than tidy: gh stores the token under that account's
// home, and git later asks that same account's credential helper for it. Sign
// in as root and push as `deploy` and the push is anonymous again.
//
// The one thing gh cannot do from a web request is its own interactive login,
// so device.go performs the OAuth device flow itself and hands gh the finished
// token through `gh auth login --with-token`, which is non-interactive by
// design. The result is a credential gh owns, in the place gh keeps it.
package ghx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var (
	ErrNotInstalled = errors.New("the GitHub CLI (gh) is not installed on this host")
	ErrNotLoggedIn  = errors.New("not signed in to GitHub")
)

// DefaultHost is the only host the sign-in flow can reach. GitHub Enterprise
// installs have their own device endpoint and their own OAuth application, so
// they sign in with a token instead — which is why the token path exists.
const DefaultHost = "github.com"

type Service struct {
	http    *http.Client
	command func(context.Context, string, string, ...string) (string, error)

	mu       sync.Mutex
	flows    map[string]*deviceFlow
	profiles map[string]cachedProfile
}

func New() *Service {
	return &Service{
		http:     &http.Client{Timeout: 20 * time.Second},
		flows:    map[string]*deviceFlow{},
		profiles: map[string]cachedProfile{},
	}
}

// Available reports whether gh can be run at all.
//
// Only this container's own copy counts. The host's gh would run as root in
// the host's namespaces, store its token in the host root's home and write a
// credential helper naming a path — none of which the account that owns the
// repository, and therefore runs the push, can use.
func (s *Service) Available() bool {
	if s.command != nil {
		return true
	}
	_, err := exec.LookPath("gh")
	return err == nil
}

// Account is who the server is signed in as, from the point of view of one
// repository — which is the only point of view there is, since the credential
// belongs to the host account that owns the checkout.
type Account struct {
	LoggedIn bool     `json:"loggedIn"`
	Host     string   `json:"host,omitempty"`
	Login    string   `json:"login,omitempty"`
	Name     string   `json:"name,omitempty"`
	Avatar   string   `json:"avatarUrl,omitempty"`
	Profile  string   `json:"profileUrl,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
	// Owner is the host account whose credentials these are, so the page can
	// say "signed in for deploy" rather than implying the answer is global.
	Owner string `json:"owner,omitempty"`
	// GitConfigured is whether git in this repository will actually use the
	// account: a committer identity for the commits, and — over HTTPS — the
	// credential helper for the push.
	GitConfigured bool   `json:"gitConfigured"`
	CommitterName string `json:"committerName,omitempty"`
	CommitterMail string `json:"committerEmail,omitempty"`
	// RemoteProtocol is how this repository's origin is reached, and it
	// decides whether the token is involved in a push at all: an SSH remote
	// authenticates with the server's key and never asks a credential helper
	// anything. Worth saying rather than warning about a helper that would
	// change nothing.
	RemoteProtocol string `json:"remoteProtocol,omitempty"`
	// Reason carries gh's own words when it says nobody is signed in, since
	// they distinguish "never logged in" from "the token has been revoked".
	Reason string `json:"reason,omitempty"`
}

type cachedProfile struct {
	at      time.Time
	profile profile
}

type profile struct {
	Login  string `json:"login"`
	Name   string `json:"name"`
	Avatar string `json:"avatar_url"`
	URL    string `json:"html_url"`
	ID     int64  `json:"id"`
}

// Status answers "who is this server, to GitHub, in this repository".
//
// The profile half is a network call to GitHub, so it is cached for a few
// minutes per account: the page polls this, and a dashboard left open on a
// screen should not spend the account's API budget on an avatar that has not
// changed.
func (s *Service) Status(ctx context.Context, dir string) (*Account, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	out, err := s.run(ctx, dir, "", "auth", "status")
	acc := parseAuthStatus(out)
	acc.Owner = ownerName(dir)
	if err != nil && !acc.LoggedIn {
		// gh exits non-zero when nobody is signed in, which is an ordinary
		// answer rather than a failure — its message is the useful part.
		acc.Reason = firstMeaningfulLine(out)
		return acc, nil
	}
	acc.CommitterName, acc.CommitterMail = s.identity(ctx, dir)
	acc.RemoteProtocol = s.remoteProtocol(ctx, dir)
	acc.GitConfigured = s.willUseTheAccount(ctx, dir, acc)
	if p, ok := s.profileFor(ctx, dir, acc.Host, acc.Login); ok {
		acc.Name, acc.Avatar, acc.Profile = p.Name, p.Avatar, p.URL
	}
	return acc, nil
}

func (s *Service) profileFor(ctx context.Context, dir, host, login string) (profile, bool) {
	key := host + "/" + login
	s.mu.Lock()
	if c, ok := s.profiles[key]; ok && time.Since(c.at) < 5*time.Minute {
		s.mu.Unlock()
		return c.profile, true
	}
	s.mu.Unlock()

	out, err := s.run(ctx, dir, "", "api", "user")
	if err != nil {
		return profile{}, false
	}
	var p profile
	if json.Unmarshal([]byte(out), &p) != nil || p.Login == "" {
		return profile{}, false
	}
	s.mu.Lock()
	s.profiles[key] = cachedProfile{at: time.Now(), profile: p}
	s.mu.Unlock()
	return p, true
}

// forget drops a cached profile, so signing out or signing in as somebody else
// is reflected on the next poll rather than five minutes later.
func (s *Service) forget() {
	s.mu.Lock()
	s.profiles = map[string]cachedProfile{}
	s.mu.Unlock()
}

var (
	// gh has printed this line two ways across the versions a server is
	// likely to carry: "Logged in to github.com as name" on the older ones,
	// "Logged in to github.com account name" since 2.40. Both are matched
	// because the Debian and Homebrew copies of gh are years apart.
	reLoggedIn = regexp.MustCompile(`Logged in to (\S+) (?:as|account) ([A-Za-z0-9-]+)`)
	reScopes   = regexp.MustCompile(`Token scopes:\s*(.+)`)
	reProtocol = regexp.MustCompile(`Git operations (?:protocol: (\w+)|for \S+ configured to use (\w+) protocol)`)
)

// parseAuthStatus reads `gh auth status`, which has no --json and never will:
// it is written for a person. The fields taken from it are the ones the page
// shows, and every one of them is optional, so a future rewording costs a
// missing scope list rather than a broken sign-in state.
func parseAuthStatus(out string) *Account {
	acc := &Account{}
	if m := reLoggedIn.FindStringSubmatch(out); m != nil {
		acc.LoggedIn, acc.Host, acc.Login = true, m[1], m[2]
	}
	if m := reScopes.FindStringSubmatch(out); m != nil {
		for _, s := range strings.Split(m[1], ",") {
			if s = strings.Trim(strings.TrimSpace(s), "'\""); s != "" {
				acc.Scopes = append(acc.Scopes, s)
			}
		}
	}
	if m := reProtocol.FindStringSubmatch(out); m != nil {
		acc.Protocol = m[1] + m[2]
	}
	return acc
}

func firstMeaningfulLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-✓✗X "))
		if line != "" && !strings.HasSuffix(line, ":") {
			return line
		}
	}
	return ""
}

// LoginWithToken hands gh a finished token and then makes git able to use it.
//
// The three steps are one operation, because any two of them without the third
// is a state an operator cannot see: a stored token with no credential helper
// pushes anonymously, and a credential helper with no committer identity fails
// at the commit instead of at the push. The identity is only written when the
// account has none — somebody who has set their own name is not overruled.
func (s *Service) LoginWithToken(ctx context.Context, dir, host, token string) (*Account, error) {
	if !s.Available() {
		return nil, ErrNotInstalled
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("a token is required")
	}
	if host == "" {
		host = DefaultHost
	}
	if out, err := s.run(ctx, dir, token+"\n", "auth", "login", "--hostname", host, "--with-token"); err != nil {
		return nil, fmt.Errorf("gh auth login: %s", firstMeaningfulLine(out))
	}
	s.forget()
	if out, err := s.run(ctx, dir, "", "auth", "setup-git", "--hostname", host); err != nil {
		return nil, fmt.Errorf("gh auth setup-git: %s", firstMeaningfulLine(out))
	}
	acc, err := s.Status(ctx, dir)
	if err != nil {
		return nil, err
	}
	s.ensureIdentity(ctx, dir, acc)
	return acc, nil
}

// Configure makes git here use the account that is already signed in.
//
// It is the remedy the page offers instead of a paragraph explaining what is
// wrong: the credential helper and a committer identity are the two halves of
// "will this actually be my commit", and both are one command the operator has
// no terminal to run. Signing in does this already — this exists for the
// account that was signed in before the identity was written, or whose git
// config has since been changed by something else.
func (s *Service) Configure(ctx context.Context, dir string) (*Account, error) {
	acc, err := s.Status(ctx, dir)
	if err != nil {
		return nil, err
	}
	if !acc.LoggedIn {
		return nil, ErrNotLoggedIn
	}
	host := acc.Host
	if host == "" {
		host = DefaultHost
	}
	if out, err := s.run(ctx, dir, "", "auth", "setup-git", "--hostname", host); err != nil {
		return nil, fmt.Errorf("gh auth setup-git: %s", firstMeaningfulLine(out))
	}
	s.ensureIdentity(ctx, dir, acc)
	return acc, nil
}

// Logout removes the stored credential. It is recoverable by definition —
// signing in again is the whole of the remedy — so it carries no typed
// confirmation, only the pause the dialog gives it.
func (s *Service) Logout(ctx context.Context, dir, host string) error {
	if !s.Available() {
		return ErrNotInstalled
	}
	if host == "" {
		host = DefaultHost
	}
	out, err := s.run(ctx, dir, "", "auth", "logout", "--hostname", host)
	s.forget()
	if err != nil {
		return fmt.Errorf("gh auth logout: %s", firstMeaningfulLine(out))
	}
	return nil
}

// identity reads the committer git would record here. Repository config wins
// over global, exactly as git resolves it, so a repo with its own identity
// reports that one.
func (s *Service) identity(ctx context.Context, dir string) (name, email string) {
	name, _ = s.git(ctx, dir, "config", "--get", "user.name")
	email, _ = s.git(ctx, dir, "config", "--get", "user.email")
	return strings.TrimSpace(name), strings.TrimSpace(email)
}

// ensureIdentity gives the account a committer identity when it has none.
//
// The address is GitHub's own no-reply form, which is what attributes the
// commit to the signed-in account without publishing a personal address in
// every repository this server pushes to. A person who has already set a name
// and address keeps them.
func (s *Service) ensureIdentity(ctx context.Context, dir string, acc *Account) {
	if !acc.LoggedIn || acc.Login == "" {
		return
	}
	if acc.CommitterName == "" {
		name := acc.Name
		if name == "" {
			name = acc.Login
		}
		if _, err := s.git(ctx, dir, "config", "--global", "user.name", name); err == nil {
			acc.CommitterName = name
		}
	}
	if acc.CommitterMail == "" {
		mail := acc.Login + "@users.noreply.github.com"
		if p, ok := s.profileFor(ctx, dir, acc.Host, acc.Login); ok && p.ID > 0 {
			mail = fmt.Sprintf("%d+%s@users.noreply.github.com", p.ID, p.Login)
		}
		if _, err := s.git(ctx, dir, "config", "--global", "user.email", mail); err == nil {
			acc.CommitterMail = mail
		}
	}
	acc.GitConfigured = s.willUseTheAccount(ctx, dir, acc)
}

// willUseTheAccount is the question the chip answers with one dot: would a
// commit and a push made from this page be this account's?
func (s *Service) willUseTheAccount(ctx context.Context, dir string, acc *Account) bool {
	if acc.CommitterName == "" || acc.CommitterMail == "" {
		return false
	}
	if acc.RemoteProtocol == "ssh" {
		return true
	}
	return s.credentialHelperSet(ctx, dir, acc.Host)
}

// remoteProtocol reads how origin is reached. An scp-style address
// (git@github.com:owner/repo) has no scheme at all, which is exactly the form
// most machines are set up with, so it is matched on its shape rather than by
// looking for "ssh://".
func (s *Service) remoteProtocol(ctx context.Context, dir string) string {
	out, err := s.git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(out)
	switch {
	case strings.HasPrefix(url, "https://"), strings.HasPrefix(url, "http://"):
		return "https"
	case strings.HasPrefix(url, "ssh://"), strings.Contains(url, "@") && strings.Contains(url, ":"):
		return "ssh"
	case url == "":
		return ""
	}
	return "other"
}

// credentialHelperSet reports whether an HTTPS push from this repository would
// find the token. gh writes the helper as a `credential.<host>.helper` entry;
// asking git for the resolved value is more honest than reading the file,
// since a repository may override the global one.
func (s *Service) credentialHelperSet(ctx context.Context, dir, host string) bool {
	if host == "" {
		host = DefaultHost
	}
	out, _ := s.git(ctx, dir, "config", "--get-regexp", `^credential\.`)
	return strings.Contains(out, "gh auth git-credential") ||
		strings.Contains(out, "credential.https://"+host)
}

func (s *Service) git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C")
	hostexec.AsOwner(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// run drives gh in a repository, as that repository's owner.
//
// Every prompt gh knows how to ask is disabled: a web request has nobody to
// answer one, and the failure mode of an unanswered prompt is a subprocess
// that hangs until the context expires rather than an error anybody can read.
func (s *Service) run(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	if s.command != nil {
		return s.command(ctx, dir, stdin, args...)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"GH_PAGER=cat",
		"NO_COLOR=1",
		"CLICOLOR=0",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
	hostexec.AsOwner(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out boundedOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if out.overflow {
		return "GitHub response exceeds 8 MiB; open it on GitHub to read the complete output", fmt.Errorf("GitHub response too large")
	}
	return out.String(), err
}

func ownerName(dir string) string {
	if o, ok := hostexec.OwnerOf(dir); ok {
		return o.Name
	}
	return ""
}
