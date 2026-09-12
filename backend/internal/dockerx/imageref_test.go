package dockerx

import "testing"

// The regression this file exists for: an image id was normalised as though it
// were a repository, so `sha256:ab…` reached the daemon as the repository
// `sha256` with the tag `ab…`, and every attempt to inspect an image by id —
// which is the only handle a dangling image has — failed with "invalid
// reference format: repository name (library/sha256…) must be lowercase".

const testID = "sha256:ae3f6ee6a602e788634d3b8729d6165c72551176dd2fdf1b8e6e6f2c11d9ae3e"

func TestClassifyImageRef(t *testing.T) {
	cases := []struct {
		in   string
		want ImageRefKind
	}{
		{"nginx", RefTag},
		{"nginx:1.27", RefTag},
		{"library/nginx:alpine", RefTag},
		{"registry.example.com:5000/team/app", RefTag},
		{"registry.example.com:5000/team/app:v2", RefTag},
		{"nginx@sha256:abcdef0123456789", RefDigest},
		{testID, RefID},
		{"<none>:<none>", RefDangling},
		{"", RefUnknown},
		// A repository that happens to be called sha256 with a non-hex tag is
		// a name, not an id.
		{"sha256:latest", RefTag},
	}
	for _, c := range cases {
		if got := ClassifyImageRef(c.in); got != c.want {
			t.Errorf("ClassifyImageRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeImageRefLeavesExactReferencesAlone(t *testing.T) {
	cases := map[string]string{
		"nginx":                          "nginx:latest",
		"nginx:1.27":                     "nginx:1.27",
		"registry.example.com:5000/app":  "registry.example.com:5000/app:latest",
		"nginx@sha256:abcdef0123456789":  "nginx@sha256:abcdef0123456789",
		testID:                           testID,
		"<none>:<none>":                  "<none>:<none>",
		"":                               "",
		" ghcr.io/owner/app:v1 ":         "ghcr.io/owner/app:v1",
		"registry.example.com:5000/a:v2": "registry.example.com:5000/a:v2",
	}
	for in, want := range cases {
		if got := NormalizeImageRef(in); got != want {
			t.Errorf("NormalizeImageRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsImageIDRequiresHex(t *testing.T) {
	if !IsImageID(testID) {
		t.Fatal("a full sha256 id should be recognised")
	}
	if IsImageID("sha256:latest") {
		t.Error("a non-hex tag on a repository called sha256 is not an id")
	}
	if IsImageID("sha256:abc") {
		t.Error("a digest too short to be an id should be rejected")
	}
	if IsImageID("nginx:1.27") {
		t.Error("a plain tag is not an id")
	}
}

func TestPullableAndCheckable(t *testing.T) {
	if !Pullable("nginx:1.27") || !Checkable("nginx:1.27") {
		t.Error("a tag can be pulled and checked")
	}
	if !Pullable("nginx@sha256:abcdef0123456789") {
		t.Error("a digest reference can be pulled")
	}
	if Checkable("nginx@sha256:abcdef0123456789") {
		t.Error("a digest reference is pinned, so there is nothing to check")
	}
	if Pullable(testID) || Checkable(testID) {
		t.Error("an image id names nothing a registry could serve")
	}
	if Pullable("<none>:<none>") {
		t.Error("a dangling image has no name to pull")
	}
}

func TestMovingTag(t *testing.T) {
	for _, ref := range []string{"nginx", "nginx:latest", "ghcr.io/owner/app:latest"} {
		if !MovingTag(ref) {
			t.Errorf("%q is a moving tag", ref)
		}
	}
	for _, ref := range []string{"nginx:1.27", "nginx@sha256:abcdef0123456789", testID} {
		if MovingTag(ref) {
			t.Errorf("%q does not move", ref)
		}
	}
}

func TestSplitImageRef(t *testing.T) {
	cases := []struct{ in, repo, tag string }{
		{"nginx:1.27", "nginx", "1.27"},
		{"nginx", "nginx", ""},
		{"registry.example.com:5000/team/app:v2", "registry.example.com:5000/team/app", "v2"},
		{"registry.example.com:5000/team/app", "registry.example.com:5000/team/app", ""},
		{"nginx@sha256:abcdef", "nginx", "sha256:abcdef"},
		{testID, "", testID},
	}
	for _, c := range cases {
		repo, tag := SplitImageRef(c.in)
		if repo != c.repo || tag != c.tag {
			t.Errorf("SplitImageRef(%q) = (%q, %q), want (%q, %q)", c.in, repo, tag, c.repo, c.tag)
		}
	}
}

func TestPreferredRefPrefersARealName(t *testing.T) {
	if got := PreferredRef(testID, []string{"nginx:1.27", "nginx:latest"}, nil); got != "nginx:1.27" {
		t.Errorf("a tagged image leads with its first tag, got %q", got)
	}
	if got := PreferredRef(testID, nil, []string{"nginx@sha256:abc"}); got != "nginx@sha256:abc" {
		t.Errorf("an untagged image falls back to its digest, got %q", got)
	}
	if got := PreferredRef(testID, []string{"<none>:<none>"}, nil); got != testID {
		t.Errorf("a dangling image is known only by its id, got %q", got)
	}
}

func TestDescribeReferenceSeparatesLocalFromDangling(t *testing.T) {
	built := &ImageDetail{ID: testID, RepoTags: []string{"my-app:1"}}
	built.describeReference(RefTag)
	if built.Dangling {
		t.Error("a tagged image is not dangling")
	}
	if !built.LocalBuild {
		t.Error("no repo digest means it was built here")
	}
	if built.Checkable {
		t.Error("an image with no registry copy has nothing to check against")
	}
	if !built.Pullable {
		t.Error("a tag can still be pulled even if this copy was built locally")
	}

	dangling := &ImageDetail{ID: testID, RepoTags: []string{"<none>:<none>"}}
	dangling.describeReference(RefID)
	if !dangling.Dangling || dangling.Pullable || dangling.Kind != RefDangling {
		t.Errorf("dangling image described wrong: %+v", dangling)
	}
	if dangling.Ref != testID {
		t.Errorf("a dangling image leads with its id, got %q", dangling.Ref)
	}

	pulled := &ImageDetail{
		ID:          testID,
		RepoTags:    []string{"nginx:latest"},
		RepoDigests: []string{"nginx@sha256:abc"},
	}
	pulled.describeReference(RefTag)
	if !pulled.Checkable || !pulled.MovingTag {
		t.Errorf("a pulled moving tag is checkable and moving: %+v", pulled)
	}
}
