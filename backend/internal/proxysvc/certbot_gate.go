package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// One certbot at a time, whoever asks.
//
// certbot holds a lock on its configuration directory for the length of a
// run, and a second certbot fails on it at once: "Another instance of Certbot
// is already running". The Certificates page kept its own jobs apart with the
// job manager's exclusive family, but a deployment run's order — placed by
// the run, not by a job — knew nothing of that family, so a deployment that
// reached its certificate step while an operator was renewing on the page
// failed in certbot's words, and a renewal started while a run was ordering
// failed the same way. The gate below is the one turn every certbot the
// dashboard runs takes: the page's jobs, a run's HTTP-01 and DNS-01 orders,
// the page's renewals. Whoever finds it held waits for the holder, bounded
// by the caller's own context, and says whom it is waiting for. certbot's own
// lock still arbitrates against what the dashboard does not run — the
// renewal timer first among them.

type certbotGate struct {
	// turn holds one token while certbot runs; taking it is taking the turn.
	turn chan struct{}
	mu   sync.Mutex
	// holder names who has the turn, for whoever has to wait.
	holder string
}

// certbotTurn is the process's one gate: certbot's lock is one per
// configuration directory, and the dashboard has one of those.
var certbotTurn = &certbotGate{turn: make(chan struct{}, 1)}

// ErrCertbotBusy is a wait for certbot that ran out of time, with whom it was
// waiting for (CertbotBusyError).
var ErrCertbotBusy = errors.New("certbot is busy")

// CertbotBusyError is a turn that did not come in time: the caller's context
// ended while Holder still had it.
type CertbotBusyError struct {
	// Holder is who had certbot, as it described itself to HoldCertbot.
	Holder string
	// Waited is how long the caller waited before giving up.
	Waited time.Duration
	cause  error
}

func (e *CertbotBusyError) Error() string {
	return fmt.Sprintf("waited %s for certbot, which was busy with %s, and ran out of time", e.Waited.Round(time.Second), e.Holder)
}

func (e *CertbotBusyError) Is(target error) bool { return target == ErrCertbotBusy }
func (e *CertbotBusyError) Unwrap() error        { return e.cause }

// HoldCertbot takes the turn to run certbot for who — a description that
// reads in another caller's transcript, such as "Renewing app.example.com
// (started by alice from the Certificates page)". When another caller has it,
// waiting is told whom, once, and the call waits until the turn is free or
// ctx ends, which is a CertbotBusyError naming the holder. The release must
// be called when certbot is done, however it ended.
func HoldCertbot(ctx context.Context, who string, waiting func(holder string)) (release func(), err error) {
	return certbotTurn.hold(ctx, who, waiting)
}

func (g *certbotGate) hold(ctx context.Context, who string, waiting func(holder string)) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case g.turn <- struct{}{}:
	default:
		holder := g.current()
		if waiting != nil {
			waiting(holder)
		}
		started := time.Now()
		select {
		case g.turn <- struct{}{}:
		case <-ctx.Done():
			// The holder may have changed hands while this waited; the one
			// named is the one holding it now, which is what the reader
			// would find on the page.
			if now := g.current(); now != "" {
				holder = now
			}
			return nil, &CertbotBusyError{Holder: holder, Waited: time.Since(started), cause: ctx.Err()}
		}
	}
	g.set(who)
	var once sync.Once
	return func() {
		once.Do(func() {
			g.set("")
			<-g.turn
		})
	}, nil
}

func (g *certbotGate) current() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.holder
}

func (g *certbotGate) set(holder string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.holder = holder
}
