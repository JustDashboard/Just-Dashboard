package deploy

import "testing"

// npm ci and Bun's frozen install accept a changed range the locked
// version still satisfies, so the evaluator decides "stale" for those two
// managers. Each row is a behaviour node-semver documents.
func TestNodeRangeSatisfiesFollowsNPMRangeGrammar(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		version, rangeText string
		satisfied, known   bool
	}{
		{"1.3.0", "^1.2.0", true, true},
		{"2.0.0", "^1.2.0", false, true},
		{"0.2.5", "^0.2.3", true, true},
		{"0.3.0", "^0.2.3", false, true},
		{"0.0.4", "^0.0.3", false, true},
		{"0.0.3", "^0.0.3", true, true},
		{"1.2.9", "~1.2.3", true, true},
		{"1.3.0", "~1.2.3", false, true},
		{"1.9.0", "~1", true, true},
		{"1.2.3", "1.2.3", true, true},
		{"1.2.4", "1.2.3", false, true},
		{"1.2.4", "=1.2.4", true, true},
		{"1.5.0", "1.x", true, true},
		{"2.0.0", "1.x", false, true},
		{"1.2.7", "1.2.*", true, true},
		{"3.0.0", "*", true, true},
		{"3.0.0", "", true, true},
		{"19.1.0", "^16.6.0 || ^17.0.0 || ^18.0.0", false, true},
		{"18.3.1", "^16.6.0 || ^17.0.0 || ^18.0.0", true, true},
		{"1.5.0", ">=1.2.3 <2.0.0", true, true},
		{"2.0.0", ">=1.2.3 <2.0.0", false, true},
		{"1.5.0", ">= 1.2.3 < 2", true, true},
		{"2.3.4", "1.2.3 - 2.3.4", true, true},
		{"2.3.5", "1.2.3 - 2.3.4", false, true},
		{"2.9.0", "1.2 - 2", true, true},
		{"1.3.0", ">1.2", true, true},
		{"1.2.9", ">1.2", false, true},
		{"1.2.9", "<=1.2", true, true},
		{"7.0.0", "npm:is-number@^7.0.0", true, true},
		{"1.0.0", "latest", false, false},
		{"1.0.0", "workspace:*", false, false},
		{"1.0.0", "github:owner/repo#v1", false, false},
		{"1.0.0-beta.1", "^1.0.0", false, false},
		{"1.0.0", "^1.0.0-beta.1", false, false},
	} {
		satisfied, known := nodeRangeSatisfies(test.version, test.rangeText)
		if satisfied != test.satisfied || known != test.known {
			t.Errorf("%s in %q = (%v, %v), want (%v, %v)", test.version, test.rangeText, satisfied, known, test.satisfied, test.known)
		}
	}
}
