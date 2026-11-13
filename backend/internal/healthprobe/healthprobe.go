// Package healthprobe asks the local server whether it is serving: the whole
// of the container's health check.
//
// It imports the standard library and nothing else, and must stay that way.
// The check runs every thirty seconds for the life of the container. While it
// was a mode of the server binary, each run first initialised every package
// the server links: about 340 of them, 150 ms and 57 MB, most of it compiling
// regular expressions the probe never used. cmd/healthcheck is this package
// and a main function.
package healthprobe

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Path is the liveness route. It answers a fixed body for as long as the
// process serves and knows nothing about the store, on purpose: this check is
// what `compose up --wait` and the updater's rollback read.
const Path = "/healthz"

// Run probes the server the environment describes. getenv is os.Getenv
// outside tests.
//
// It targets the configured bind address so a health check cannot pass
// against some other process that happens to be listening.
func Run(getenv func(string) string) error {
	target, client := Target(getenv)
	resp, err := client.Get(target)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", resp.Status)
	}
	return nil
}

// Target is the URL Run asks and the client it asks with.
func Target(getenv func(string) string) (string, *http.Client) {
	addr := setting(getenv, "JD_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if port, ok := strings.CutPrefix(addr, "0.0.0.0:"); ok {
		addr = "127.0.0.1:" + port
	}
	// Agent mode serves TLS on this same address, so probing it with plain
	// HTTP got a handshake error every time: the container went unhealthy
	// after three attempts and stayed there while the agent was serving
	// perfectly. The certificate is self-signed by design and the peer is
	// loopback, so verifying it would be theatre.
	//
	// The environment variable is the only signal available here: the probe
	// is a separate process from the server, so a server started with -agent
	// rather than JD_AGENT_MODE=true is invisible to it. The compose file sets
	// the variable, and the image's health check inherits it.
	scheme := "http://"
	client := &http.Client{Timeout: 4 * time.Second}
	if agent, err := strconv.ParseBool(setting(getenv, "JD_AGENT_MODE")); err == nil && agent {
		scheme = "https://"
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	return scheme + addr + Path, client
}

// setting reads a JD_* variable, falling back to the VPSD_* name an install
// from before the rename still sets. It is config.Env, restated: importing
// internal/config would link the store, the updater and everything under them
// into a program whose point is to link none of it.
func setting(getenv func(string) string, key string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	if legacy, ok := strings.CutPrefix(key, "JD_"); ok {
		return strings.TrimSpace(getenv("VPSD_" + legacy))
	}
	return ""
}

// Main is the health check as a program: silent and 0 when the server
// answers, a line on stderr and 1 when it does not.
func Main() {
	if err := Run(os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		os.Exit(1)
	}
}
