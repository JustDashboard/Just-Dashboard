package deploy

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
)

// A repository written on macOS or Windows can import './components/header'
// while the file is Header.tsx: the author's case-insensitive disk resolves
// it, and the Linux build fails with "Module not found". Detection already
// reads the application's source for the environment it uses; the same read
// collects relative import specifiers, which are resolved against the file
// list the walk recorded. Only a specifier with no exact match and exactly one
// case-insensitive one is reported — an alias, a generated file or a typo
// that matches nothing says nothing about case.

const sourceShapeMaxSpecifiers = 2000

var (
	jsImportFromRE    = regexp.MustCompile(`(?:import|export)\s+(?:type\s+)?[^'";]*?\sfrom\s*['"](\.{1,2}/[^'"\n]+)['"]`)
	jsImportBareRE    = regexp.MustCompile(`(?m)^\s*import\s*['"](\.{1,2}/[^'"\n]+)['"]`)
	jsImportCallRE    = regexp.MustCompile(`(?:\bimport|\brequire)\s*\(\s*['"](\.{1,2}/[^'"\n]+)['"]\s*\)`)
	phpNamespaceRE    = regexp.MustCompile(`(?m)^\s*namespace\s+([A-Za-z0-9_\\]+)\s*;`)
	phpClassRE        = regexp.MustCompile(`(?m)^\s*(?:(?:final|abstract|readonly)\s+)*(?:class|interface|trait|enum)\s+([A-Za-z0-9_]+)`)
	quotedPathRE      = regexp.MustCompile(`['"]([^'"\n]{1,256})['"]`)
	bullmqWorkerRE    = regexp.MustCompile(`new\s+Worker\s*[(<]`)
	jsResolveSuffixes = []string{"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".json", ".vue", ".svelte", ".d.ts"}
)

type sourceImport struct {
	file      string
	line      int
	specifier string
}

type phpDeclaration struct {
	file      string
	namespace string
	class     string
}

type staticReference struct {
	file string
	path string
}

// sourceShapeScan collects, from the application source the environment
// scanner already reads, what repository shape needs from it.
type sourceShapeScan struct {
	imports      []sourceImport
	php          []phpDeclaration
	static       []staticReference
	queueWorkers []string
}

func newSourceShapeScan() *sourceShapeScan { return &sourceShapeScan{} }

func (s *sourceShapeScan) scan(rel, lowerName string, content []byte) {
	switch path.Ext(lowerName) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".tsx", ".jsx", ".vue", ".svelte", ".astro":
		for _, expression := range []*regexp.Regexp{jsImportFromRE, jsImportBareRE, jsImportCallRE} {
			for _, match := range expression.FindAllSubmatchIndex(content, -1) {
				if len(s.imports) >= sourceShapeMaxSpecifiers {
					break
				}
				specifier := string(content[match[2]:match[3]])
				s.imports = append(s.imports, sourceImport{file: rel, line: 1 + strings.Count(string(content[:match[2]]), "\n"), specifier: specifier})
			}
		}
		text := string(content)
		if strings.Contains(text, "bullmq") && bullmqWorkerRE.MatchString(text) && len(s.queueWorkers) < 16 {
			s.queueWorkers = append(s.queueWorkers, rel)
		}
		for _, line := range strings.Split(text, "\n") {
			if len(s.static) >= 64 {
				break
			}
			if !strings.Contains(line, "static(") && !strings.Contains(line, "sendFile(") && !strings.Contains(line, "root:") {
				continue
			}
			for _, quoted := range quotedPathRE.FindAllStringSubmatch(line, -1) {
				value := quoted[1]
				if strings.Contains(value, "dist") || strings.Contains(value, "build") {
					s.static = append(s.static, staticReference{file: rel, path: value})
				}
			}
		}
	case ".php":
		namespace := phpNamespaceRE.FindSubmatch(content)
		class := phpClassRE.FindSubmatch(content)
		if namespace != nil && class != nil && len(s.php) < sourceShapeMaxSpecifiers {
			s.php = append(s.php, phpDeclaration{file: rel, namespace: string(namespace[1]), class: string(class[1])})
		}
	}
}

func (s *sourceShapeScan) firstUnder(files []string, root string) string {
	for _, file := range files {
		if underRoot(file, root) {
			return file
		}
	}
	return ""
}

// resolveCase looks for a file the relative specifier names, exactly and then
// ignoring case. It returns the one case-insensitive match when there is no
// exact one and exactly one file differs only in case.
func (s *repoShapeScan) resolveCase(file, specifier string) (string, bool) {
	specifier, _, _ = strings.Cut(specifier, "?")
	specifier, _, _ = strings.Cut(specifier, "#")
	target := path.Clean(path.Join(path.Dir(file), specifier))
	if target == "." || strings.HasPrefix(target, "../") || target == ".." {
		return "", false
	}
	candidates := []string{}
	for _, suffix := range jsResolveSuffixes {
		candidates = append(candidates, target+suffix)
	}
	switch ext := path.Ext(target); ext {
	case ".js", ".jsx", ".mjs", ".cjs":
		base := strings.TrimSuffix(target, ext)
		for _, replacement := range []string{".ts", ".tsx", ".mts", ".cts"} {
			candidates = append(candidates, base+replacement)
		}
	}
	for _, suffix := range jsResolveSuffixes[1:] {
		candidates = append(candidates, target+"/index"+suffix)
	}
	for _, candidate := range candidates {
		if s.files[candidate] {
			return "", false
		}
	}
	matches := map[string]bool{}
	for _, candidate := range candidates {
		for _, actual := range s.filesLower[strings.ToLower(candidate)] {
			matches[actual] = true
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	for actual := range matches {
		return actual, true
	}
	return "", false
}

// applyImportCase attaches each case mismatch to the candidates whose build
// includes the file.
func (s *repoShapeScan) applyImportCase(result *DetectionResult) {
	if s.filesTruncated {
		return
	}
	var mismatches []ImportCaseMismatch
	for _, item := range s.sources.imports {
		if actual, ok := s.resolveCase(item.file, item.specifier); ok {
			mismatches = append(mismatches, ImportCaseMismatch{File: item.file, Line: item.line, Specifier: item.specifier, Actual: actual, Language: "javascript"})
		}
	}
	mismatches = append(mismatches, s.psr4Mismatches(result)...)
	if len(mismatches) == 0 {
		return
	}
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.NotDeployable != "" || candidate.BuildMethod == BuildStatic || candidate.BuildMethod == BuildCompose {
			continue
		}
		for _, mismatch := range mismatches {
			if len(candidate.ImportCaseMismatches) >= 16 {
				break
			}
			if !underRoot(mismatch.File, candidate.Root) || ownedByNestedCandidate(mismatch.File, candidate.Root, result.Candidates) {
				continue
			}
			if (mismatch.Language == "javascript" && candidate.Recipe != "node" && candidate.BuildMethod != BuildDockerfile) ||
				(mismatch.Language == "php" && candidate.Recipe != "php" && candidate.BuildMethod != BuildDockerfile) {
				continue
			}
			if rejectPlanSecretLiteral("import specifier", mismatch.Specifier) != nil {
				continue
			}
			candidate.ImportCaseMismatches = append(candidate.ImportCaseMismatches, mismatch)
		}
	}
}

func ownedByNestedCandidate(file, root string, candidates []DetectedCandidate) bool {
	for _, other := range candidates {
		if other.Root != root && underRoot(other.Root, root) && underRoot(file, other.Root) {
			return true
		}
	}
	return false
}

// psr4Mismatches compares each PHP class the scan read with the file name
// Composer's PSR-4 map expects for it. Composer's optimized autoloader skips a
// class whose file differs only in case, so it is "Class not found" at runtime.
func (s *repoShapeScan) psr4Mismatches(result *DetectionResult) []ImportCaseMismatch {
	var mismatches []ImportCaseMismatch
	seenRoots := map[string]bool{}
	for _, candidate := range result.Candidates {
		if candidate.Recipe != "php" || seenRoots[candidate.Root] {
			continue
		}
		seenRoots[candidate.Root] = true
		content, err := readContainedRegular(s.root, joinRoot(candidate.Root, "composer.json"), 512<<10)
		if err != nil {
			continue
		}
		var manifest struct {
			Autoload struct {
				PSR4 map[string]json.RawMessage `json:"psr-4"`
			} `json:"autoload"`
		}
		if json.Unmarshal(manifestText(content), &manifest) != nil {
			continue
		}
		prefixes := make([]string, 0, len(manifest.Autoload.PSR4))
		for prefix := range manifest.Autoload.PSR4 {
			prefixes = append(prefixes, prefix)
		}
		sort.Strings(prefixes)
		for _, declaration := range s.sources.php {
			if !underRoot(declaration.file, candidate.Root) {
				continue
			}
			for _, prefix := range prefixes {
				var directories []string
				var single string
				if json.Unmarshal(manifest.Autoload.PSR4[prefix], &single) == nil {
					directories = []string{single}
				} else {
					_ = json.Unmarshal(manifest.Autoload.PSR4[prefix], &directories)
				}
				full := declaration.namespace + `\` + declaration.class
				if prefix == "" || !strings.HasPrefix(strings.ToLower(full), strings.ToLower(prefix)) {
					continue
				}
				relativeClass := strings.ReplaceAll(full[len(prefix):], `\`, "/") + ".php"
				for _, directory := range directories {
					expected := path.Clean(joinRoot(candidate.Root, path.Join(strings.TrimSuffix(directory, "/"), relativeClass)))
					if expected != declaration.file && strings.EqualFold(expected, declaration.file) {
						mismatches = append(mismatches, ImportCaseMismatch{File: declaration.file, Specifier: full, Actual: expected, Language: "php"})
					}
				}
			}
		}
	}
	return mismatches
}
