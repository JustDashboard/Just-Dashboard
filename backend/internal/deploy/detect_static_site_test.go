package deploy

import (
	"strings"
	"testing"
)

func mapSiteFiles(files map[string]string) siteFiles {
	return func(name string) ([]byte, bool) {
		content, ok := files[name]
		return []byte(content), ok
	}
}

func TestStaticBasePathIsReadOnlyFromLiterals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, framework, output string
		files                   map[string]string
		manifest                nodeManifest
		base, source            string
		expression              bool
	}{
		{name: "docusaurus", framework: "docusaurus", files: map[string]string{"docusaurus.config.ts": "import type {Config} from '@docusaurus/types';\nconst config: Config = {\n  url: 'https://me.github.io',\n  baseUrl: '/project/',\n};\nexport default config;"},
			base: "/project", source: "docusaurus.config.ts"},
		{name: "docusaurus at the root", framework: "docusaurus", files: map[string]string{"docusaurus.config.js": "module.exports = { baseUrl: '/' }"}, source: "docusaurus.config.js"},
		{name: "vite", framework: "vite", files: map[string]string{"vite.config.ts": "export default defineConfig({\n  plugins: [react()],\n  base: \"/repo/\",\n})"},
			base: "/repo", source: "vite.config.ts"},
		{name: "vite computed", framework: "vite", files: map[string]string{"vite.config.js": "export default { base: mode === 'production' ? '/repo/' : '/' }"},
			source: "vite.config.js", expression: true},
		{name: "vite from the build flag", framework: "vite", manifest: nodeManifest{Scripts: map[string]string{"build": "vite build --base=/app/"}},
			base: "/app", source: "package.json"},
		{name: "a database key is not a base", framework: "vite", files: map[string]string{"vite.config.js": "export default { define: { database: '/x' } }"}},
		{name: "astro", framework: "astro", files: map[string]string{"astro.config.mjs": "export default defineConfig({ site: 'https://me.github.io', base: '/docs' })"},
			base: "/docs", source: "astro.config.mjs"},
		{name: "vitepress", framework: "vitepress", output: "docs/.vitepress/dist", files: map[string]string{"docs/.vitepress/config.mts": "export default { base: '/guide/' }"},
			base: "/guide", source: "docs/.vitepress/config.mts"},
		{name: "sveltekit", framework: "sveltekit", files: map[string]string{"svelte.config.js": "export default { kit: { adapter: adapter(), paths: { base: '/app' } } }"},
			base: "/app", source: "svelte.config.js"},
		{name: "sveltekit computed", framework: "sveltekit", files: map[string]string{"svelte.config.js": "export default { kit: { paths: { base: process.argv.includes('dev') ? '' : process.env.BASE_PATH } } }"},
			source: "svelte.config.js", expression: true},
		{name: "angular", framework: "angular", files: map[string]string{"angular.json": `{"projects":{"app":{"architect":{"build":{"options":{"baseHref":"/ng/"}}}}}}`},
			base: "/ng", source: "angular.json"},
		{name: "create react app", framework: "create-react-app", files: map[string]string{"package.json": `{"homepage":"https://me.github.io/cra-app"}`},
			base: "/cra-app", source: "package.json"},
		{name: "create react app, relative", framework: "create-react-app", files: map[string]string{"package.json": `{"homepage":"."}`}},
		{name: "vue cli", framework: "vue-cli", files: map[string]string{"vue.config.js": "module.exports = { publicPath: '/vue/' }"}, base: "/vue", source: "vue.config.js"},
		{name: "gatsby needs --prefix-paths", framework: "gatsby", files: map[string]string{"gatsby-config.js": "module.exports = { pathPrefix: '/blog' }"}},
		{name: "gatsby", framework: "gatsby", files: map[string]string{"gatsby-config.js": "module.exports = { pathPrefix: '/blog' }"},
			manifest: nodeManifest{Scripts: map[string]string{"build": "gatsby build --prefix-paths"}}, base: "/blog", source: "gatsby-config.js"},
		{name: "an unsafe path is no base", framework: "vite", files: map[string]string{"vite.config.js": "export default { base: '/../etc/' }"}, source: "vite.config.js"},
		// Only a key of the exported object is the site's base: VitePress's
		// own sidebar groups carry a base of their own, a commented-out line is
		// not configuration, and Nuxt's runtimeConfig.baseURL is an API's.
		{name: "vitepress sidebar base", framework: "vitepress", output: "docs/.vitepress/dist", files: map[string]string{"docs/.vitepress/config.mts": `import { defineConfig } from 'vitepress'
export default defineConfig({
  title: 'Docs', // base: '/old/'
  themeConfig: {
    sidebar: {
      '/guide/': { base: '/guide/', items: [{ text: 'Start', link: 'start' }] },
    },
  },
})`}},
		{name: "commented vite base", framework: "vite", files: map[string]string{"vite.config.ts": "export default defineConfig({\n  plugins: [react()],\n  // base: '/my-repo/',\n  /* base: '/other/', */\n  server: { proxy: { '/api': 'http://localhost:3000' } },\n})"}},
		{name: "nuxt runtimeConfig baseURL", framework: "nuxt", files: map[string]string{"nuxt.config.ts": "export default defineNuxtConfig({ runtimeConfig: { public: { baseURL: '/api' } } })"}},
		{name: "nuxt app baseURL", framework: "nuxt", files: map[string]string{"nuxt.config.ts": "export default defineNuxtConfig({ ssr: false, app: { head: { title: 'x' }, baseURL: '/nuxt/' } })"},
			base: "/nuxt", source: "nuxt.config.ts"},
		{name: "next basePath", framework: "nextjs", files: map[string]string{"next.config.mjs": "/** @type {import('next').NextConfig} */\nconst nextConfig = { output: 'export', basePath: '/site' };\nexport default withMDX(nextConfig);"},
			base: "/site", source: "next.config.mjs"},
		{name: "vite config function", framework: "vite", files: map[string]string{"vite.config.ts": "export default defineConfig(({ command }) => {\n  const plugins = [{ base: '/nested/' }]\n  return { plugins, base: '/fn/' }\n})"},
			base: "/fn", source: "vite.config.ts"},
		{name: "vite arrow object", framework: "vite", files: map[string]string{"vite.config.ts": "export default defineConfig(({ mode }) => ({ base: `/arrow/`, build: { outDir: 'dist' } }))"},
			base: "/arrow", source: "vite.config.ts"},
		{name: "a url string is not a comment", framework: "astro", files: map[string]string{"astro.config.mjs": "export default defineConfig({ site: 'https://me.github.io', /* c */ base: '/after/' })"},
			base: "/after", source: "astro.config.mjs"},
		{name: "shorthand base", framework: "vite", files: map[string]string{"vite.config.js": "const base = '/repo/'\nexport default { base }"},
			source: "vite.config.js", expression: true},
		{name: "template placeholder", framework: "vite", files: map[string]string{"vite.config.js": "export default { base: `/${process.env.REPO}/` }"},
			source: "vite.config.js", expression: true},
		{name: "a ternary is not a key", framework: "vite", files: map[string]string{"vite.config.js": "export default { title: prod ? base : '/', root: 'src' }"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			base, source, expression := staticBasePath(mapSiteFiles(test.files), test.framework, test.output, test.manifest)
			if base != test.base || source != test.source || expression != test.expression {
				t.Fatalf("got %q %q %v, want %q %q %v", base, source, expression, test.base, test.source, test.expression)
			}
		})
	}
}

func TestSiteOutputsAndFallbacksAreRead(t *testing.T) {
	t.Parallel()
	if fallback := svelteKitFallbackPage("adapter({ pages: 'build', fallback: '200.html' })"); fallback != "200.html" {
		t.Fatalf("fallback = %q", fallback)
	}
	if fallback := svelteKitFallbackPage("adapter({ fallback: '../x.html' })"); fallback != "" {
		t.Fatalf("an unsafe fallback = %q", fallback)
	}
	for _, test := range []struct {
		files    map[string]string
		manifest nodeManifest
		output   string
	}{
		{output: "_site"},
		{files: map[string]string{".eleventy.js": "module.exports = () => ({ dir: { input: 'src', output: 'public' } })"}, output: "public"},
		{files: map[string]string{"eleventy.config.js": "export const config = { dir: { output: './dist/' } }"}, output: "dist"},
		{files: map[string]string{"eleventy.config.js": "export const config = { dir: { output: 'dist' } }"},
			manifest: nodeManifest{Scripts: map[string]string{"build": "npx @11ty/eleventy --output=site"}}, output: "site"},
		{files: map[string]string{"eleventy.config.js": "export const config = { dir: { output: '../outside' } }"}, output: "_site"},
	} {
		if output, _ := eleventyOutput(mapSiteFiles(test.files), test.manifest); output != test.output {
			t.Fatalf("eleventy %v = %q, want %q", test.files, output, test.output)
		}
	}
	if output := hexoPublicDir(mapSiteFiles(map[string]string{"_config.yml": "title: x\npublic_dir: dist # built\n"})); output != "dist" {
		t.Fatalf("hexo = %q", output)
	}
	if node := netlifyNodeVersion([]byte("[build]\n  publish = \"dist\"\n[build.environment]\n  NODE_VERSION = \"20\"\n")); node != "20" {
		t.Fatalf("netlify NODE_VERSION = %q", node)
	}
}

func TestStaticSiteEvidenceIsBounded(t *testing.T) {
	t.Parallel()
	valid := DetectedCandidate{StaticSite: &DetectedStaticSite{Generator: "hugo", Version: "0.166.0", BasePath: "/docs", ThemeSubmodule: "themes/x"}}
	if err := validateDetectedStaticSite(valid); err != nil {
		t.Fatal(err)
	}
	for name, site := range map[string]DetectedStaticSite{
		"unknown generator": {Generator: "frontpage"}, "base path": {BasePath: "/a/../b"}, "theme": {ThemeSubmodule: "../x"},
		"newline": {Declared: "0.1\n"}, "count": {HostingRules: -1}, "long": {Version: strings.Repeat("1", 40)},
	} {
		site := site
		if err := validateDetectedStaticSite(DetectedCandidate{StaticSite: &site}); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// A site built on Netlify pins its Node release in netlify.toml's build
// environment; a version file beside it still comes first.
func TestNetlifyNodeVersionChoosesTheNodeRelease(t *testing.T) {
	t.Parallel()
	manifest := `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"7"}}`
	_, site := siteCandidate(t, map[string]string{"package.json": manifest, "package-lock.json": "{}",
		"netlify.toml": "[build]\n  publish = \"dist\"\n[build.environment]\n  NODE_VERSION = \"20\"\n"})
	if site.NodeVersion != "20 (netlify.toml)" {
		t.Fatalf("node = %q", site.NodeVersion)
	}
	_, site = siteCandidate(t, map[string]string{"package.json": manifest, "package-lock.json": "{}", ".nvmrc": "24\n",
		"netlify.toml": "[build.environment]\n  NODE_VERSION = \"20\"\n"})
	if site.NodeVersion != "24 (.nvmrc)" {
		t.Fatalf("node = %q", site.NodeVersion)
	}
}
