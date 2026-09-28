package deploy

import "testing"

func TestNodeDevServerNamesWatchersAndDevelopmentServers(t *testing.T) {
	t.Parallel()
	for body, want := range map[string]string{
		"ng serve":                           "ng serve",
		"ng s --port 4200":                   "ng serve",
		"vite":                               "vite",
		"vite --port 5173":                   "vite",
		"vite dev":                           "vite",
		"vite build":                         "",
		"vite preview --host":                "",
		"astro dev":                          "astro dev",
		"astro":                              "astro dev",
		"astro build":                        "",
		"next dev --turbopack":               "next dev",
		"next start":                         "",
		"npx next dev":                       "next dev",
		"cross-env PORT=3000 nuxt dev":       "nuxt dev",
		"remix vite:dev":                     "remix vite:dev",
		"react-scripts start":                "react-scripts start",
		"gatsby develop":                     "gatsby develop",
		"webpack serve --mode development":   "webpack serve",
		"webpack-dev-server":                 "webpack-dev-server",
		"vue-cli-service serve":              "vue-cli-service serve",
		"parcel index.html":                  "parcel",
		"parcel build index.html":            "",
		"strapi develop":                     "strapi develop",
		"strapi start":                       "",
		"node ace serve --hmr":               "node ace serve",
		"nodemon index.js":                   "nodemon",
		"tsx watch src/index.ts":             "tsx watch",
		"tsx src/index.ts":                   "",
		"node --watch server.js":             "node --watch",
		"node server.js":                     "",
		"ts-node-dev --respawn src/main.ts":  "ts-node-dev",
		"bun run --hot src/index.ts":         "bun --hot",
		"bun src/index.ts":                   "",
		"nest start --watch":                 "nest start --watch",
		"nest start":                         "",
		"npm run build && node dist/main.js": "",
		"eleventy --serve":                   "eleventy --serve",
	} {
		if got := nodeDevServer(body); got != want {
			t.Errorf("nodeDevServer(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestNodeWatchedCommandDropsOnlyTheWatcher(t *testing.T) {
	t.Parallel()
	declared := func(names ...string) func(string) bool {
		return func(name string) bool {
			for _, candidate := range names {
				if candidate == name {
					return true
				}
			}
			return false
		}
	}
	for _, test := range []struct {
		body     string
		declared []string
		want     string
		ok       bool
	}{
		{"nodemon index.js", nil, "node index.js", true},
		{"nodemon --watch src -e js,json server.js --port 3000", nil, "node server.js --port 3000", true},
		{"nodemon src/index.ts", nil, "", false},
		{"nodemon src/index.ts", []string{"tsx"}, "tsx src/index.ts", true},
		{"nodemon --exec ts-node src/index.ts", []string{"ts-node"}, "", false},
		{"NODE_ENV=development nodemon app.js", nil, "NODE_ENV=development node app.js", true},
		{"tsx watch src/index.ts", []string{"tsx"}, "tsx src/index.ts", true},
		{"tsx watch --env-file=.env --ignore ./data src/server.ts", []string{"tsx"}, "tsx --env-file=.env src/server.ts", true},
		{"tsx watch src/index.ts", nil, "", false},
		{"node --watch --env-file=.env server.js", nil, "node --env-file=.env server.js", true},
		{"bun run --hot src/index.ts", nil, "bun src/index.ts", true},
		{"bun --watch run src/index.ts", nil, "bun src/index.ts", true},
		{"bun --hot build", nil, "", false},
		{"ts-node-dev --respawn --transpile-only src/main.ts", []string{"ts-node"}, "ts-node --transpile-only src/main.ts", true},
		{"ts-node-dev src/main.ts", nil, "", false},
		{"next dev", nil, "", false},
		{"nodemon index.js && echo done", nil, "", false},
	} {
		got, ok := nodeWatchedCommand(test.body, declared(test.declared...))
		if got != test.want || ok != test.ok {
			t.Errorf("nodeWatchedCommand(%q) = %q, %v, want %q, %v", test.body, got, ok, test.want, test.ok)
		}
	}
}

func TestNodeStartRunsEntryFollowsScripts(t *testing.T) {
	t.Parallel()
	scripts := map[string]string{"start": "npm run serve", "serve": "cross-env NODE_ENV=production node ./dist/my-app/server/server.mjs"}
	if !nodeStartRunsEntry(scripts, "npm run start", "dist/my-app/server/server.mjs") {
		t.Fatal("entry reached through two scripts")
	}
	if !nodeStartRunsEntry(nil, "npx prisma migrate deploy && node dist/main", "dist/main.js") {
		t.Fatal("entry without its extension")
	}
	if !nodeStartRunsEntry(nil, "node build", "build/index.js") {
		t.Fatal("a directory entry")
	}
	if nodeStartRunsEntry(nil, "node server.js", "dist/main.js") {
		t.Fatal("another file")
	}
	for body, want := range map[string]bool{
		"NODE_ENV=production node dist/index.js": true,
		"tsx server/index.ts":                    true,
		"cross-env NODE_ENV=production node .":   true,
		"bun run src/index.ts":                   true,
		"vite preview":                           false,
		"serve -s dist":                          false,
		"node --version":                         false,
	} {
		if got := nodeRunsFile(body); got != want {
			t.Errorf("nodeRunsFile(%q) = %v", body, got)
		}
	}
}

func TestNodeWorkspaceBuildOrderIsDependenciesFirst(t *testing.T) {
	t.Parallel()
	members := map[string]nodeInstallManifest{
		"web":          {Name: "web", Dependencies: map[string]string{"@repo/ui": "workspace:*", "@repo/db": "*", "react": "^19"}},
		"@repo/ui":     {Name: "@repo/ui", Scripts: map[string]string{"build": "tsup"}, Dependencies: map[string]string{"@repo/tokens": "workspace:^"}},
		"@repo/tokens": {Name: "@repo/tokens", Scripts: map[string]string{"build": "node build.js"}},
		"@repo/db":     {Name: "@repo/db", DevDependencies: map[string]string{"@repo/tokens": "workspace:*"}},
		"@repo/cycle":  {Name: "@repo/cycle", Scripts: map[string]string{"build": "x"}, Dependencies: map[string]string{"web": "workspace:*"}},
	}
	got := nodeWorkspaceBuildOrder(members["web"], members)
	if want := []string{"@repo/tokens", "@repo/ui"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("build order = %v, want %v", got, want)
	}
	if got := nodeWorkspaceBuild("npm", "web", "build", []string{"@repo/ui"}); got != "npm run build --workspace=@repo/ui && npm run build" {
		t.Fatal(got)
	}
	if got := nodeWorkspaceBuild("pnpm", "web", "build", []string{"@repo/ui"}); got != "pnpm --filter web... run build" {
		t.Fatal(got)
	}
}
