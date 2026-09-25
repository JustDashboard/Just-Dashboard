package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// prepareSite writes a fixture and prepares it with detection's own plan,
// the way quick setup would save it.
func prepareSite(t *testing.T, files map[string]string, backend BuildBackend, variables ...string) (PreparedBuild, *DetectedCandidate, error) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		writeBuildFixture(t, root, name, content)
	}
	result, err := (Detector{}).DetectPath(t.Context(), root, SourceIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	candidate := selectedDetectionCandidate(&result)
	if candidate == nil {
		t.Fatalf("nothing selected: %#v", result.Candidates)
	}
	config := BuildPlanConfig{Method: candidate.BuildMethod, Recipe: candidate.Recipe, BuildCommand: candidate.BuildCommand,
		StartCommand: candidate.StartCommand, OutputDirectory: candidate.OutputDirectory, SPAFallback: candidate.SPAFallback,
		PythonVersion: candidate.PythonVersion}
	if backend == nil {
		backend = &artifactBackendFake{}
	}
	prepared, err := NewArtifactBuilder(backend).Prepare(context.Background(), root, config, false, "just-dashboard/site:run-1", variables...)
	return prepared, candidate, err
}

func assertDockerfileLines(t *testing.T, dockerfile string, want, absent []string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(dockerfile, line) {
			t.Fatalf("Dockerfile lacks %q:\n%s", line, dockerfile)
		}
	}
	for _, line := range absent {
		if strings.Contains(dockerfile, line) {
			t.Fatalf("Dockerfile carries %q:\n%s", line, dockerfile)
		}
	}
}

func TestSiteRecipeRendersEachGenerator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     map[string]string
		variables []string
		toolchain string
		want      []string
		absent    []string
	}{
		{
			name:      "hugo on its official image, as its own user",
			files:     map[string]string{"hugo.toml": "baseURL = '/'\n", "content/_index.md": "", ".hvm": "v0.148.2\n"},
			variables: []string{"HUGO_BASEURL"},
			toolchain: "hugo 0.148.2 (extended)",
			want: []string{"FROM ghcr.io/gohugoio/hugo:v0.148.2@sha256:", " AS build\n", "WORKDIR /project\n", "COPY --chown=hugo:hugo . .\n",
				"RUN --mount=type=secret,id=HUGO_BASEURL,env=HUGO_BASEURL,required=true hugo --gc --minify --baseURL \"${HUGO_BASEURL:-/}\"\n",
				"FROM nginx:1.29-alpine@sha256:", "COPY --from=build /project/public/ /usr/share/nginx/html/\n"},
			absent: []string{"node_modules", "CMD "},
		},
		{
			name: "hugo installs package.json for Hugo Pipes from its lockfile",
			files: map[string]string{"hugo.toml": "baseURL = '/'\n", "content/_index.md": "",
				"package.json":      `{"devDependencies":{"postcss":"8.5.28","postcss-cli":"12.0.0","autoprefixer":"10.6.1"}}`,
				"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"postcss":"8.5.28","postcss-cli":"12.0.0","autoprefixer":"10.6.1"}}}}`},
			toolchain: "hugo 0.166.0 (extended)",
			want: []string{"FROM node:22-alpine@sha256:", " AS deps\n", "RUN npm ci\n", "FROM ghcr.io/gohugoio/hugo:v0.166.0@sha256:",
				"COPY --from=deps --chown=hugo:hugo /app/node_modules ./node_modules\n"},
		},
		{
			name:      "zola 0.23 runs its musl binary on Alpine",
			files:     map[string]string{"config.toml": "base_url = \"https://example.org\"\n", "templates/index.html": "x", "content/_index.md": ""},
			variables: []string{"ZOLA_BASE_URL"},
			toolchain: "zola 0.23.6",
			want: []string{"FROM ghcr.io/getzola/zola:v0.23.6@sha256:", " AS zola\n", "FROM alpine:3.22@sha256:", "COPY --from=zola /zola /usr/local/bin/zola\n",
				"RUN --mount=type=secret,id=ZOLA_BASE_URL,env=ZOLA_BASE_URL,required=true zola build --base-url \"${ZOLA_BASE_URL:-/}\"\n",
				"COPY --from=build /app/public/ /usr/share/nginx/html/\n"},
		},
		{
			name: "an older zola runs its glibc binary on Debian",
			files: map[string]string{"config.toml": "base_url = \"https://example.org\"\ncompile_sass = true\n", "templates/index.html": "x",
				".tool-versions": "zola 0.19.2\n"},
			toolchain: "zola 0.19.2",
			want:      []string{"FROM ghcr.io/getzola/zola:v0.19.2@sha256:", "FROM debian:bookworm-slim@sha256:", "COPY --from=zola /bin/zola /usr/local/bin/zola\n"},
		},
		{
			name:      "mdbook from its release archive, checked against its digest",
			files:     map[string]string{"book.toml": "[book]\ntitle = \"x\"\n", "src/SUMMARY.md": ""},
			toolchain: "mdbook 0.5.4",
			want: []string{"FROM alpine:3.22@sha256:", "ARG TARGETARCH\n", "mdbook-v0.5.4-${arch}-unknown-linux-musl.tar.gz",
				"sum=" + mdbookReleases["0.5"].amd64Checksum, "sum=" + mdbookReleases["0.5"].arm64Checksum, `sha256sum -c -`, "head -c 67108864",
				"ENV MDBOOK_OUTPUT__HTML__SITE_URL=/\n", "RUN mdbook build\n", "COPY --from=build /app/book/ /usr/share/nginx/html/\n"},
		},
		{
			name: "jekyll installs its locked gems frozen",
			files: map[string]string{"Gemfile": "gem 'jekyll'\n", "_config.yml": "title: x\nurl: https://example.org\n", "_posts/a.md": "",
				"Gemfile.lock": "GEM\n  specs:\n    jekyll (4.4.1)\n\nPLATFORMS\n  x86_64-linux\n\nDEPENDENCIES\n  jekyll\n"},
			toolchain: "jekyll on ruby 3.3",
			want: []string{"FROM ruby:3.3-slim@sha256:", "build-essential git", "ENV LANG=C.UTF-8 JEKYLL_ENV=production PAGES_DISABLE_NETWORK=1\n",
				"ENV BUNDLE_FROZEN=true\n", "RUN bundle install --jobs 4 --retry 3\n", "RUN bundle exec jekyll build --baseurl \"\"\n",
				"COPY --from=build /app/_site/ /usr/share/nginx/html/\n"},
			absent: []string{"bundle lock", `gem "github-pages"`},
		},
		{
			name:      "a GitHub Pages site with no Gemfile gets the github-pages gem",
			files:     map[string]string{"_config.yml": "title: x\ntheme: minima\n", "index.md": "# x\n", ".ruby-version": "3.4.5\n"},
			toolchain: "jekyll on ruby 3.4",
			want: []string{"FROM ruby:3.4-slim@sha256:", `RUN printf '%s\n' 'source "https://rubygems.org"' 'gem "github-pages", group: :jekyll_plugins' > Gemfile`,
				`RUN printf '%s\n' 'url: ""' > ` + jekyllURLOverride, "--config _config.yml," + jekyllURLOverride},
			absent: []string{"BUNDLE_FROZEN"},
		},
		{
			name: "a lock written on a Mac gains Linux before installing",
			files: map[string]string{"Gemfile": "gem 'jekyll'\n", "_config.yml": "title: x\n", "_layouts/default.html": "",
				"Gemfile.lock": "GEM\n  specs:\n    jekyll (4.4.1)\n\nPLATFORMS\n  arm64-darwin-23\n\nDEPENDENCIES\n  jekyll\n"},
			toolchain: "jekyll on ruby 3.3",
			want:      []string{"RUN bundle lock --add-platform x86_64-linux aarch64-linux && bundle install --jobs 4 --retry 3\n"},
			absent:    []string{"BUNDLE_FROZEN"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, candidate, err := prepareSite(t, test.files, nil, test.variables...)
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Recipe != "site" || prepared.Recipe != "site" || prepared.Toolchain != test.toolchain {
				t.Fatalf("recipe %q toolchain %q:\n%s", prepared.Recipe, prepared.Toolchain, prepared.DockerfilePreview)
			}
			assertDockerfileLines(t, prepared.DockerfilePreview, test.want, test.absent)
			for _, base := range prepared.BaseImages {
				if !strings.Contains(prepared.DockerfilePreview, base.Reference+"@"+base.Digest) {
					t.Fatalf("base %s is not named by digest:\n%s", base.Reference, prepared.DockerfilePreview)
				}
			}
		})
	}
}

// resolveRefusing is a registry without some tags, as Hugo's is for the
// releases it skipped publishing an image for.
type resolveRefusing struct {
	artifactBackendFake
	missing map[string]bool
}

func (r *resolveRefusing) ResolveImage(ctx context.Context, reference, platform string) (ResolvedImage, error) {
	if r.missing[reference] {
		return ResolvedImage{}, errors.New("manifest unknown")
	}
	return r.artifactBackendFake.ResolveImage(ctx, reference, platform)
}

func TestSiteRecipeFallsBackWhenAPinnedReleaseHasNoImage(t *testing.T) {
	t.Parallel()
	backend := &resolveRefusing{missing: map[string]bool{"ghcr.io/gohugoio/hugo:v0.149.0": true}}
	prepared, _, err := prepareSite(t, map[string]string{"hugo.toml": "baseURL = '/'\n", "content/_index.md": "", ".hvm": "v0.149.0\n"}, backend)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Toolchain != "hugo "+hugoDefaultVersion+" (extended)" || len(prepared.Notes) == 0 ||
		!strings.Contains(prepared.Notes[0], "Hugo 0.149.0 has no official image") ||
		!strings.Contains(prepared.DockerfilePreview, "FROM ghcr.io/gohugoio/hugo:v"+hugoDefaultVersion+"@") {
		t.Fatalf("prepared = %+v\n%s", prepared, prepared.DockerfilePreview)
	}
}

func TestSiteRecipeRefusesWhatItCannotBuild(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		files  map[string]string
		config BuildPlanConfig
		want   string
	}{
		"no generator": {files: map[string]string{"index.html": ""}, config: BuildPlanConfig{Method: BuildRecipe, Recipe: "site", OutputDirectory: "public"},
			want: "the root holds none of their configuration"},
		"no output": {files: map[string]string{"hugo.toml": "baseURL = '/'\n", "content/a.md": ""}, config: BuildPlanConfig{Method: BuildRecipe, Recipe: "site"},
			want: "set the output directory (public)"},
		"a preprocessor it does not install": {files: map[string]string{"book.toml": "[preprocessor.katex]\n", "src/SUMMARY.md": ""},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "site", OutputDirectory: "book"}, want: "mdbook-katex"},
		"an unknown mkdocs plugin without requirements": {files: map[string]string{"mkdocs.yml": "site_name: x\nplugins:\n  - mystery\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", OutputDirectory: "site", BuildCommand: "mkdocs build"}, want: "plugin mystery"},
		"python output with no build command and no generator": {files: map[string]string{"requirements.txt": "jinja2==3.1.4\n"},
			config: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", OutputDirectory: "out"}, want: "set a build command"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for path, content := range test.files {
				writeBuildFixture(t, root, path, content)
			}
			_, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, test.config, false, "t:1")
			if err == nil || !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPythonAndDenoRecipesServeTheirSiteOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		files  map[string]string
		want   []string
		absent []string
	}{
		{
			name:  "mkdocs from its requirements",
			files: map[string]string{"mkdocs.yml": "site_name: x\ntheme: material\n", "requirements.txt": "mkdocs-material==9.7.7\n", ".python-version": "3.12\n"},
			want: []string{"FROM python:3.12-slim@sha256:", " AS build\n", "RUN pip install --no-cache-dir --requirement requirements.txt\n",
				"RUN mkdocs build\n", "FROM nginx:1.29-alpine@sha256:", "COPY --from=build /app/site/ /usr/share/nginx/html/\n"},
			absent: []string{"CMD ", "gunicorn"},
		},
		{
			name:  "mkdocs with nothing declared installs pinned releases",
			files: map[string]string{"mkdocs.yml": "site_name: x\ntheme:\n  name: material\nplugins:\n  - search\n  - git-revision-date-localized\n"},
			want: []string{"RUN pip install --no-cache-dir mkdocs==1.6.1 mkdocs-material==9.7.7 mkdocs-git-revision-date-localized-plugin==1.6.0\n",
				// The plugin dates pages from their commits.
				"apt-get install -y --no-install-recommends git"},
		},
		{
			name:  "a project whose manifest lacks the generator gets it pinned beside",
			files: map[string]string{"mkdocs.yml": "site_name: x\n", "requirements.txt": "requests==2.32.3\n"},
			want:  []string{"RUN pip install --no-cache-dir --requirement requirements.txt\n", "RUN pip install --no-cache-dir mkdocs==1.6.1\n"},
		},
		{
			name: "sphinx from what Read the Docs installs",
			files: map[string]string{"docs/conf.py": "project = 'x'\n", "docs/index.rst": "x\n",
				".readthedocs.yaml": "version: 2\nsphinx:\n  configuration: docs/conf.py\npython:\n  install:\n    - requirements: docs/requirements.txt\n    - method: pip\n      path: .\n      extra_requirements:\n        - docs\n"},
			want: []string{"RUN pip install --no-cache-dir --requirement docs/requirements.txt\n", "RUN pip install --no-cache-dir '.[docs]'\n",
				"RUN sphinx-build -b html docs docs/_build/html\n", "COPY --from=build /app/docs/_build/html/ /usr/share/nginx/html/\n"},
		},
		{
			name:  "pelican with its publish settings",
			files: map[string]string{"pelicanconf.py": "PATH = 'content'\n", "publishconf.py": "", "requirements.txt": "pelican[markdown]==4.12.0\n"},
			want:  []string{"RUN pelican content -o output -s publishconf.py\n", "COPY --from=build /app/output/ /usr/share/nginx/html/\n"},
		},
		{
			name: "lume builds with its task and nginx serves the output",
			files: map[string]string{"deno.json": `{"imports":{"lume/":"https://deno.land/x/lume@v3.2.5/"},"tasks":{"build":"deno task lume","lume":"echo x"}}`,
				"deno.lock": "{}", "_config.ts": "const site = lume({ dest: \"./out\" })\n"},
			want: []string{"FROM denoland/deno:alpine@sha256:", " AS build\n", "RUN deno install --frozen\n", "RUN deno task build\n",
				"FROM nginx:1.29-alpine@sha256:", "COPY --from=build /app/out/ /usr/share/nginx/html/\n"},
			absent: []string{"CMD "},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, candidate, err := prepareSite(t, test.files, nil)
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Profile != ProfileStatic || candidate.StartCommand != "" {
				t.Fatalf("candidate = %#v", candidate)
			}
			assertDockerfileLines(t, prepared.DockerfilePreview, test.want, test.absent)
		})
	}
}
