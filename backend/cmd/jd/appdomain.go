package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"
)

// jd app-domain challenge add|remove is certbot's manual hook for the
// just-dashboard.com address's wildcard (backend/internal/proxysvc/app_domain.go).
//
// certbot runs it as root, from the dashboard's own order or from its renewal
// timer, with the value to publish in CERTBOT_VALIDATION and the name in
// CERTBOT_DOMAIN. It asks the dashboard on this server to publish or withdraw
// the value through the account link, over the same API every other command
// speaks, with the credential the dashboard wrote 0600 into its data
// directory instead of a person's key: the timer runs with nobody signed in.
// The add returns once the zone's own nameservers answer the value, or fails
// after two minutes saying which did not, so a slow update is a clear error
// rather than Let's Encrypt's "no TXT record found" a minute later.

// defaultHookCredentials is where the dashboard writes the credential.
const defaultHookCredentials = "/var/lib/just-dashboard/app-domain-hook.json"

// hookTimeout is above the dashboard's own two-minute wait for the zone.
const hookTimeout = 150 * time.Second

type hookCredential struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func (a *app) appDomainChallenge(args []string) error {
	fs := a.newFlags("app-domain challenge", "app-domain challenge add|remove [--credentials FILE]")
	credentials := fs.String("credentials", defaultHookCredentials, "the hook credential the dashboard wrote")
	positional, err := a.parse(fs, args)
	if err != nil {
		return err
	}
	if err := exactly(positional, 1, "app-domain challenge add|remove [--credentials FILE]"); err != nil {
		return err
	}
	action := positional[0]
	if action != "add" && action != "remove" {
		return usagef("jd app-domain challenge takes add or remove, not %q", action)
	}
	value := strings.TrimSpace(a.env("CERTBOT_VALIDATION"))
	domain := strings.TrimSpace(a.env("CERTBOT_DOMAIN"))
	if value == "" || domain == "" {
		return usagef("CERTBOT_VALIDATION and CERTBOT_DOMAIN are not set: this is certbot's hook, and certbot sets them")
	}
	cred, err := readHookCredential(*credentials)
	if err != nil {
		return err
	}
	c, err := a.newClient(cred.URL, cred.Token)
	if err != nil {
		return err
	}
	c.http.Timeout = hookTimeout
	body := map[string]string{"action": action, "domain": domain, "value": value}
	if _, err := c.sendJSON(http.MethodPost, "/app-domain/hook/challenge", nil, body, nil); err != nil {
		return err
	}
	if action == "add" {
		fmt.Fprintf(a.stderr, "jd: _acme-challenge.%s is published and its nameservers answer it\n", strings.TrimPrefix(domain, "*."))
	} else {
		fmt.Fprintf(a.stderr, "jd: the challenge value for %s is withdrawn\n", strings.TrimPrefix(domain, "*."))
	}
	return nil
}

func readHookCredential(path string) (hookCredential, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return hookCredential{}, fmt.Errorf("%s is missing: the dashboard writes it when the just-dashboard.com address is claimed; issue the certificate again from Settings → just-dashboard.com link", path)
	}
	if err != nil {
		return hookCredential{}, fmt.Errorf("reading the hook's credential: %v (certbot runs this hook as root)", err)
	}
	var cred hookCredential
	if err := json.Unmarshal(raw, &cred); err != nil || cred.URL == "" || cred.Token == "" {
		return hookCredential{}, fmt.Errorf("%s is not the dashboard's hook credential", path)
	}
	return cred, nil
}
