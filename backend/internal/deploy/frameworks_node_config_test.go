package deploy

import "testing"

func TestFrameworkConfigurationLiteralsAreReadAsText(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"export default { output: 'export' }":                          "export",
		"const config = {\n  output: \"standalone\",\n}":               "standalone",
		"module.exports = {\n  // output: 'export',\n}":                "",
		"module.exports = { /* output: 'export' */ reactStrictMode }":  "",
		"module.exports = { output: isStatic ? 'export' : undefined }": "unknown",
		"module.exports = { experimental: { outputFileTracingRoot } }": "",
		"const url = 'https://example.test/output: x'":                 "",
	} {
		if got := nextOutputMode(string(jsBlankComments([]byte(text)))); got != want {
			t.Errorf("nextOutputMode(%q) = %q, want %q", text, got, want)
		}
	}
	for text, want := range map[string]string{
		"import adapter from '@sveltejs/adapter-auto';\nexport default { kit: { adapter: adapter() } }":                                                     "@sveltejs/adapter-auto",
		"import node from '@sveltejs/adapter-node';\nimport vercel from '@sveltejs/adapter-vercel';\nexport default { kit: { adapter: node() } }":           "@sveltejs/adapter-node",
		"import node from '@sveltejs/adapter-node';\nimport staticAdapter from '@sveltejs/adapter-static';\nconst a = process.env.X ? node : staticAdapter": "",
		"export default { kit: {} }": "",
	} {
		if got, _ := svelteAdapter(string(jsBlankComments([]byte(text)))); got != want {
			t.Errorf("svelteAdapter(%q) = %q, want %q", text, got, want)
		}
	}
	if _, imported := svelteAdapter("import node from '@sveltejs/adapter-node';\nimport vercel from '@sveltejs/adapter-vercel';\nexport default { kit: { adapter: process.env.VERCEL ? vercel() : node() } }"); len(imported) != 2 {
		t.Fatalf("adapters chosen by an expression = %q", imported)
	}
	if got := literalRelativePath(nextDistDirRE, "distDir: '../outside'"); got != "" {
		t.Fatalf("escaping distDir = %q", got)
	}
	if got := string(jsBlankComments([]byte("const a = `// kept`; // dropped\n/* dropped */ const b = '/* kept */'"))); got != "const a = `// kept`;           \n              const b = '/* kept */'" {
		t.Fatalf("jsBlankComments = %q", got)
	}
	if got := svelteKitAdapterNode("^2.0.0"); got != "3.0.3" {
		t.Fatalf("kit ^2.0.0 = %q", got)
	}
	if got := svelteKitAdapterNode("^2.21.0"); got != "5.5.7" {
		t.Fatalf("kit ^2.21.0 = %q", got)
	}
	if got := svelteKitAdapterNode("^1.30.0"); got != "1.3.1" {
		t.Fatalf("kit ^1.30.0 = %q", got)
	}
	if got := svelteKitAdapterNode("next"); got != "" {
		t.Fatalf("kit next = %q", got)
	}
}

// The framework reads have a budget of their own: a monorepo whose
// configuration and source scans spent it still compares every lockfile,
// and a spent install budget still reads what the framework's configuration
// says.
func TestFrameworkReadsKeepToTheirOwnBudget(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json":      lockedManifest,
		"package-lock.json": `{"name":"shop","lockfileVersion":3,"requires":true,"packages":{"":{"name":"shop","dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"is-number":"7.0.0"}},"node_modules/left-pad":{"version":"1.3.0"},"node_modules/is-number":{"version":"7.0.0","dev":true}}}`,
		"next.config.mjs":   "export default { output: 'export' }",
	}
	budget := newNodeReadBudget()
	budget.nodeConfigBudget().remaining = 0
	source, err := readNodeInstallSource(writeNodeTree(t, files), "", "x64", budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, read := source.files.configText("next"); read || len(source.facts.readings) != 1 || source.facts.readings[0].State != LockfileInSync {
		t.Fatalf("config read %v, readings %+v", read, source.facts.readings)
	}
	budget = newNodeReadBudget()
	budget.nodeConfigBudget()
	budget.remaining = int64(len(files["package.json"]))
	source, err = readNodeInstallSource(writeNodeTree(t, files), "", "x64", budget)
	if err != nil {
		t.Fatal(err)
	}
	if config, read := source.files.configText("next"); !read || nextOutputMode(config.text) != "export" {
		t.Fatalf("next.config = %+v %v", config, read)
	}
}
