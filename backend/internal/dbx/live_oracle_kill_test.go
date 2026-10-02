package dbx

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// What Oracle says when it has ended a session that was busy is an error to
// its driver and a kill that worked to everybody else.
func TestOracleKillReadsSessionMarkedForKillAsDone(t *testing.T) {
	if err := oracleKillOutcome(errors.New("ORA-00031: session marked for kill\n")); err != nil {
		t.Errorf("session marked for kill = %v, want the kill to have worked", err)
	}
	refused := errors.New("ORA-00030: User session ID does not exist.")
	if err := oracleKillOutcome(refused); !errors.Is(err, refused) {
		t.Errorf("a session that does not exist = %v, want Oracle's own refusal", err)
	}
	if err := oracleKillOutcome(nil); err != nil {
		t.Errorf("a kill that answered nothing = %v", err)
	}
}

// A session in the middle of a statement, ended from another: the kill answers
// as one that worked, and the statement stops. An administrator's connection
// is needed for it, so the test is skipped without one.
func TestLiveOracleKillOfABusySessionSucceeds(t *testing.T) {
	const env = "JD_TEST_ORACLE_ADMIN_DSN"
	if os.Getenv(env) == "" {
		t.Skipf("set %s to an account that may end sessions", env)
	}
	db := liveSQL(t, DriverOracle, env, "")
	ctx := context.Background()
	// One session, held: asked of the pool, the statement of a session that
	// was ended is sent again on a new one, and never stops.
	victim, err := liveSQL(t, DriverOracle, env, "").Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	done := make(chan error, 1)
	go func() {
		// Busy on the processor rather than asleep: a sleeping session is
		// ended on the spot and never marked.
		var n int64
		done <- victim.QueryRowContext(ctx,
			`SELECT /* jd_ora_kill_probe */ COUNT(*) FROM all_objects a, all_objects b, all_objects c`).Scan(&n)
	}()
	var target string
	for i := 0; i < 200 && target == ""; i++ {
		list, err := ListActivity(ctx, db, DriverOracle)
		if err != nil {
			t.Fatalf("ListActivity: %v", err)
		}
		for _, a := range list {
			if !a.Self && strings.Contains(a.Query, "jd_ora_kill_probe") {
				target = a.PID
			}
		}
		if target == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if target == "" {
		t.Fatal("the probe never appeared in the activity list")
	}
	if err := KillQuery(ctx, db, DriverOracle, target); err != nil {
		t.Fatalf("KillQuery(%s) = %v, want a kill that worked", target, err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("the probe finished normally; the kill did nothing")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the probe is still running 30s after being killed")
	}
}
