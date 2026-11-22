package deploy

import (
	"regexp"
	"strconv"
	"strings"
)

// buildPulls says whether a build passes `--pull`, which makes BuildKit ask
// the registry about every image the Dockerfile names, even one it holds.
//
// A recipe's or a static site's generated Dockerfile names each base, and
// each image it copies from, by digest, and its frontend too
// (recipeDockerfileSyntax): there is nothing for a registry to answer that
// could change what is built. An image BuildKit does not hold is still
// fetched by that digest. Everything else keeps `--pull`:
//
//   - a user's Dockerfile, and a Compose service's, name mutable tags, and
//     `--pull` is what makes a deploy build on what the tag names today;
//   - a Railpack plan names its builder and runtime images by tag
//     (ghcr.io/railwayapp/railpack-builder:mise-…), inside the plan, where
//     the dashboard does not pin them.
//
// The answer is read from the Dockerfile the build will run, so a generated
// file that ever names an image by tag alone gets `--pull` back.
func buildPulls(prepared PreparedBuild) bool {
	switch prepared.Method {
	case BuildRecipe, BuildStatic:
		return !dockerfileImagesPinned(prepared.DockerfilePreview)
	}
	return true
}

var (
	// digestPinnedImageRE is a reference that names its content:
	// registry/path:tag@sha256:<64 hex>, with nothing left to expand.
	digestPinnedImageRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)
	syntaxDirectiveRE   = regexp.MustCompile(`^#\s*syntax\s*=\s*(\S+)\s*$`)
)

// dockerfileImagesPinned reports whether every image the Dockerfile reads —
// its frontend, each FROM, each COPY --from and RUN --mount from= that is
// not an earlier stage — is pinned by digest.
func dockerfileImagesPinned(content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			break
		}
		if match := syntaxDirectiveRE.FindStringSubmatch(strings.ToLower(trimmed)); match != nil &&
			!digestPinnedImageRE.MatchString(match[1]) {
			return false
		}
	}
	parsed := parseDockerfile([]byte(content))
	stages := map[string]bool{}
	count := 0
	pinnedOrStage := func(reference string) bool {
		reference = strings.TrimSpace(reference)
		if stages[strings.ToLower(reference)] {
			return true
		}
		if index, err := strconv.Atoi(reference); err == nil {
			return index >= 0 && index < count
		}
		return digestPinnedImageRE.MatchString(reference)
	}
	for _, instruction := range parsed.Instructions {
		switch instruction.Keyword {
		case "FROM":
			words := shellWords(instruction.Args, parsed.Escape)
			if len(words) == 0 {
				return false
			}
			if base := words[0]; base != "scratch" && !pinnedOrStage(base) {
				return false
			}
			if len(words) >= 3 && strings.EqualFold(words[1], "as") {
				stages[strings.ToLower(words[2])] = true
			}
			count++
		case "COPY":
			if from, ok := instruction.flag("from"); ok && !pinnedOrStage(from) {
				return false
			}
		case "RUN":
			for _, flag := range instruction.Flags {
				options, ok := strings.CutPrefix(flag, "--mount=")
				if !ok {
					continue
				}
				for _, option := range strings.Split(options, ",") {
					if from, ok := strings.CutPrefix(option, "from="); ok && !pinnedOrStage(from) {
						return false
					}
				}
			}
		}
	}
	return count > 0
}
