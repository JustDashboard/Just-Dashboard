package deploy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeNodeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readNodeTree(t *testing.T, files map[string]string, dir string) nodeInstallSource {
	t.Helper()
	source, err := readNodeInstallSource(writeNodeTree(t, files), dir, "x64", newNodeReadBudget())
	if err != nil {
		t.Fatal(err)
	}
	return source
}

const lockedManifest = `{"name":"shop","dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"is-number":"7.0.0"}}`

// Each lockfile is read the way its manager's frozen install reads it. The
// shapes are the ones the managers write (pnpm 7, 8 and 10, Yarn 1 and 4,
// npm 10, Bun 1.2); "changed" is stale only where the manager compares range
// text, and for npm and Bun only when the locked version left the range.
func TestNodeLockfileReadingsFollowEachManagersFrozenCheck(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		manifest string
		lockfile string
		content  string
		state    string
		missing  []string
		extra    []string
		changed  []string
	}{
		{
			name: "npm in sync", lockfile: "package-lock.json", state: LockfileInSync,
			content: `{"name":"shop","lockfileVersion":3,"requires":true,"packages":{"":{"name":"shop","dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"is-number":"7.0.0"}},"node_modules/left-pad":{"version":"1.3.0"},"node_modules/is-number":{"version":"7.0.0","dev":true}}}`,
		},
		{
			name: "npm missing a dependency", lockfile: "package-lock.json", state: LockfileStale, missing: []string{"is-number"},
			content: `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0"}},"node_modules/left-pad":{"version":"1.3.0"}}}`,
		},
		{
			name: "npm ci leaves out a removed dependency", lockfile: "package-lock.json", state: LockfileInSync, extra: []string{"zod"},
			content: `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0","zod":"^3"},"devDependencies":{"is-number":"7.0.0"}},"node_modules/left-pad":{"version":"1.3.0"},"node_modules/is-number":{"version":"7.0.0"}}}`,
		},
		{
			name: "npm accepts a changed range the lock still satisfies", lockfile: "package-lock.json", state: LockfileInSync,
			content: `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.2.0"},"devDependencies":{"is-number":"7.0.0"}},"node_modules/left-pad":{"version":"1.3.0"},"node_modules/is-number":{"version":"7.0.0"}}}`,
		},
		{
			name: "npm refuses a range the locked version left", lockfile: "package-lock.json", state: LockfileStale, changed: []string{"left-pad"},
			manifest: `{"dependencies":{"left-pad":"^2.0.0"},"devDependencies":{"is-number":"7.0.0"}}`,
			content:  `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"is-number":"7.0.0"}},"node_modules/left-pad":{"version":"1.3.0"},"node_modules/is-number":{"version":"7.0.0"}}}`,
		},
		{
			name: "npm version 1 lock", lockfile: "package-lock.json", state: LockfileStale, missing: []string{"is-number"},
			content: `{"lockfileVersion":1,"requires":true,"dependencies":{"left-pad":{"version":"1.3.0"}}}`,
		},
		{
			name: "npm-shrinkwrap is npm's lock", lockfile: "npm-shrinkwrap.json", state: LockfileInSync,
			content: `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"is-number":"7.0.0"}},"node_modules/left-pad":{"version":"1.3.0"},"node_modules/is-number":{"version":"7.0.0"}}}`,
		},
		{
			name: "an empty object is not a lock", lockfile: "package-lock.json", state: LockfileUnknown, content: `{}`,
		},
		{
			name: "bun text lock in sync", lockfile: "bun.lock", state: LockfileInSync,
			content: "{\n  \"lockfileVersion\": 1,\n  \"workspaces\": {\n    \"\": {\n      \"name\": \"shop\",\n      \"dependencies\": {\n        \"left-pad\": \"^1.3.0\",\n      },\n      \"devDependencies\": {\n        \"is-number\": \"7.0.0\",\n      },\n    },\n  },\n  \"packages\": {\n    \"is-number\": [\"is-number@7.0.0\", \"\", {}, \"sha512-x\"],\n    \"left-pad\": [\"left-pad@1.3.0\", \"\", {}, \"sha512-y\"],\n  }\n}\n",
		},
		{
			name: "bun judges a changed range by the locked version", lockfile: "bun.lock", state: LockfileStale, changed: []string{"left-pad"},
			manifest: `{"dependencies":{"left-pad":"^1.4.0"},"devDependencies":{"is-number":"7.0.0"}}`,
			content:  `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"left-pad":"^1.3.0",},"devDependencies":{"is-number":"7.0.0",},},},"packages":{"left-pad":["left-pad@1.3.0","",{},"x"],"is-number":["is-number@7.0.0","",{},"x"],}}`,
		},
		{
			name: "bun refuses a lock that keeps a removed dependency", lockfile: "bun.lock", state: LockfileStale, extra: []string{"zod"},
			content: `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"left-pad":"^1.3.0","zod":"^3",},"devDependencies":{"is-number":"7.0.0",},},},"packages":{"left-pad":["left-pad@1.3.0","",{},"x"],"is-number":["is-number@7.0.0","",{},"x"],"zod":["zod@3.24.0","",{},"x"],}}`,
		},
		{
			name: "bun binary lock proves absence only", lockfile: "bun.lockb", state: LockfileStale, missing: []string{"is-number"},
			content: "\x00\x01bun-lockfile-format-v0\x00left-pad\x00^1.3.0\x00",
		},
		{
			name: "bun binary lock with every name", lockfile: "bun.lockb", state: LockfileUnknown,
			content: "\x00bun-lockfile\x00left-pad\x00is-number\x00",
		},
		{
			name: "pnpm 9 importers in sync", lockfile: "pnpm-lock.yaml", state: LockfileInSync,
			content: "lockfileVersion: '9.0'\n\nsettings:\n  autoInstallPeers: true\n\nimporters:\n\n  .:\n    dependencies:\n      left-pad:\n        specifier: ^1.3.0\n        version: 1.3.0\n    devDependencies:\n      is-number:\n        specifier: 7.0.0\n        version: 7.0.0\n\npackages:\n\n  left-pad@1.3.0:\n    resolution: {integrity: sha512-x}\n",
		},
		{
			name: "pnpm compares range text", lockfile: "pnpm-lock.yaml", state: LockfileStale, changed: []string{"left-pad"},
			manifest: `{"dependencies":{"left-pad":"^1.2.0"},"devDependencies":{"is-number":"7.0.0"}}`,
			content:  "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      left-pad:\n        specifier: ^1.3.0\n        version: 1.3.0\n    devDependencies:\n      is-number:\n        specifier: 7.0.0\n        version: 7.0.0\npackages:\n  left-pad@1.3.0: {}\n",
		},
		{
			name: "pnpm refuses a lock that keeps a removed dependency", lockfile: "pnpm-lock.yaml", state: LockfileStale, extra: []string{"zod"},
			content: "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      left-pad:\n        specifier: ^1.3.0\n        version: 1.3.0\n      zod:\n        specifier: ^3\n        version: 3.24.0\n    devDependencies:\n      is-number:\n        specifier: 7.0.0\n        version: 7.0.0\n",
		},
		{
			name: "pnpm 8 single project", lockfile: "pnpm-lock.yaml", state: LockfileInSync,
			content: "lockfileVersion: '6.0'\n\ndependencies:\n  left-pad:\n    specifier: ^1.3.0\n    version: 1.3.0\n\ndevDependencies:\n  is-number:\n    specifier: 7.0.0\n    version: 7.0.0\n\npackages:\n\n  /left-pad@1.3.0:\n    dev: false\n",
		},
		{
			name: "pnpm 7 specifiers", lockfile: "pnpm-lock.yaml", state: LockfileStale, missing: []string{"is-number"},
			content: "lockfileVersion: 5.4\n\nspecifiers:\n  left-pad: ^1.3.0\n\ndependencies:\n  left-pad: 1.3.0\n\npackages:\n\n  /left-pad/1.3.0:\n    dev: false\n",
		},
		{
			name: "yarn classic in sync", lockfile: "yarn.lock", state: LockfileInSync,
			content: "# THIS IS AN AUTOGENERATED FILE. DO NOT EDIT THIS FILE DIRECTLY.\n# yarn lockfile v1\n\n\nis-number@7.0.0:\n  version \"7.0.0\"\n\n\"left-pad@^1.2.0\", left-pad@^1.3.0:\n  version \"1.3.0\"\n",
		},
		{
			name: "yarn classic needs every range", lockfile: "yarn.lock", state: LockfileStale, changed: []string{"left-pad"}, missing: []string{"is-number"},
			manifest: `{"dependencies":{"left-pad":"^1.4.0"},"devDependencies":{"is-number":"7.0.0"}}`,
			content:  "# yarn lockfile v1\n\nleft-pad@^1.3.0:\n  version \"1.3.0\"\n",
		},
		{
			name: "yarn berry workspace block", lockfile: "yarn.lock", state: LockfileInSync,
			content: "__metadata:\n  version: 8\n  cacheKey: 10c0\n\n\"is-number@npm:7.0.0\":\n  version: 7.0.0\n\n\"left-pad@npm:^1.3.0\":\n  version: 1.3.0\n\n\"shop@workspace:.\":\n  version: 0.0.0-use.local\n  resolution: \"shop@workspace:.\"\n  dependencies:\n    is-number: \"npm:7.0.0\"\n    left-pad: \"npm:^1.3.0\"\n  languageName: unknown\n",
		},
		{
			name: "yarn berry drift", lockfile: "yarn.lock", state: LockfileStale, missing: []string{"is-number"},
			content: "__metadata:\n  version: 8\n\n\"shop@workspace:.\":\n  version: 0.0.0-use.local\n  dependencies:\n    left-pad: \"npm:^1.3.0\"\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := test.manifest
			if manifest == "" {
				manifest = lockedManifest
			}
			source := readNodeTree(t, map[string]string{"package.json": manifest, test.lockfile: test.content}, "")
			if len(source.facts.readings) != 1 {
				t.Fatalf("readings = %+v", source.facts.readings)
			}
			reading := source.facts.readings[0]
			if reading.State != test.state || !slices.Equal(reading.Missing, test.missing) ||
				!slices.Equal(reading.Extra, test.extra) || !slices.Equal(reading.Changed, test.changed) {
				t.Fatalf("reading = %+v", reading.DetectedLockfile)
			}
			if reading.Note == "" {
				t.Fatal("a reading needs a sentence for the operator")
			}
		})
	}
}

// package-lock.json carries three further facts the install needs: a lock
// written with --legacy-peer-deps, platform binaries the lock never
// recorded (npm/cli#4828), and registry hosts the build cannot reach.
func TestNPMLockfileReadsPeersPlatformBinariesAndHosts(t *testing.T) {
	t.Parallel()
	lock := `{"lockfileVersion":3,"packages":{
		"":{"dependencies":{"react":"19.1.0","react-helmet-async":"2.0.5","vite":"6.0.0"}},
		"node_modules/react":{"version":"19.1.0"},
		"node_modules/react-helmet-async":{"version":"2.0.5","peerDependencies":{"react":"^16.6.0 || ^17.0.0 || ^18.0.0","react-dom":"^18"},"peerDependenciesMeta":{"react-dom":{"optional":true}}},
		"node_modules/vite":{"version":"6.0.0","resolved":"https://artifactory.corp/npm/vite-6.0.0.tgz","dependencies":{"rollup":"^4.0.0"}},
		"node_modules/rollup":{"version":"4.40.0","optionalDependencies":{"@rollup/rollup-linux-x64-musl":"4.40.0","@rollup/rollup-linux-x64-gnu":"4.40.0","@rollup/rollup-linux-arm64-musl":"4.40.0","@rollup/rollup-darwin-arm64":"4.40.0"}},
		"node_modules/@rollup/rollup-linux-x64-gnu":{"version":"4.40.0","optional":true},
		"node_modules/@rollup/rollup-darwin-arm64":{"version":"4.40.0","optional":true}
	}}`
	source := readNodeTree(t, map[string]string{
		"package.json":      `{"dependencies":{"react":"19.1.0","react-helmet-async":"2.0.5","vite":"6.0.0"}}`,
		"package-lock.json": lock,
	}, "")
	reading := source.facts.readings[0]
	if reading.State != LockfileInSync {
		t.Fatalf("reading = %+v", reading.DetectedLockfile)
	}
	if len(reading.peers) != 1 || reading.peers[0] != (nodePeerConflict{Package: "react-helmet-async", Peer: "react", Range: "^16.6.0 || ^17.0.0 || ^18.0.0", Version: "19.1.0"}) {
		t.Fatalf("peers = %+v", reading.peers)
	}
	if len(reading.optional) != 1 || reading.optional[0] != (nodeOptionalBinary{Name: "@rollup/rollup-linux-x64-musl", Version: "4.40.0", Arch: "x64", Libc: "musl"}) {
		t.Fatalf("optional binaries = %+v", reading.optional)
	}
	if !slices.Equal(reading.hosts, []string{"artifactory.corp"}) {
		t.Fatalf("private hosts = %v", reading.hosts)
	}
}

// A workspace member has no lockfile of its own: its install root is the
// nearest ancestor whose lockfile and workspace globs include it, and the
// lock's record of the member is what is compared.
func TestWorkspaceMemberInstallsFromItsWorkspaceLockfile(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json":             `{"name":"root","private":true,"devDependencies":{"turbo":"2.5.0"}}`,
		"pnpm-workspace.yaml":      "packages:\n  - apps/*\n  - packages/*\n",
		"apps/web/package.json":    `{"name":"web","dependencies":{"left-pad":"1.3.0","ui":"workspace:*"}}`,
		"packages/ui/package.json": `{"name":"ui"}`,
		"pnpm-lock.yaml":           "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    devDependencies:\n      turbo:\n        specifier: 2.5.0\n        version: 2.5.0\n\n  apps/web:\n    dependencies:\n      left-pad:\n        specifier: 1.3.0\n        version: 1.3.0\n      ui:\n        specifier: workspace:*\n        version: link:../../packages/ui\n\n  packages/ui: {}\n\npackages:\n\n  left-pad@1.3.0: {}\n",
	}
	source := readNodeTree(t, files, "apps/web")
	if source.context != "" || source.member() != "apps/web" || !source.facts.workspaceTurbo {
		t.Fatalf("source = context %q member %q turbo %v", source.context, source.member(), source.facts.workspaceTurbo)
	}
	if len(source.facts.readings) != 1 || source.facts.readings[0].State != LockfileInSync {
		t.Fatalf("readings = %+v", source.facts.readings)
	}

	files["apps/web/package.json"] = `{"name":"web","dependencies":{"left-pad":"1.3.0","ui":"workspace:*","zod":"^4"}}`
	stale := readNodeTree(t, files, "apps/web").facts.readings[0]
	if stale.State != LockfileStale || !slices.Equal(stale.Missing, []string{"zod"}) {
		t.Fatalf("member drift = %+v", stale.DetectedLockfile)
	}

	// A settings-only pnpm-workspace.yaml is not a workspace, and a package
	// with a lockfile of its own is never widened.
	lone := readNodeTree(t, map[string]string{
		"package.json":           `{"name":"root"}`,
		"pnpm-lock.yaml":         "lockfileVersion: '9.0'\n",
		"pnpm-workspace.yaml":    "onlyBuiltDependencies:\n  - esbuild\n",
		"tools/cli/package.json": `{"name":"cli"}`,
	}, "tools/cli")
	if lone.context != "tools/cli" || lone.member() != "" {
		t.Fatalf("settings file widened the context: %+v", lone)
	}
}

func TestWorkspaceGlobsFollowPackageManagerMatching(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		patterns []string
		member   string
		want     bool
	}{
		{[]string{"apps/*"}, "apps/web", true},
		{[]string{"./apps/*/"}, "apps/web", true},
		{[]string{"apps/*"}, "apps/web/nested", false},
		{[]string{"packages/**"}, "packages/a/b", true},
		{[]string{"apps/*", "!apps/legacy"}, "apps/legacy", false},
		{[]string{"apps/web"}, "apps/web", true},
		{[]string{"apps/*"}, "services/api", false},
	} {
		if got := nodeWorkspaceIncludes(test.patterns, test.member); got != test.want {
			t.Errorf("%v includes %s = %v, want %v", test.patterns, test.member, got, test.want)
		}
	}
}

// The lockfile budget is its own: a lockfile larger than it is reported as
// not compared, never as stale and never as a truncated detection.
func TestOversizedLockfileIsUnknownRatherThanStale(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, map[string]string{"package.json": lockedManifest})
	large, err := os.Create(filepath.Join(root, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(nodeLockfileMaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	large.Close()
	source, err := readNodeInstallSource(root, "", "x64", newNodeReadBudget())
	if err != nil {
		t.Fatal(err)
	}
	if reading := source.facts.readings[0]; reading.State != LockfileUnknown || !strings.Contains(reading.Note, "was not compared") {
		t.Fatalf("reading = %+v", reading.DetectedLockfile)
	}
	spent := &nodeReadBudget{remaining: 10}
	source, err = readNodeInstallSource(writeNodeTree(t, map[string]string{"package.json": lockedManifest, "yarn.lock": "# yarn lockfile v1\n"}), "", "x64", spent)
	if err == nil {
		t.Fatalf("a spent budget still read package.json: %+v", source)
	}
}

// A workspace's lockfile is parsed once per detection, however many members
// compare against it: four members of a workspace whose lockfile is larger
// than a quarter of the read budget each get a comparison, not "the read
// budget is spent". pnpm's lock is charged only for the head that is read.
func TestWorkspaceLockfileIsReadOncePerDetection(t *testing.T) {
	t.Parallel()
	files := map[string]string{"package.json": `{"name":"root","private":true,"workspaces":["apps/*"]}`}
	entries := []string{`"":{"name":"root","workspaces":["apps/*"]}`}
	for _, member := range []string{"a", "b", "c", "d"} {
		files["apps/"+member+"/package.json"] = `{"name":"` + member + `","scripts":{"start":"node index.js"},"dependencies":{"left-pad":"^1.3.0"}}`
		entries = append(entries, `"apps/`+member+`":{"name":"`+member+`","dependencies":{"left-pad":"^1.3.0"}}`)
	}
	entries = append(entries, `"node_modules/left-pad":{"version":"1.3.0"}`)
	files["package-lock.json"] = `{"lockfileVersion":3,"filler":"` + strings.Repeat("x", 12<<20) + `","packages":{` + strings.Join(entries, ",") + `}}`
	result, err := (Detector{}).DetectPath(t.Context(), writeNodeTree(t, files), SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	members := 0
	for _, candidate := range result.Candidates {
		if !strings.HasPrefix(candidate.Root, "apps/") {
			continue
		}
		members++
		if len(candidate.Lockfiles) != 1 || candidate.Lockfiles[0].State != LockfileInSync {
			t.Fatalf("%s lockfiles = %+v", candidate.Root, candidate.Lockfiles)
		}
	}
	if members != 4 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}

	budget := newNodeReadBudget()
	pnpm := "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    dependencies:\n      left-pad:\n        specifier: ^1.3.0\n        version: 1.3.0\n\npackages:\n\n" +
		strings.Repeat("  filler@1.0.0: {}\n", 1<<19)
	source, err := readNodeInstallSource(writeNodeTree(t, map[string]string{"package.json": `{"dependencies":{"left-pad":"^1.3.0"}}`, "pnpm-lock.yaml": pnpm}), "", "x64", budget)
	if err != nil {
		t.Fatal(err)
	}
	if source.facts.readings[0].State != LockfileInSync || nodeReadBudgetBytes-budget.remaining > 1<<20 {
		t.Fatalf("reading %+v cost %d bytes of a %d-byte lockfile", source.facts.readings[0].DetectedLockfile, nodeReadBudgetBytes-budget.remaining, len(pnpm))
	}
}

// A lockfile's note and names are a repository's text: whatever their
// length or characters, the candidate stays inside the bounds a saved draft
// is validated against.
func TestLockfileReadingFitsTheDetectionBounds(t *testing.T) {
	t.Parallel()
	dependencies := []string{}
	for index := range 20 {
		dependencies = append(dependencies, `"@`+strings.Repeat("s", 100)+`/`+strings.Repeat(string(rune('a'+index)), 110)+`":"^1.0.0"`)
	}
	dependencies = append(dependencies, `"line\nbreak":"^1.0.0"`)
	_, candidate := detectNodeTree(t, map[string]string{
		"package.json":      `{"name":"app","scripts":{"start":"node index.js"},"dependencies":{` + strings.Join(dependencies, ",") + `}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0","` + strings.Repeat("é", 300) + `":"^1"}}}}`,
	})
	if len(candidate.Lockfiles) != 1 || candidate.Lockfiles[0].State != LockfileStale || len(candidate.Lockfiles[0].Note) > 480 {
		t.Fatalf("lockfiles = %+v", candidate.Lockfiles)
	}
	if err := validateDetectedNodeInstall(candidate); err != nil {
		t.Fatalf("detection does not validate: %v", err)
	}
}

// Reads go through an os.Root and never follow a symlink to a file, so a
// checkout cannot point the reader at the host.
func TestNodeFilesRefuseSymlinks(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, map[string]string{"package.json": lockedManifest})
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("lockfileVersion: '9.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "pnpm-lock.yaml")); err != nil {
		t.Fatal(err)
	}
	source, err := readNodeInstallSource(root, "", "x64", newNodeReadBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(source.facts.readings) != 0 {
		t.Fatalf("a symlinked lockfile was read: %+v", source.facts.readings)
	}
}
