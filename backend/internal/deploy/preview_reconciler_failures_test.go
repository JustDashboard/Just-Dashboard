package deploy

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// The loop ran `_ = r.Reconcile(ctx)`: a reconciler that could not read the
// open previews closed none of them and said nothing.
func TestPreviewReconcilerSaysWhenItCannotReadItsPreviews(t *testing.T) {
	f, _, _, _, reconciler := reconcilerFixture(t)
	out := &lockedLog{}
	reconciler.log = slog.New(slog.NewTextHandler(out, nil))
	if _, err := f.store.DB.Exec(`ALTER TABLE deploy_preview_refs RENAME TO deploy_preview_refs_gone`); err != nil {
		t.Fatal(err)
	}
	reconciler.Start(context.Background())
	defer reconciler.Stop()
	out.waitFor(t, "background task failed", 5*time.Second)
	if logged := out.String(); !strings.Contains(logged, `task="preview reconciler"`) || !strings.Contains(logged, "deploy_preview_refs") {
		t.Fatalf("log = %s, want the reconciler named with the store's reason", logged)
	}
}
