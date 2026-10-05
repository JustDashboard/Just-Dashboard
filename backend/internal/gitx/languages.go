package gitx

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Language is one language's share of a checkout's tracked code, by bytes.
type Language struct {
	Name  string  `json:"name"`
	Share float64 `json:"share"`
}

// maxLanguageFiles bounds the stat calls one checkout can cost. A tree larger
// than this is measured on its first files in git's own order, which still
// names what the repository is written in.
const maxLanguageFiles = 50000

// languageExtensions names a file's language the way GitHub's Linguist does,
// for the languages the Git page has a mark for. Anything else — prose, data,
// configuration, images, lockfiles — is not code and is left out of the
// shares, as Linguist leaves it out of a repository's language bar.
var languageExtensions = map[string][]string{
	"TypeScript": {".ts", ".tsx", ".mts", ".cts"},
	"JavaScript": {".js", ".jsx", ".mjs", ".cjs"},
	"Go":         {".go"},
	"Python":     {".py", ".pyi"},
	"Rust":       {".rs"},
	"Java":       {".java"},
	"Kotlin":     {".kt", ".kts"},
	"Ruby":       {".rb", ".rake"},
	"PHP":        {".php"},
	"Swift":      {".swift"},
	"C":          {".c", ".h"},
	"C++":        {".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
	"C#":         {".cs"},
	"Haskell":    {".hs"},
	"Lua":        {".lua"},
	"R":          {".r"},
	"HTML":       {".html", ".htm"},
	"CSS":        {".css"},
	"SCSS":       {".scss"},
	"Shell":      {".sh", ".bash", ".zsh"},
	"Vue":        {".vue"},
	"Svelte":     {".svelte"},
	"Dart":       {".dart"},
	"Elixir":     {".ex", ".exs"},
	"Scala":      {".scala", ".sc"},
	"Zig":        {".zig"},
	"Perl":       {".pl", ".pm"},
	"PowerShell": {".ps1", ".psm1"},
	"Dockerfile": {".dockerfile"},
}

var languageByExtension = func() map[string]string {
	out := map[string]string{}
	for name, extensions := range languageExtensions {
		for _, ext := range extensions {
			out[ext] = name
		}
	}
	return out
}()

// vendoredDirs are directories whose contents are somebody else's code or a
// build's output; a checkout that commits its dependencies is not written in
// them.
var vendoredDirs = map[string]bool{
	"node_modules": true, "vendor": true, "third_party": true, "dist": true, "build": true,
}

// languageOf is the language a tracked path counts towards, or "".
func languageOf(file string) string {
	segments := strings.Split(file, "/")
	for _, segment := range segments[:len(segments)-1] {
		if vendoredDirs[segment] {
			return ""
		}
	}
	base := segments[len(segments)-1]
	lower := strings.ToLower(base)
	if lower == "dockerfile" || strings.HasPrefix(lower, "dockerfile.") {
		return "Dockerfile"
	}
	if strings.HasSuffix(lower, ".min.js") || strings.HasSuffix(lower, ".min.css") {
		return ""
	}
	return languageByExtension[strings.ToLower(filepath.Ext(base))]
}

// languageCache remembers each checkout's shares for the commit they were
// measured at: the list polls every minute, and the tracked tree only moves
// when HEAD does.
type languageCache struct {
	head      string
	languages []Language
}

// Languages measures what a checkout's tracked files are written in, at most
// six languages, largest first. A language under one per cent is dropped
// unless it is the only one, so a single stray script does not take a slot.
func (s *Service) Languages(ctx context.Context, path, head string) []Language {
	if head == "" {
		return nil
	}
	if cached, ok := s.languages.Load(path); ok {
		if entry := cached.(languageCache); entry.head == head {
			return entry.languages
		}
	}
	out, err := s.run(ctx, path, "ls-files", "-z", "--cached")
	if err != nil {
		return nil
	}
	bytes := map[string]int64{}
	var total int64
	for i, file := range strings.Split(out, "\x00") {
		if i >= maxLanguageFiles {
			break
		}
		name := languageOf(file)
		if file == "" || name == "" {
			continue
		}
		fi, err := os.Lstat(filepath.Join(path, file))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		bytes[name] += fi.Size()
		total += fi.Size()
	}
	languages := shares(bytes, total)
	s.languages.Store(path, languageCache{head: head, languages: languages})
	return languages
}

func shares(bytes map[string]int64, total int64) []Language {
	if total == 0 {
		return nil
	}
	all := make([]Language, 0, len(bytes))
	for name, n := range bytes {
		all = append(all, Language{Name: name, Share: float64(n) / float64(total)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Share != all[j].Share {
			return all[i].Share > all[j].Share
		}
		return all[i].Name < all[j].Name
	})
	out := []Language{}
	for i, lang := range all {
		if len(out) == 6 || (i > 0 && lang.Share < 0.01) {
			break
		}
		// Rounded down, so six shares never add up past the whole.
		lang.Share = math.Floor(lang.Share*1000) / 1000
		out = append(out, lang)
	}
	return out
}
