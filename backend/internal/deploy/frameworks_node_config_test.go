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
		if got := nextOutputMode(jsWithoutComments(text)); got != want {
			t.Errorf("nextOutputMode(%q) = %q, want %q", text, got, want)
		}
	}
	for text, want := range map[string]string{
		"import adapter from '@sveltejs/adapter-auto';\nexport default { kit: { adapter: adapter() } }":                                                     "@sveltejs/adapter-auto",
		"import node from '@sveltejs/adapter-node';\nimport vercel from '@sveltejs/adapter-vercel';\nexport default { kit: { adapter: node() } }":           "@sveltejs/adapter-node",
		"import node from '@sveltejs/adapter-node';\nimport staticAdapter from '@sveltejs/adapter-static';\nconst a = process.env.X ? node : staticAdapter": "",
		"export default { kit: {} }": "",
	} {
		if got := svelteAdapter(jsWithoutComments(text)); got != want {
			t.Errorf("svelteAdapter(%q) = %q, want %q", text, got, want)
		}
	}
	for text, want := range map[string]string{
		"basePath: '/docs'":   "/docs",
		"basePath: '/docs/'":  "/docs",
		"basePath: '/'":       "",
		"basePath: '/../etc'": "",
		"basePath: prefix":    "",
	} {
		if got := literalBasePath(basePathRE, text); got != want {
			t.Errorf("literalBasePath(%q) = %q, want %q", text, got, want)
		}
	}
	if got := literalRelativePath(nextDistDirRE, "distDir: '../outside'"); got != "" {
		t.Fatalf("escaping distDir = %q", got)
	}
	if got := jsWithoutComments("const a = `// kept`; // dropped\n/* dropped */ const b = '/* kept */'"); got != "const a = `// kept`; \n  const b = '/* kept */'" {
		t.Fatalf("jsWithoutComments = %q", got)
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
