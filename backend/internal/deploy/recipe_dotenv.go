package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A Go or Rust service that loads .env and treats its absence as fatal —
// the tutorial `if err := godotenv.Load(); err != nil { log.Fatal(...) }`, or
// `dotenvy::dotenv().expect(...)` — exits at start in a recipe image, which
// copies only the binary. The file is gitignored, so it is never in the
// checkout either. An empty file satisfies the load, carries no value, and
// both loaders leave variables already in the environment alone, so the
// dashboard's values still win.

var safeWorkdirRE = regexp.MustCompile(`^/[A-Za-z0-9_./-]*$`)

// dotenvFileRequired reads the source at preparation, bounded, for a fatal
// .env load: Go files outside tests and vendored code, and Rust's binary
// entry points.
func dotenvFileRequired(root, kind string) bool {
	switch kind {
	case "rust":
		candidates := []string{"src/main.rs"}
		if entries, err := os.ReadDir(filepath.Join(root, "src", "bin")); err == nil {
			for _, entry := range entries {
				if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".rs") && len(candidates) < 32 {
					candidates = append(candidates, "src/bin/"+entry.Name())
				}
			}
		}
		for _, candidate := range candidates {
			if content, err := readContainedRegular(root, candidate, 1<<20); err == nil && rustDotenvFatalRE.Match(content) {
				return true
			}
		}
	case "go":
		found := false
		count := 0
		stop := errors.New("stop")
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if rel != "." && (entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			if count++; count > 2000 {
				return stop
			}
			content, err := readContainedRegular(root, rel, 1<<20)
			if err != nil || !strings.Contains(string(content), "godotenv") {
				return nil
			}
			for _, match := range goDotenvLoadRE.FindAllIndex(content, -1) {
				if goDotenvFatalRE.Match(content[match[1]:min(len(content), match[1]+240)]) {
					found = true
					return stop
				}
			}
			return nil
		})
		return found
	}
	return false
}

// withEmptyDotenv creates the empty .env in the final stage's working
// directory, as root and before the stage drops to its unprivileged user.
func withEmptyDotenv(lines []string) []string {
	stage := 0
	for index, line := range lines {
		if strings.HasPrefix(line, "FROM ") {
			stage = index
		}
	}
	directory := "/"
	insert := len(lines)
	for index := stage; index < len(lines); index++ {
		line := lines[index]
		if strings.HasPrefix(line, "WORKDIR ") {
			if dir := strings.TrimSpace(strings.TrimPrefix(line, "WORKDIR ")); safeWorkdirRE.MatchString(dir) {
				directory = dir
			}
		}
		if insert == len(lines) && (strings.HasPrefix(line, "USER ") || strings.HasPrefix(line, "ENTRYPOINT ") || strings.HasPrefix(line, "CMD ")) {
			insert = index
		}
	}
	file := strings.TrimSuffix(directory, "/") + "/.env"
	command := "RUN touch " + file
	if directory != "/" {
		command = "RUN mkdir -p " + directory + " && touch " + file
	}
	result := append([]string{}, lines[:insert]...)
	result = append(result, command)
	return append(result, lines[insert:]...)
}
