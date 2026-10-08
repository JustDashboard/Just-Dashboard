// The optional controlled-vantage executable is rootless and outbound-only.
// It does not start the dashboard, bind a listener or execute host commands.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
)

func main() {
	state := flag.String("state", "probe-state.json", "private local credential/sequence state")
	enroll := flag.Bool("enroll", false, "consume a one-time enrollment token read from stdin")
	origin := flag.String("url", "", "existing HTTPS Caddy control-plane origin")
	id := flag.String("id", "", "one-time enrollment's vantage ID")
	signingKey := flag.String("server-key", "", "operator-approved Ed25519 server public key")
	tlsPin := flag.String("tls-pin", "", "operator-approved TLS SPKI SHA256 pin (hex)")
	once := flag.Bool("once", false, "poll once, perform at most one scoped job, then exit")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *enroll {
		if _, e := os.Lstat(*state); !os.IsNotExist(e) {
			fatal(fmt.Errorf("agent state already exists or is unreadable; enrollment will not replace it"))
		}
		token, e := io.ReadAll(io.LimitReader(os.Stdin, 4097))
		if e != nil || len(token) > 4096 {
			fatal(fmt.Errorf("read one enrollment token from stdin"))
		}
		client, e := netvantage.Transport(*tlsPin)
		if e != nil {
			fatal(e)
		}
		defer client.CloseIdleConnections()
		deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cfg, e := netvantage.Enroll(deadline, client, *origin, *tlsPin, *id, string(token), *signingKey)
		if e != nil {
			fatal(e)
		}
		if e = netvantage.SaveConfig(*state, cfg); e != nil {
			fatal(e)
		}
		fmt.Fprintln(os.Stdout, "Enrolled the controlled vantage. Approved targets and private identity are saved in the private state file.")
		return
	}
	cfg, e := netvantage.LoadConfig(*state)
	if e != nil {
		fatal(e)
	}
	client, e := netvantage.Transport(cfg.TLSPin)
	if e != nil {
		fatal(e)
	}
	defer client.CloseIdleConnections()
	poller := &netvantage.Poller{Config: cfg, Path: *state, Client: client}
	if *once {
		if e = poller.Once(ctx); e != nil {
			fatal(e)
		}
		return
	}
	if e = poller.Run(ctx, func(e error) { fmt.Fprintln(os.Stderr, "controlled probe:", e) }); e != nil && ctx.Err() == nil {
		fatal(e)
	}
}
func fatal(e error) { fmt.Fprintln(os.Stderr, "controlled probe:", e); os.Exit(1) }
