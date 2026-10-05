package deploy

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

// Generic ranking picks a repository's main app. Recovery instead binds to
// the captured entrypoint, so a monorepo worker cannot become its web sibling.
func bindHostRecoveryCandidate(detection *DetectionResult, capture *procs.HostWorkloadCapture, root string) {
	entry := ""
	paths := []string{capture.SourcePath}
	if len(capture.Command) > 1 {
		paths = append(paths, capture.Command[1:]...)
	}
	for _, candidate := range paths {
		if strings.HasPrefix(candidate, "-") {
			continue
		}
		switch strings.ToLower(filepath.Ext(candidate)) {
		case ".js", ".mjs", ".cjs", ".ts", ".py":
		default:
			continue
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		relative, err := filepath.Rel(root, candidate)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, "../") {
			entry = filepath.ToSlash(relative)
			break
		}
	}
	if entry == "" && len(capture.Command) > 1 && strings.HasPrefix(filepath.Base(capture.Command[0]), "python") {
		for _, operand := range capture.Command[1:] {
			module, _, _ := strings.Cut(operand, ":")
			if strings.HasPrefix(module, "-") || module == "" || strings.ContainsAny(module, "/\\ ") {
				continue
			}
			module = strings.ReplaceAll(module, ".", "/")
			for _, relative := range []string{module + ".py", module + "/__init__.py"} {
				if safeRelativePath(relative) {
					if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err == nil && info.Mode().IsRegular() {
						entry = relative
						break
					}
				}
			}
			if entry != "" {
				break
			}
		}
	}
	best, score, ambiguous := -1, -1, false
	for index, candidate := range detection.Candidates {
		candidateRoot := strings.Trim(strings.TrimPrefix(filepath.ToSlash(candidate.Root), "./"), "/")
		if candidateRoot == "." {
			candidateRoot = ""
		}
		if entry == "" && candidateRoot != "" {
			continue
		}
		if entry != "" && candidateRoot != "" && !strings.HasPrefix(entry, candidateRoot+"/") {
			continue
		}
		candidateScore := len(candidateRoot) * 4
		if candidate.BuildMethod == BuildDockerfile {
			candidateScore++
		}
		if candidate.ID == detection.SelectedID {
			candidateScore++
		}
		if candidateScore > score {
			best, score, ambiguous = index, candidateScore, false
		} else if candidateScore == score {
			ambiguous = true
		}
	}
	if best >= 0 && !ambiguous {
		detection.SelectedID = detection.Candidates[best].ID
	} else {
		detection.SelectedID = "unbound-native-entrypoint"
	}
}

func supportedNativePythonCommand(command []string) bool {
	if len(command) < 2 {
		return false
	}
	base := filepath.Base(command[0])
	if base != "python" && base != "python3" && !strings.HasPrefix(base, "python3.") {
		return false
	}
	for index := 1; index < len(command); index++ {
		argument := command[index]
		if argument == "-m" {
			return index+1 < len(command) && command[index+1] != "" && !strings.ContainsAny(command[index+1], "/\\ ")
		}
		if argument == "-c" || argument == "-" || strings.HasPrefix(argument, "-c") {
			return false
		}
		if strings.HasPrefix(argument, "-") {
			if argument != "-u" && argument != "-B" && argument != "-O" && argument != "-OO" && argument != "-I" && argument != "-s" && argument != "-S" {
				return false
			}
			continue
		}
		return strings.HasSuffix(argument, ".py")
	}
	return false
}
