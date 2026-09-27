package gitx

import "testing"

func TestSummaryPreservesDivergenceGoneAndDetached(t *testing.T) {
	dir, git := tempRepo(t)
	git("branch", "upstream")
	git("branch", "--set-upstream-to=upstream", "main")
	git("commit", "--allow-empty", "-m", "local change")
	git("checkout", "upstream")
	git("commit", "--allow-empty", "-m", "upstream change")
	git("checkout", "main")
	s := New([]string{dir})
	r, err := s.Summary(t.Context(), dir)
	if err != nil || r.Ahead != 1 || r.Behind != 1 || r.Upstream != "upstream" || r.Gone || r.Empty || r.Detached || r.Subject != "local change" || r.Head == "" {
		t.Fatalf("diverged summary: %+v, %v", r, err)
	}
	git("branch", "-D", "upstream")
	r, err = s.Summary(t.Context(), dir)
	if err != nil || !r.Gone || r.Upstream != "upstream" || r.Ahead != 0 || r.Behind != 0 {
		t.Fatalf("gone summary: %+v, %v", r, err)
	}
	git("checkout", "--detach")
	r, err = s.Summary(t.Context(), dir)
	if err != nil || !r.Detached || r.Branch != "detached at "+r.Head || r.Subject != "local change" || r.Author != "t" || r.CommitAt.IsZero() {
		t.Fatalf("detached summary: %+v, %v", r, err)
	}
}
