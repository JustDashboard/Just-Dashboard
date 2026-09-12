package ghx

import "testing"

// The repository name is interpolated into a REST path and reaches gh's argv,
// so anything that is not exactly owner/name has to be refused before it gets
// there rather than filtered afterwards.
func TestValidRepoName(t *testing.T) {
	for _, name := range []string{"Wayy01/Just-Dashboard", "owner/repo.js", "a/b", "o_w-n.er/r_e-p.o"} {
		if !validRepoName(name) {
			t.Fatalf("validRepoName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"", "owner", "owner/", "/repo", "owner/repo/extra", "owner/../etc",
		"owner/repo?per_page=1", "owner/repo#fragment", "own er/repo", "owner/.",
		"owner/repo\nHost: evil", string(make([]byte, 200)),
	} {
		if validRepoName(name) {
			t.Fatalf("validRepoName(%q) = true, want false", name)
		}
	}
}
