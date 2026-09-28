package store

import "testing"

// The ports history holds one open stretch per socket. A second would double
// every event after it, so the database refuses it, while closed stretches
// of the same socket pile up as its history.
func TestListenerObservationsKeepOneOpenStretchPerSocket(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	insert := func(goneAt any) error {
		_, err := st.DB.Exec(`INSERT INTO listener_observations
			(protocol, family, address, port, process, first_seen, gone_at) VALUES ('tcp', 'ipv4', '0.0.0.0', 22, 'sshd', 100, ?)`, goneAt)
		return err
	}
	for _, goneAt := range []any{200, 300, nil} {
		if err := insert(goneAt); err != nil {
			t.Fatalf("stretch ending %v: %v", goneAt, err)
		}
	}
	if err := insert(nil); err == nil {
		t.Fatal("a second open stretch for 0.0.0.0:22 was accepted")
	}
	if _, err := st.DB.Exec(`INSERT INTO listener_observations
		(protocol, family, address, port, first_seen) VALUES ('tcp', 'ipv6', '::', 22, 100)`); err != nil {
		t.Fatalf("the other family's socket was refused: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO listener_history (id, started_at, last_sample) VALUES (2, 1, 1)`); err == nil {
		t.Fatal("listener_history took a second row")
	}
}
