package deploy

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Submodules and Git LFS used to be a decision on every candidate of any
// repository that declared them, answerable only by leaving the configure
// screens and importing again. Most of the time the answer is knowable: a
// theme submodule on the same host as the repository is fetched with the
// repository's own access, and LFS objects matter only when files the build
// root holds are tracked by it. Detection now works that out, and asks only
// about a submodule on another host, whose access nobody has configured.

type lfsPattern struct {
	base    string
	pattern *regexp.Regexp
	name    bool
}

var gitModuleURLHostRE = regexp.MustCompile(`^[A-Za-z0-9._-]+@([A-Za-z0-9.-]+):`)

// readGitAttributes keeps the patterns a .gitattributes file puts under the
// LFS filter.
func (s *repoShapeScan) readGitAttributes(rel string, content []byte) {
	base := path.Dir(rel)
	if base == "." {
		base = ""
	}
	for _, raw := range strings.Split(string(manifestText(content)), "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") || len(s.lfsPatterns) >= 64 {
			continue
		}
		lfs := false
		for _, attribute := range fields[1:] {
			if attribute == "filter=lfs" {
				lfs = true
			}
		}
		if !lfs {
			continue
		}
		if expression, name, ok := gitAttributesPattern(fields[0]); ok {
			s.lfsPatterns = append(s.lfsPatterns, lfsPattern{base: base, pattern: expression, name: name})
		}
	}
}

// gitAttributesPattern compiles one gitattributes path pattern. A pattern
// with no slash matches a file name at any depth; one with a slash matches
// the path relative to the attributes file, where ** crosses directories.
func gitAttributesPattern(pattern string) (*regexp.Regexp, bool, bool) {
	pattern = strings.TrimPrefix(pattern, "/")
	name := !strings.Contains(pattern, "/")
	var expression strings.Builder
	expression.WriteString("^")
	for index := 0; index < len(pattern); index++ {
		switch character := pattern[index]; {
		case strings.HasPrefix(pattern[index:], "**/"):
			expression.WriteString("(?:.*/)?")
			index += 2
		case strings.HasPrefix(pattern[index:], "/**"):
			expression.WriteString("/.*")
			index += 2
		case strings.HasPrefix(pattern[index:], "**"):
			expression.WriteString(".*")
			index++
		case character == '*':
			expression.WriteString("[^/]*")
		case character == '?':
			expression.WriteString("[^/]")
		case character == '[':
			end := strings.IndexByte(pattern[index:], ']')
			if end < 0 {
				expression.WriteString(`\[`)
				continue
			}
			class := pattern[index+1 : index+end]
			class = strings.Replace(class, "!", "^", 1)
			expression.WriteString("[" + regexp.QuoteMeta(class) + "]")
			index += end
		default:
			expression.WriteString(regexp.QuoteMeta(string(character)))
		}
	}
	expression.WriteString("$")
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, false, false
	}
	return compiled, name, true
}

// lfsTracked says whether a file lies under an LFS filter pattern.
func (s *repoShapeScan) lfsTracked(rel string) bool {
	for _, pattern := range s.lfsPatterns {
		if pattern.base != "" && !strings.HasPrefix(rel, pattern.base+"/") {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(rel, pattern.base), "/")
		subject := relative
		if pattern.name {
			subject = path.Base(relative)
		}
		if pattern.pattern.MatchString(subject) {
			return true
		}
	}
	return false
}

// parseGitModules reads .gitmodules as data: each submodule's path, and
// whether its URL is reachable with the repository's own access.
func parseGitModules(content []byte, remote string) []GitSubmodule {
	sourceHost := gitURLHost(remote)
	var result []GitSubmodule
	current := GitSubmodule{}
	currentURL := ""
	flush := func() {
		if current.Path != "" && safeRelativePath(current.Path) && len(result) < 64 {
			host := gitURLHost(currentURL)
			current.SameSource = strings.HasPrefix(currentURL, "./") || strings.HasPrefix(currentURL, "../") ||
				(host != "" && host == sourceHost)
			result = append(result, current)
		}
		current, currentURL = GitSubmodule{}, ""
	}
	for _, raw := range strings.Split(string(manifestText(content)), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			flush()
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "path":
			current.Path = strings.Trim(strings.TrimSpace(value), `"`)
		case "url":
			currentURL = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	flush()
	return result
}

func gitURLHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if match := gitModuleURLHostRE.FindStringSubmatch(raw); match != nil && !strings.Contains(raw, "://") {
		return strings.ToLower(match[1])
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// applyGitRequirements records the submodules and LFS files and turns them
// into evidence, or a decision only for a submodule on another host.
func (s *repoShapeScan) applyGitRequirements(result *DetectionResult) {
	requirements := GitRequirements{
		Submodules: s.gitModulesPath != "", LFS: len(s.lfsPatterns) > 0 || s.lfsDeclared,
		LFSChecked: true, LFSFiles: s.lfsCount, LFSPaths: append([]string(nil), s.lfsFiles...),
	}
	if s.gitModulesPath != "" {
		requirements.SubmoduleList = parseGitModules(s.gitModules, result.Source.Remote)
		requirements.SubmodulesChecked = true
	}
	result.GitRequirements = requirements
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		for _, submodule := range requirements.SubmoduleList {
			if !submoduleInRoot(submodule, candidate.Root) {
				continue
			}
			if submodule.SameSource {
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: s.gitModulesPath,
					Reason: "Git submodule " + submodule.Path + " is on the repository's own host and is fetched with it"})
				continue
			}
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: s.gitModulesPath,
				Reason: "Git submodule " + submodule.Path + " is on another host"})
			candidate.NeedsDecision = append(candidate.NeedsDecision,
				"confirm access to Git submodule "+submodule.Path+" on another host, or leave submodules off")
		}
		if tracked, _ := lfsFilesUnder(requirements, candidate.Root); tracked > 0 {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: rootLabelOf(candidate.Root),
				Reason: fmt.Sprintf("%d file(s) under this root are stored in Git LFS and are downloaded with the source", tracked)})
		}
	}
}

// submoduleInRoot says whether a build root needs a submodule: it lies
// inside the root, or the root lies inside it.
func submoduleInRoot(submodule GitSubmodule, root string) bool {
	return underRoot(submodule.Path, root) || underRoot(root, submodule.Path)
}

// lfsFilesUnder counts the LFS-tracked files a build root holds, and says
// whether the count is exact. The repository root's is; a nested root is
// counted from the listed paths, which hold every tracked file only when the
// list was not cut short.
func lfsFilesUnder(requirements GitRequirements, root string) (int, bool) {
	if root == "" {
		return requirements.LFSFiles, true
	}
	count := 0
	for _, file := range requirements.LFSPaths {
		if underRoot(file, root) {
			count++
		}
	}
	return count, requirements.LFSFiles <= len(requirements.LFSPaths)
}
