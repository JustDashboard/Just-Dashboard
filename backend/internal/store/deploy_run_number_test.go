package store_test

import (
	"context"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestDeploymentRunNumbersMigrateAndSurviveReopen(t *testing.T) {
	dir := install066Fixture(t)
	for pass := 0; pass < 2; pass++ {
		st, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		legacy := deploy.NewStore(st, nil, nil)
		engine := deploy.NewOrchestrationStore(st)
		for id, number := range map[int64]int64{1: 1, 2: 2, 3: 3, 4: 1} {
			old, err := legacy.Run(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			run, err := engine.Run(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if old.ID != id || run.ID != id || old.RunNumber != number || run.RunNumber != number {
				t.Fatalf("pass %d, run %d: legacy=%+v engine=%+v, want number %d", pass, id, old, run, number)
			}
		}
		id, err := legacy.StartRun(context.Background(), 2, "manual", "operator", "")
		if err != nil {
			t.Fatal(err)
		}
		run, err := engine.Run(context.Background(), id)
		if err != nil || run.RunNumber != int64(pass+2) {
			t.Fatalf("new run after upgrade: %+v, %v", run, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
