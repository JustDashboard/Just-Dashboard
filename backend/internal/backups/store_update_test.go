package backups

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

func jobUpdateFixture(t *testing.T) (*Store, *Job) {
	t.Helper()
	db, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("9d", 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, sealer)
	job, err := s.Create(t.Context(), &Job{Name: "nightly", Sources: []string{t.TempDir()},
		TargetKind: TargetS3, Target: TargetConfig{Bucket: "fixture-bucket", Region: "fixture-region"},
		Retention: 2, Schedule: "0 3 * * *", Enabled: true},
		&TargetSecrets{AccessKeyID: "fixture-old-key", SecretAccessKey: "fixture-old-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return s, job
}

func assertJobKeysUnchanged(t *testing.T, s *Store, id int64) {
	t.Helper()
	keys, err := s.Secrets(t.Context(), id)
	if err != nil || keys.AccessKeyID != "fixture-old-key" || keys.SecretAccessKey != "fixture-old-secret" {
		t.Fatal("failed save replaced the original credentials", err)
	}
}

func TestJobUpdatePublishesCredentialsAndDefinitionAtomically(t *testing.T) {
	s, job := jobUpdateFixture(t)
	if _, err := s.st.DB.Exec(`CREATE TRIGGER reject_definition BEFORE UPDATE OF name ON backup_jobs
 WHEN NEW.name = 'rejected' BEGIN SELECT RAISE(ABORT, 'fixture definition failure'); END`); err != nil {
		t.Fatal(err)
	}
	job.Name = "rejected"
	job.Target.Bucket = "new-fixture-bucket"
	job.Retention = 10
	newKeys := &TargetSecrets{AccessKeyID: "fixture-new-key", SecretAccessKey: "fixture-new-secret"}
	if _, err := s.Update(t.Context(), job.ID, job, newKeys); err == nil {
		t.Fatal("fault-injected definition update succeeded")
	}
	saved, err := s.Get(t.Context(), job.ID)
	if err != nil || saved.Name != "nightly" || saved.Target.Bucket != "fixture-bucket" || saved.Retention != 2 || saved.Revision != 1 {
		t.Fatalf("failed publication changed the definition: %+v %v", saved, err)
	}
	assertJobKeysUnchanged(t, s, job.ID)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Update(canceled, job.ID, job, newKeys); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication returned %v", err)
	}
	assertJobKeysUnchanged(t, s, job.ID)
	job.Name = "accepted"
	saved, err = s.Update(t.Context(), job.ID, job, newKeys)
	keys, keyErr := s.Secrets(t.Context(), job.ID)
	if err != nil || keyErr != nil || saved.Revision != 2 || saved.Target.Bucket != "new-fixture-bucket" || keys.AccessKeyID != newKeys.AccessKeyID {
		t.Fatal("successful save did not publish both definition and credentials", err, keyErr)
	}
}

func TestJobUpdateRejectsConcurrentDefinitionsAndKeys(t *testing.T) {
	s, job := jobUpdateFixture(t)
	a, err := s.Get(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Get(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Retention = 10
	if _, err := s.Update(t.Context(), job.ID, a, nil); err != nil {
		t.Fatal(err)
	}
	b.Name = "stale-name"
	if _, err := s.Update(t.Context(), job.ID, b, &TargetSecrets{AccessKeyID: "stale-key"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save returned %v", err)
	}
	saved, _ := s.Get(t.Context(), job.ID)
	if saved.Name != "nightly" || saved.Retention != 10 || saved.Revision != 2 {
		t.Fatalf("stale save undid current settings: %+v", saved)
	}
	assertJobKeysUnchanged(t, s, job.ID)
	if _, err := s.SetEnabled(t.Context(), job.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(t.Context(), job.ID, saved, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("old form undid schedule pause: %v", err)
	}
	saved, _ = s.Get(t.Context(), job.ID)
	if saved.Enabled || saved.Revision != 3 {
		t.Fatalf("stale form resumed schedule: %+v", saved)
	}
}

func TestJobUpdateOnlyOneSimultaneousEditorPublishes(t *testing.T) {
	s, job := jobUpdateFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, retention := range []int{10, 20} {
		go func() {
			copy := *job
			copy.Sources = slices.Clone(job.Sources)
			copy.Retention = retention
			<-start
			_, err := s.Update(t.Context(), job.ID, &copy, nil)
			results <- err
		}()
	}
	close(start)
	success, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("simultaneous publishers: %d successful, %d conflicts", success, conflicts)
	}
}

func TestAddingDatabaseDumpRetriesNewDefinitionAndPreservesUnrelatedSettings(t *testing.T) {
	s, job := jobUpdateFixture(t)
	var once sync.Once
	s.WithDatabaseValidator(func(_ context.Context, id int64) (DatabaseDescription, error) {
		once.Do(func() {
			current, err := s.Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			current.Retention = 10
			current.Schedule = "0 6 * * *"
			current.Enabled = false
			current.Target.Bucket = "edited-fixture-bucket"
			if _, err := s.Update(t.Context(), job.ID, current, nil); err != nil {
				t.Fatal(err)
			}
		})
		return DatabaseDescription{ID: id}, nil
	})
	saved, err := s.AddDatabaseDump(t.Context(), job.ID, 9)
	if err != nil || saved.Retention != 10 || saved.Schedule != "0 6 * * *" || saved.Enabled || saved.Target.Bucket != "edited-fixture-bucket" || !slices.Equal(saved.DatabaseDumps, []int64{9}) || saved.Revision != 3 {
		t.Fatalf("add dump replayed stale settings: %+v %v", saved, err)
	}
	assertJobKeysUnchanged(t, s, job.ID)
	again, err := s.AddDatabaseDump(t.Context(), job.ID, 9)
	if err != nil || again.Revision != saved.Revision || len(again.DatabaseDumps) != 1 {
		t.Fatalf("duplicate addition was not idempotent: %+v %v", again, err)
	}
	// A deleted connection must still fail even when its ID is already listed.
	s.WithDatabaseValidator(func(context.Context, int64) (DatabaseDescription, error) {
		return DatabaseDescription{}, errors.New("connection deleted")
	})
	if _, err := s.AddDatabaseDump(t.Context(), job.ID, 9); err == nil {
		t.Fatal("added a deleted connection")
	}
	if _, err := s.AddDatabaseDump(t.Context(), 999, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted job returned %v", err)
	}
}

func TestSimultaneousDatabaseDumpAdditionsKeepBoth(t *testing.T) {
	s, job := jobUpdateFixture(t)
	// Both helpers read revision 1 before validation releases them.
	var first sync.WaitGroup
	first.Add(2)
	var mu sync.Mutex
	calls := 0
	s.WithDatabaseValidator(func(_ context.Context, id int64) (DatabaseDescription, error) {
		mu.Lock()
		calls++
		wait := calls <= 2
		mu.Unlock()
		if wait {
			first.Done()
			first.Wait()
		}
		return DatabaseDescription{ID: id}, nil
	})
	results := make(chan error, 2)
	for _, id := range []int64{9, 10} {
		go func() {
			_, err := s.AddDatabaseDump(t.Context(), job.ID, id)
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.Get(t.Context(), job.ID)
	if err != nil || !slices.Equal(saved.DatabaseDumps, []int64{9, 10}) || saved.Retention != 2 || saved.Revision != 3 {
		t.Fatalf("simultaneous additions lost a dump: %+v %v", saved, err)
	}
	assertJobKeysUnchanged(t, s, job.ID)
	// Filesystem settings were never sent with either addition.
	if saved.Sources[0] != filepath.Clean(job.Sources[0]) {
		t.Fatal("dump addition changed source paths")
	}
}

func TestDatabaseDumpAdditionRefusesMalformedStoredList(t *testing.T) {
	s, job := jobUpdateFixture(t)
	for _, raw := range []string{"not-json", `{"connection":9}`, `[9,"invalid"]`, ""} {
		if _, err := s.st.DB.Exec(`UPDATE backup_jobs SET database_dumps = ? WHERE id = ?`, raw, job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddDatabaseDump(t.Context(), job.ID, 10); err == nil {
			t.Fatalf("replaced unreadable dump list %q", raw)
		}
		var saved string
		var revision int64
		if err := s.st.DB.QueryRow(`SELECT database_dumps, revision FROM backup_jobs WHERE id = ?`, job.ID).Scan(&saved, &revision); err != nil || saved != raw || revision != 1 {
			t.Fatalf("corrupted list changed after rejected addition: %q revision %d, %v", saved, revision, err)
		}
		assertJobKeysUnchanged(t, s, job.ID)
	}
}

func TestDatabaseDumpAdditionBoundsConcurrentPublicationRetries(t *testing.T) {
	s, job := jobUpdateFixture(t)
	publications := 0
	s.WithDatabaseValidator(func(_ context.Context, id int64) (DatabaseDescription, error) {
		publications++
		if _, err := s.SetEnabled(t.Context(), job.ID, false); err != nil {
			t.Fatal(err)
		}
		return DatabaseDescription{ID: id}, nil
	})
	if _, err := s.AddDatabaseDump(t.Context(), job.ID, 9); !errors.Is(err, ErrConflict) {
		t.Fatalf("unbounded competing changes did not conflict: %v", err)
	}
	saved, err := s.Get(t.Context(), job.ID)
	if err != nil || publications != 5 || len(saved.DatabaseDumps) != 0 || saved.Retention != 2 || saved.Enabled {
		t.Fatalf("exhausted helper replaced current settings: %+v, publications %d, %v", saved, publications, err)
	}
	assertJobKeysUnchanged(t, s, job.ID)
}

func TestDatabaseDumpAdditionHandlesJobDeletedDuringValidation(t *testing.T) {
	s, job := jobUpdateFixture(t)
	s.WithDatabaseValidator(func(_ context.Context, id int64) (DatabaseDescription, error) {
		if err := s.Delete(t.Context(), job.ID); err != nil {
			t.Fatal(err)
		}
		return DatabaseDescription{ID: id}, nil
	})
	if _, err := s.AddDatabaseDump(t.Context(), job.ID, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("job deleted during validation returned %v", err)
	}
}
