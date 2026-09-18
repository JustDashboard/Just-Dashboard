package deploy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The Python recipe builds on the maintained CPython releases. A version
// outside this set is a Dockerfile, not a guess at an image tag.
const defaultPythonRecipeVersion = "3.13"

var (
	pythonRecipeVersionRE  = regexp.MustCompile(`^3\.(10|11|12|13)$`)
	pythonRecipeVersions   = []string{"3.10", "3.11", "3.12", "3.13"}
	pythonVersionLiteralRE = regexp.MustCompile(`3\.[0-9]{1,2}`)
	pythonRequiresRE       = regexp.MustCompile(`(?m)^\s*(?:requires-python|python)\s*=\s*"([^"]+)"`)
	pythonSpecifierRE      = regexp.MustCompile(`(>=|~=|\^|==|>|<=|<)\s*(3\.[0-9]{1,2})(\.[0-9*]+)?`)
)

// pythonServerPackages are the process managers detection points a start
// command at. When the manifests do not declare one, the recipe installs
// this exact release into the same environment as the application, so a
// detected start command never runs a binary the image does not have.
var pythonServerPackages = map[string]string{
	"gunicorn": "gunicorn==23.0.0",
	"uvicorn":  "uvicorn==0.35.0",
}

// choosePythonRecipeVersion resolves the interpreter family for a build.
// An explicit setting wins; then `.python-version`, then Heroku-style
// `runtime.txt`, then the lower bound of pyproject's requires-python (or
// Poetry's python constraint), picking the newest catalogue release the
// constraint allows. With nothing declared the maintained default applies.
func choosePythonRecipeVersion(explicit, versionFile, runtimeFile, pyproject string) (string, error) {
	selected := strings.TrimSpace(explicit)
	if selected == "" {
		selected = pythonVersionFromFile(versionFile)
	}
	if selected == "" {
		selected = pythonVersionFromFile(runtimeFile)
	}
	if selected == "" {
		if match := pythonRequiresRE.FindStringSubmatch(pyproject); match != nil {
			selected = pythonVersionForConstraint(match[1])
		}
	}
	if selected == "" {
		selected = defaultPythonRecipeVersion
	}
	if !pythonRecipeVersionRE.MatchString(selected) {
		return "", fmt.Errorf("%w: the Python recipe supports Python 3.10 to 3.13; select a supported version or use a Dockerfile", ErrUnsupportedBuilder)
	}
	return selected, nil
}

// pythonVersionFromFile reads the family out of the first meaningful line of
// a version file: "3.12", "3.12.4", "python-3.12.4", "cpython@3.12".
func pythonVersionFromFile(content string) string {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return pythonVersionLiteralRE.FindString(line)
	}
	return ""
}

// pythonVersionForConstraint picks the newest catalogue release inside a
// PEP 440 or Poetry-style range. Only the operators that bound a family are
// read: `>=`, `>`, `^` and a two-part `~=` set a floor; `<` and `<=` a
// ceiling; `==3.12.*` and a three-part `~=3.11.0` mean that family alone.
// Anything stranger leaves the choice to the default.
func pythonVersionForConstraint(constraint string) string {
	floor, ceiling, exact := "", "", ""
	for _, match := range pythonSpecifierRE.FindAllStringSubmatch(constraint, -1) {
		operator, version, patch := match[1], match[2], match[3]
		switch operator {
		case ">=", ">", "^":
			floor = version
		case "~=":
			if patch != "" {
				exact = version
			} else {
				floor = version
			}
		case "==":
			exact = version
		case "<":
			ceiling = version
		case "<=":
			ceiling = version + "+"
		}
	}
	if exact != "" {
		for _, version := range pythonRecipeVersions {
			if version == exact {
				return version
			}
		}
		return exact
	}
	candidates := append([]string(nil), pythonRecipeVersions...)
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	for _, version := range candidates {
		if floor != "" && pythonMinor(version) < pythonMinor(floor) {
			continue
		}
		if ceiling != "" {
			inclusive := strings.HasSuffix(ceiling, "+")
			limit := pythonMinor(strings.TrimSuffix(ceiling, "+"))
			if (inclusive && pythonMinor(version) > limit) || (!inclusive && pythonMinor(version) >= limit) {
				continue
			}
		}
		return version
	}
	return ""
}

func pythonMinor(version string) int {
	_, minor, _ := strings.Cut(version, ".")
	value := 0
	for _, r := range minor {
		if r < '0' || r > '9' {
			break
		}
		value = value*10 + int(r-'0')
	}
	return value
}

// pythonInstall is how the recipe installs dependencies at a root: which
// manifest it reads, the exact install command, and the environment the
// installed packages live in so the start command finds them.
type pythonInstall struct {
	kind    string
	command string
	env     []string
	// serverInstall installs an undeclared process manager into the same
	// environment as the dependencies.
	serverInstall string
}

// selectPythonInstall reads the manifests at a root in order of how much
// they pin: a uv or Poetry lock, then requirements.txt, then a bare
// pyproject. Unpinned requirements are accepted — refusing them turned the
// most ordinary Python repository there is into a manual Dockerfile — and
// preflight warns about what a rebuild may resolve differently.
func selectPythonInstall(root, version string) (pythonInstall, error) {
	switch {
	case regularExists(root, "uv.lock"):
		return pythonInstall{
			kind:    "uv.lock",
			command: "pip install --no-cache-dir uv && uv sync --frozen --no-dev",
			// uv installs into the project's .venv; putting it first on PATH is
			// what lets the start command say `uvicorn` rather than a path.
			env:           []string{"VIRTUAL_ENV=/app/.venv", "PATH=/app/.venv/bin:$PATH"},
			serverInstall: "uv pip install --no-cache",
		}, nil
	case regularExists(root, "poetry.lock"), regularExists(root, "pyproject.toml") && pyprojectUsesPoetry(root):
		return pythonInstall{
			kind:    "poetry.lock",
			command: "pip install --no-cache-dir poetry && poetry install --only main --no-root --no-interaction",
			// Installing into the interpreter itself rather than a Poetry
			// virtualenv keeps the start command's `gunicorn` on PATH.
			env:           []string{"POETRY_VIRTUALENVS_CREATE=false"},
			serverInstall: "pip install --no-cache-dir",
		}, nil
	case regularExists(root, "requirements.txt"):
		return pythonInstall{
			kind:          "requirements.txt",
			command:       "pip install --no-cache-dir --requirement requirements.txt",
			serverInstall: "pip install --no-cache-dir",
		}, nil
	case regularExists(root, "pyproject.toml"):
		// A PEP 621 project with no lock: read its dependency list with the
		// standard library rather than `pip install .`, which needs a build
		// backend and package discovery an application never set up.
		loader := `import tomllib`
		prefix := ""
		if version == "3.10" {
			loader = `import tomli as tomllib`
			prefix = "pip install --no-cache-dir tomli==2.0.1 && "
		}
		return pythonInstall{
			kind: "pyproject.toml",
			command: prefix + `python -c '` + loader + `; print("\n".join(tomllib.load(open("pyproject.toml","rb")).get("project",{}).get("dependencies",[])))' > /tmp/jd-requirements.txt` +
				" && pip install --no-cache-dir --requirement /tmp/jd-requirements.txt",
			serverInstall: "pip install --no-cache-dir",
		}, nil
	}
	return pythonInstall{}, fmt.Errorf("%w: Python recipe requires requirements.txt, pyproject.toml, uv.lock or poetry.lock", ErrUnsupportedBuilder)
}

func pyprojectUsesPoetry(root string) bool {
	content, err := readContainedRegular(root, "pyproject.toml", 512<<10)
	return err == nil && strings.Contains(string(content), "[tool.poetry]")
}

// undeclaredPythonServers lists the process managers a start command runs
// that no manifest declares, in the order they are run.
func undeclaredPythonServers(start string, deps pythonDependencies) []string {
	var result []string
	seen := map[string]bool{}
	for _, segment := range strings.Split(start, "&&") {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		program := fields[0]
		if _, ok := pythonServerPackages[program]; ok && !deps.has(program) && !seen[program] {
			seen[program] = true
			result = append(result, program)
		}
	}
	return result
}
