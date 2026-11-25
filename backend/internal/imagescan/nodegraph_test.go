package imagescan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// layerFile is one entry of a test layer: a file with its content, a
// symlink (link), a hard link (hard) or a whiteout (a name starting .wh.).
type layerFile struct {
	name, content, link, hard string
}

func pkg(name string, fields map[string]any) string {
	fields["name"] = name
	raw, _ := json.Marshal(fields)
	return string(raw)
}

func deps(names ...string) map[string]string {
	out := map[string]string{}
	for _, name := range names {
		out[name] = "*"
	}
	return out
}

func layerTar(t *testing.T, files []layerFile) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, file := range files {
		header := &tar.Header{Name: file.name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(file.content))}
		switch {
		case file.link != "":
			header = &tar.Header{Name: file.name, Typeflag: tar.TypeSymlink, Linkname: file.link, Mode: 0o777}
		case file.hard != "":
			header = &tar.Header{Name: file.name, Typeflag: tar.TypeLink, Linkname: file.hard, Mode: 0o644}
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(file.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func compressLayer(t *testing.T, raw []byte, how string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	switch how {
	case "gzip":
		writer := gzip.NewWriter(&buffer)
		_, _ = writer.Write(raw)
		_ = writer.Close()
	case "zstd":
		writer, err := zstd.NewWriter(&buffer)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(raw)
		_ = writer.Close()
	default:
		return raw
	}
	return buffer.Bytes()
}

// saveArchive writes a `docker save` archive of layers in the OCI layout
// containerd's store writes: blobs first, manifest.json after them.
func saveArchive(t *testing.T, layers ...[]byte) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	var names []string
	for i, layer := range layers {
		name := fmt.Sprintf("blobs/sha256/%064d", i+1)
		names = append(names, name)
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o444, Size: int64(len(layer)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(layer)
	}
	manifest, _ := json.Marshal([]map[string]any{{"Config": "blobs/sha256/config", "Layers": names}})
	_ = writer.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg})
	_, _ = writer.Write(manifest)
	_ = writer.Close()
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readGraph(t *testing.T, archive string) *NodeGraph {
	t.Helper()
	graph, err := ReadNodeGraph(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func classified(graph *NodeGraph, paths ...string) map[string]string {
	findings := make([]Finding, len(paths))
	for i, path := range paths {
		findings[i] = Finding{ID: "CVE-1", Package: path, Path: path}
	}
	out := map[string]string{}
	for _, finding := range graph.Classify(findings) {
		out[finding.Path] = strings.TrimSuffix(finding.Dependency+" "+finding.Via, " ")
	}
	return out
}

// The image a recipe ships: the base's npm, then the application with every
// dependency it installed, development ones included. What the application
// runs is told from what only its development tools brought, and a binary
// is the package's it sits in.
func TestNodeGraphTellsWhatTheApplicationRunsFromWhatItsToolsBrought(t *testing.T) {
	base := layerTar(t, []layerFile{
		{name: "usr/local/lib/node_modules/npm/package.json", content: pkg("npm", map[string]any{"dependencies": deps("glob")})},
		{name: "usr/local/lib/node_modules/npm/node_modules/glob/package.json", content: pkg("glob", map[string]any{})},
	})
	app := layerTar(t, []layerFile{
		{name: "app/package.json", content: pkg("shop", map[string]any{
			"dependencies":    deps("next", "react"),
			"devDependencies": deps("drizzle-kit", "eslint", "react"),
		})},
		{name: "app/node_modules/next/package.json", content: pkg("next", map[string]any{
			"dependencies":         deps("postcss", "@swc/helpers"),
			"peerDependencies":     deps("react", "@playwright/test", "sass"),
			"peerDependenciesMeta": map[string]any{"@playwright/test": map[string]any{"optional": true}, "sass": map[string]any{"optional": true}},
		})},
		{name: "app/node_modules/next/node_modules/postcss/package.json", content: pkg("postcss", map[string]any{})},
		{name: "app/node_modules/postcss/package.json", content: pkg("postcss", map[string]any{})},
		{name: "app/node_modules/@swc/helpers/package.json", content: pkg("@swc/helpers", map[string]any{})},
		{name: "app/node_modules/react/package.json", content: pkg("react", map[string]any{})},
		{name: "app/node_modules/@playwright/test/package.json", content: pkg("@playwright/test", map[string]any{})},
		{name: "app/node_modules/drizzle-kit/package.json", content: pkg("drizzle-kit", map[string]any{"dependencies": deps("esbuild", "@esbuild-kit/esm-loader")})},
		{name: "app/node_modules/esbuild/package.json", content: pkg("esbuild", map[string]any{"optionalDependencies": deps("@esbuild/linux-x64")})},
		{name: "app/node_modules/@esbuild/linux-x64/package.json", content: pkg("@esbuild/linux-x64", map[string]any{})},
		{name: "app/node_modules/@esbuild/linux-x64/bin/esbuild", content: "\x7fELF"},
		{name: "app/node_modules/@esbuild-kit/esm-loader/package.json", content: pkg("@esbuild-kit/esm-loader", map[string]any{"dependencies": deps("@esbuild-kit/core-utils")})},
		{name: "app/node_modules/@esbuild-kit/core-utils/package.json", content: pkg("@esbuild-kit/core-utils", map[string]any{"dependencies": deps("esbuild")})},
		{name: "app/node_modules/@esbuild-kit/core-utils/node_modules/esbuild/package.json", content: pkg("esbuild", map[string]any{"optionalDependencies": deps("@esbuild/linux-x64")})},
		{name: "app/node_modules/@esbuild-kit/core-utils/node_modules/esbuild/node_modules/@esbuild/linux-x64/package.json", content: pkg("@esbuild/linux-x64", map[string]any{})},
		{name: "app/node_modules/eslint/package.json", content: pkg("eslint", map[string]any{"dependencies": deps("brace-expansion")})},
		{name: "app/node_modules/brace-expansion/package.json", content: pkg("brace-expansion", map[string]any{})},
		{name: "app/node_modules/left-over/package.json", content: pkg("left-over", map[string]any{})},
	})
	for _, how := range []string{"gzip", "zstd", "plain"} {
		t.Run(how, func(t *testing.T) {
			graph := readGraph(t, saveArchive(t, compressLayer(t, base, how), compressLayer(t, app, how)))
			got := classified(graph,
				"app/node_modules/next/package.json",
				"app/node_modules/react/package.json",
				"app/node_modules/next/node_modules/postcss/package.json",
				"app/node_modules/postcss/package.json",
				"app/node_modules/@swc/helpers/package.json",
				"app/node_modules/@playwright/test/package.json",
				"app/node_modules/@esbuild/linux-x64/bin/esbuild",
				"app/node_modules/@esbuild-kit/core-utils/node_modules/esbuild/node_modules/@esbuild/linux-x64/bin/esbuild",
				"app/node_modules/brace-expansion/package.json",
				"app/node_modules/left-over/package.json",
				"usr/local/lib/node_modules/npm/node_modules/glob/package.json",
				"usr/local/lib/node_modules/npm/package.json",
				"app/server.js",
			)
			want := map[string]string{
				"app/node_modules/next/package.json": "direct next",
				// Named in both: what the application runs wins.
				"app/node_modules/react/package.json": "direct react",
				// Next.js's own copy is the one it loads; the hoisted one
				// nothing the application runs needs.
				"app/node_modules/next/node_modules/postcss/package.json": "indirect next",
				"app/node_modules/postcss/package.json":                   "development",
				"app/node_modules/@swc/helpers/package.json":              "indirect next",
				// An optional peer is the host's choice, here a test tool.
				"app/node_modules/@playwright/test/package.json":                                                            "development",
				"app/node_modules/@esbuild/linux-x64/bin/esbuild":                                                           "development drizzle-kit",
				"app/node_modules/@esbuild-kit/core-utils/node_modules/esbuild/node_modules/@esbuild/linux-x64/bin/esbuild": "development drizzle-kit",
				"app/node_modules/brace-expansion/package.json":                                                             "development eslint",
				"app/node_modules/left-over/package.json":                                                                   "development",
				// A global install is there to be run.
				"usr/local/lib/node_modules/npm/node_modules/glob/package.json": "indirect npm",
				"usr/local/lib/node_modules/npm/package.json":                   "direct npm",
				// Not in node_modules: not this reading's to say.
				"app/server.js": "",
			}
			for path, dependency := range want {
				if got[path] != dependency {
					t.Errorf("%s = %q, want %q", path, got[path], dependency)
				}
			}
		})
	}
}

// pnpm's store is links: node_modules/next is a link into .pnpm, where each
// package's own dependencies are links beside it. Node follows them from the
// real path, and so does the reading.
func TestNodeGraphFollowsAStoreOfLinks(t *testing.T) {
	layer := layerTar(t, []layerFile{
		{name: "app/package.json", content: pkg("shop", map[string]any{"dependencies": deps("next"), "devDependencies": deps("vite")})},
		{name: "app/node_modules/next", link: ".pnpm/next@16.4.6/node_modules/next"},
		{name: "app/node_modules/vite", link: "/app/node_modules/.pnpm/vite@7.0.0/node_modules/vite"},
		{name: "app/node_modules/.pnpm/next@16.4.6/node_modules/next/package.json", content: pkg("next", map[string]any{"dependencies": deps("styled-jsx")})},
		{name: "app/node_modules/.pnpm/next@16.4.6/node_modules/styled-jsx", link: "../../styled-jsx@5.1.6/node_modules/styled-jsx"},
		{name: "app/node_modules/.pnpm/styled-jsx@5.1.6/node_modules/styled-jsx/package.json", content: pkg("styled-jsx", map[string]any{})},
		{name: "app/node_modules/.pnpm/vite@7.0.0/node_modules/vite/package.json", content: pkg("vite", map[string]any{"dependencies": deps("esbuild")})},
		{name: "app/node_modules/.pnpm/vite@7.0.0/node_modules/esbuild", link: "../../esbuild@0.25.0/node_modules/esbuild"},
		{name: "app/node_modules/.pnpm/esbuild@0.25.0/node_modules/esbuild/package.json", content: pkg("esbuild", map[string]any{})},
		// A loop goes nowhere rather than forever.
		{name: "app/node_modules/loop", link: "loop"},
	})
	graph := readGraph(t, saveArchive(t, compressLayer(t, layer, "gzip")))
	got := classified(graph,
		"app/node_modules/.pnpm/next@16.4.6/node_modules/next/package.json",
		"app/node_modules/.pnpm/styled-jsx@5.1.6/node_modules/styled-jsx/package.json",
		"app/node_modules/.pnpm/esbuild@0.25.0/node_modules/esbuild/package.json",
	)
	want := map[string]string{
		"app/node_modules/.pnpm/next@16.4.6/node_modules/next/package.json":            "direct next",
		"app/node_modules/.pnpm/styled-jsx@5.1.6/node_modules/styled-jsx/package.json": "indirect next",
		"app/node_modules/.pnpm/esbuild@0.25.0/node_modules/esbuild/package.json":      "development vite",
	}
	for path, dependency := range want {
		if got[path] != dependency {
			t.Errorf("%s = %q, want %q", path, got[path], dependency)
		}
	}
	if _, ok := graph.resolve("app", "loop"); ok {
		t.Fatal("a link to itself resolved")
	}
}

// A later layer's whiteouts take away what an earlier one installed: a
// package removed, a folder emptied, and a package.json hard-linked in.
func TestNodeGraphAppliesWhiteouts(t *testing.T) {
	first := layerTar(t, []layerFile{
		{name: "app/package.json", content: pkg("shop", map[string]any{"dependencies": deps("kept", "gone", "hard")})},
		{name: "app/node_modules/kept/package.json", content: pkg("kept", map[string]any{"dependencies": deps("inner")})},
		{name: "app/node_modules/kept/node_modules/inner/package.json", content: pkg("inner", map[string]any{})},
		{name: "app/node_modules/gone/package.json", content: pkg("gone", map[string]any{})},
		{name: "app/node_modules/hard-source/package.json", content: pkg("hard", map[string]any{})},
		{name: "app/node_modules/hard/package.json", hard: "app/node_modules/hard-source/package.json"},
	})
	second := layerTar(t, []layerFile{
		{name: "app/node_modules/.wh.gone"},
		{name: "app/node_modules/kept/node_modules/.wh..wh..opq"},
	})
	graph := readGraph(t, saveArchive(t, compressLayer(t, first, "gzip"), compressLayer(t, second, "gzip")))
	for name, want := range map[string]bool{"kept": true, "gone": false, "hard": true} {
		if _, ok := graph.resolve("app", name); ok != want {
			t.Errorf("%s resolves = %v, want %v", name, ok, want)
		}
	}
	if _, ok := graph.resolve("app/node_modules/kept", "inner"); ok {
		t.Fatal("an emptied folder still holds its package")
	}
}

// An archive that is not one image, or a manifest that does not parse, is
// not read as an image with nothing in it.
func TestNodeGraphRefusesWhatItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.tar")
	_ = os.WriteFile(path, layerTar(t, []layerFile{{name: "manifest.json", content: "[]"}}), 0o644)
	if _, err := ReadNodeGraph(context.Background(), path); err == nil {
		t.Fatal("an archive with no image was read")
	}
	if _, err := ReadNodeGraph(context.Background(), filepath.Join(t.TempDir(), "missing.tar")); err == nil {
		t.Fatal("a missing archive was read")
	}
	manifest := parseNodeManifest([]byte(`{"dependencies": ["not", "a", "map"], "optionalDependencies": {"../escape": "1", "ok": "1"}}`))
	if len(manifest.runtime) != 1 || manifest.runtime[0] != "ok" {
		t.Fatalf("a manifest of odd shapes = %+v", manifest)
	}
	if NeedsNodeGraph([]Finding{{Path: "usr/lib/python3/site-packages/x/METADATA"}, {}}) {
		t.Fatal("findings with nothing under node_modules asked for the graph")
	}
	if !NeedsNodeGraph([]Finding{{Path: "app/node_modules/x/package.json"}}) {
		t.Fatal("a node package did not ask for the graph")
	}
}
