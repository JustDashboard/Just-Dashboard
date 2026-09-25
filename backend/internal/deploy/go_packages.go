package deploy

import (
	"errors"
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Which main package a Go module builds is read from its sources the way the
// go command reads them — package clauses and build constraints — and never
// by compiling or running anything. Detection and the recipe share this, so
// the package detection proposes is the package the recipe agrees to build.

// goSourceFacts is what one non-test .go file says about its package.
type goSourceFacts struct {
	// dir is the file's directory, slash-separated and relative to the scan
	// root; "." is the root itself.
	dir string
	pkg string
	// built says the go command would compile the file for the recipe's
	// target — linux on this host's architecture with cgo disabled — judged
	// on its file name and its //go:build line. A file importing "C" is
	// judged on its explicit constraint alone, so a cgo file its author did
	// not restrict to cgo builds still counts as one the build needs.
	built bool
	cgo   bool
	// builtCgo is the same judgement with cgo enabled, which a file
	// restricted to cgo builds passes and its `!cgo` twin fails.
	builtCgo bool
	// serves says the file imports an HTTP or RPC server package, which is
	// what tells a service's main package from a worker's or a tool's.
	serves bool
	// cgoLinks are the system libraries a cgo file's preamble links for
	// linux: "pkg-config:<name>" and "lib:<name>" (from -l in LDFLAGS).
	cgoLinks []string
	// embeds are the file's //go:embed patterns.
	embeds []string
}

// goServerImports are the packages whose import marks a main package that
// listens: the standard library's server and the routers and RPC frameworks
// a Go service is commonly built on.
var goServerImports = []string{
	"net/http", "github.com/gin-gonic/gin", "github.com/labstack/echo", "github.com/gofiber/fiber",
	"github.com/go-chi/chi", "github.com/gorilla/mux", "github.com/julienschmidt/httprouter",
	"google.golang.org/grpc", "connectrpc.com/connect", "github.com/valyala/fasthttp",
}

// goKnownOS and goKnownArch are the names a file-name suffix constrains on,
// as go/build recognises them.
var (
	goKnownOS = map[string]bool{
		"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true,
		"illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true, "openbsd": true,
		"plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true,
	}
	goKnownArch = map[string]bool{
		"386": true, "amd64": true, "amd64p32": true, "arm": true, "armbe": true, "arm64": true, "arm64be": true,
		"loong64": true, "mips": true, "mipsle": true, "mips64": true, "mips64le": true, "mips64p32": true,
		"mips64p32le": true, "ppc": true, "ppc64": true, "ppc64le": true, "riscv": true, "riscv64": true,
		"s390": true, "s390x": true, "sparc": true, "sparc64": true, "wasm": true,
	}
)

// readGoSourceFacts parses only a file's header: its build constraint, its
// package clause and its imports.
func readGoSourceFacts(rel string, content []byte) (goSourceFacts, bool) {
	file, err := parser.ParseFile(token.NewFileSet(), path.Base(rel), content, parser.ImportsOnly)
	if err != nil || file.Name == nil {
		return goSourceFacts{}, false
	}
	facts := goSourceFacts{dir: path.Dir(filepath.ToSlash(rel)), pkg: file.Name.Name}
	for _, imported := range file.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		if value == "C" {
			facts.cgo = true
		}
		for _, server := range goServerImports {
			if value == server || strings.HasPrefix(value, server+"/") {
				facts.serves = true
			}
		}
	}
	named := goFileNameMatches(path.Base(rel))
	facts.built = named && goConstraintSatisfied(content, false)
	facts.builtCgo = named && goConstraintSatisfied(content, true)
	if facts.cgo {
		facts.cgoLinks = goCgoLinks(content)
	}
	facts.embeds = goEmbedPatterns(content)
	return facts, true
}

// goFileNameMatches applies the _GOOS, _GOARCH and _GOOS_GOARCH file-name
// suffixes for the recipe's target.
func goFileNameMatches(name string) bool {
	parts := strings.Split(strings.TrimSuffix(name, ".go"), "_")
	if len(parts) < 2 {
		return true
	}
	last := parts[len(parts)-1]
	if len(parts) >= 3 && goKnownOS[parts[len(parts)-2]] && goKnownArch[last] {
		return parts[len(parts)-2] == "linux" && last == runtime.GOARCH
	}
	if goKnownOS[last] {
		return last == "linux"
	}
	if goKnownArch[last] {
		return last == runtime.GOARCH
	}
	return true
}

// goConstraintSatisfied evaluates the //go:build line (or, lacking one, the
// legacy +build lines) above the package clause for linux on this host's
// architecture with no extra tags, with or without cgo, which is how the
// recipe builds. `ignore`, `tools` and mage files therefore drop out.
func goConstraintSatisfied(content []byte, cgo bool) bool {
	var expression constraint.Expr
	var plus []constraint.Expr
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "//") {
			break
		}
		if !constraint.IsGoBuild(line) && !constraint.IsPlusBuild(line) {
			continue
		}
		parsed, err := constraint.Parse(line)
		if err != nil {
			// The go command refuses a malformed constraint too.
			return false
		}
		if constraint.IsGoBuild(line) {
			expression = parsed
		} else {
			plus = append(plus, parsed)
		}
	}
	if expression == nil {
		for _, legacy := range plus {
			if expression == nil {
				expression = legacy
			} else {
				expression = &constraint.AndExpr{X: expression, Y: legacy}
			}
		}
	}
	if expression == nil {
		return true
	}
	return expression.Eval(func(tag string) bool {
		switch tag {
		case "linux", "unix", "gc", runtime.GOARCH:
			return true
		case "cgo":
			return cgo
		}
		// Release tags: the recipe's toolchain is new enough for any go1.N a
		// module that builds on it would name.
		return strings.HasPrefix(tag, "go1.")
	})
}

// goSkippedDirectory names the directories the go command never builds a
// module's own packages from: testdata, and names starting with _ or .; plus
// the dependency and output trees a checkout carries.
func goSkippedDirectory(name string) bool {
	return name == "testdata" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") ||
		name == "vendor" || name == "node_modules"
}

// goModulePackages is one module's view of its sources.
type goModulePackages struct {
	// mains are the directories holding a buildable package main.
	mains []string
	// serving marks the mains whose files import a server package.
	serving map[string]bool
	// cgo names the directories whose files import "C" with no pure-Go twin
	// beside them (a file built only without cgo): a CGO_ENABLED=0 build
	// would drop them and lose what they define, so the build needs cgo.
	cgo []string
	// cgoLinks are the system libraries every cgo file a cgo build compiles
	// links, as goSourceFacts names them.
	cgoLinks []string
	// embeds are the //go:embed patterns of the module's built files.
	embeds []goEmbed
	// templMissing names .templ components with no generated _templ.go
	// beside them, which `templ generate` writes.
	templMissing []string
}

// goEmbed is one //go:embed pattern and the directory of the file that
// declares it, which the pattern is relative to.
type goEmbed struct {
	dir, pattern string
}

// collectGoModulePackages reduces per-file facts to one module's packages.
// Facts below a skipped directory or inside a nested module are left out, as
// the go command leaves them out of the module's own package list.
func collectGoModulePackages(facts []goSourceFacts, nestedModules []string) goModulePackages {
	result := goModulePackages{serving: map[string]bool{}}
	mains := map[string]bool{}
	// A file built only when cgo is off is the pure-Go fallback of the cgo
	// files beside it.
	twinned := map[string]bool{}
	for _, file := range facts {
		if !file.cgo && file.built && !file.builtCgo {
			twinned[file.dir] = true
		}
	}
	for _, file := range facts {
		if goDirectorySkipped(file.dir, nestedModules) {
			continue
		}
		if file.cgo && file.builtCgo {
			result.cgoLinks = append(result.cgoLinks, file.cgoLinks...)
			if !twinned[file.dir] {
				result.cgo = append(result.cgo, file.dir)
			}
		}
		if file.built && !file.cgo {
			for _, pattern := range file.embeds {
				if len(result.embeds) < goEmbedsKept {
					result.embeds = append(result.embeds, goEmbed{dir: file.dir, pattern: pattern})
				}
			}
		}
		if file.cgo || !file.built || file.pkg != "main" {
			continue
		}
		mains[file.dir] = true
		if file.serves {
			result.serving[file.dir] = true
		}
	}
	for dir := range mains {
		result.mains = append(result.mains, dir)
	}
	sort.Strings(result.mains)
	result.cgo = uniqueSorted(result.cgo)
	result.cgoLinks = uniqueSorted(result.cgoLinks)
	return result
}

func goDirectorySkipped(dir string, nestedModules []string) bool {
	if dir != "." {
		for _, segment := range strings.Split(dir, "/") {
			if goSkippedDirectory(segment) {
				return true
			}
		}
	}
	for _, nested := range nestedModules {
		if dir == nested || strings.HasPrefix(dir, nested+"/") {
			return true
		}
	}
	return false
}

// goMainPackagesKept is how many main packages a candidate lists. A tools
// monorepo can hold hundreds; the ranking reads them all, and the list the
// plan is checked against says how many it left out.
const goMainPackagesKept = 64

// applyGoModulePackages records a Go candidate's main packages, and the one
// the ranking chose, from the recipe's own scan of the module. A module with
// no main package is a library: it stays listed, at low confidence, so a
// service elsewhere in the repository is selected over it.
func applyGoModulePackages(candidate *DetectedCandidate, packages goModulePackages, marker *detectedMarkers) {
	for _, main := range packages.mains {
		if len(candidate.GoMainPackages) < goMainPackagesKept && validGoPackagePath(main) {
			candidate.GoMainPackages = append(candidate.GoMainPackages, main)
		} else {
			candidate.GoMainPackagesOmitted++
		}
	}
	if candidate.NotDeployable != "" {
		// The repository's shape already says the module is not a service
		// (detect_not_deployable.go): its only commands are examples or
		// tools. The mains are kept for a plan that builds one anyway.
		return
	}
	modulePath, moduleFile := "", path.Join(candidate.Root, "go.mod")
	if marker != nil {
		modulePath, moduleFile = goModulePath(marker.goModContent), filepath.ToSlash(marker.goMod)
	}
	chosen, reason := chooseGoMainPackage(packages.mains, packages.serving, modulePath)
	switch {
	case chosen != "":
		if validGoPackagePath(chosen) {
			candidate.GoPackage = chosen
		}
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: path.Join(candidate.Root, chosen), Reason: detectionLine("builds " + goPackageArgument(chosen) + ": " + reason),
		})
	case len(packages.mains) == 0:
		label := candidate.Root
		if label == "" {
			label = "."
		}
		candidate.GoLibrary = true
		candidate.Name = "Go library in " + label
		candidate.Confidence = ConfidenceLow
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: moduleFile, Reason: "no buildable package main: a library the recipe cannot run",
		})
		*candidate = newDetectedCandidate(candidate.Root, candidate.BuildMethod, *candidate)
	default:
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{
			Path: moduleFile, Reason: detectionLine("main packages " + goMainPackageList(packages.mains, 0)),
		})
		candidate.NeedsDecision = append(candidate.NeedsDecision,
			detectionLine("choose the Go main package to build: "+goMainPackageList(packages.mains, 0)))
	}
}

// detectionLine bounds a line of detection text to what a saved detection
// accepts.
func detectionLine(text string) string {
	if bounded, cut := truncateUTF8(text, 509); cut {
		return bounded + "..."
	}
	return text
}

// chooseGoMainPackage ranks a module's main packages the way its layout
// conventionally names the served one: the module root, then
// cmd/<module name>, then cmd/server, cmd/api, cmd/web or cmd/app when only
// one of them exists, then the only main that imports a server package. A
// tie is left to the operator rather than guessed.
func chooseGoMainPackage(mains []string, serving map[string]bool, modulePath string) (string, string) {
	switch len(mains) {
	case 0:
		return "", ""
	case 1:
		return mains[0], "the module's only main package"
	}
	if slices.Contains(mains, ".") {
		return ".", "main package at the module root"
	}
	if name := goModuleName(modulePath); name != "" && slices.Contains(mains, "cmd/"+name) {
		return "cmd/" + name, "cmd/ directory named after the module"
	}
	conventional := []string{}
	for _, name := range []string{"server", "api", "web", "app"} {
		if slices.Contains(mains, "cmd/"+name) {
			conventional = append(conventional, "cmd/"+name)
		}
	}
	if len(conventional) == 1 {
		return conventional[0], "conventional server directory"
	}
	pool := mains
	if len(conventional) > 1 {
		pool = conventional
	}
	served := []string{}
	for _, main := range pool {
		if serving[main] {
			served = append(served, main)
		}
	}
	if len(served) == 1 {
		return served[0], "the only main package that starts a server"
	}
	return "", ""
}

// goModuleName is the last element of a module path, without a /vN suffix.
func goModuleName(modulePath string) string {
	modulePath = strings.Trim(modulePath, "/")
	name := path.Base(modulePath)
	if len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = path.Base(path.Dir(modulePath))
	}
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// goModulePath reads the module directive of a go.mod.
func goModulePath(module []byte) string {
	for _, line := range strings.Split(string(module), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

// goModuleToolchain reads the toolchain directive of a go.mod.
func goModuleToolchain(module []byte) string {
	for _, line := range strings.Split(string(module), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "toolchain" && len(fields[1]) <= 32 {
			return fields[1]
		}
	}
	return ""
}

// goVersionFileValue is the first meaningful line of a .go-version file.
func goVersionFileValue(content []byte) string {
	for _, raw := range strings.Split(string(content), "\n") {
		if line := strings.TrimSpace(raw); line != "" && !strings.HasPrefix(line, "#") {
			if len(line) > 32 {
				return ""
			}
			return line
		}
	}
	return ""
}

// goModuleForVersionCheck rebuilds the two go.mod lines a toolchain choice is
// judged against from the facts a candidate carries.
func goModuleForVersionCheck(minimum, toolchain string) []byte {
	module := "module candidate\n"
	if minimum != "" {
		module += "go " + minimum + "\n"
	}
	if toolchain != "" {
		module += "toolchain " + toolchain + "\n"
	}
	return []byte(module)
}

// Bounds for the recipe's own scan of a module. The header of a Go file —
// constraint, package clause and imports — is at its top, so only a prefix
// of each file is read, and the whole scan stops at a fixed budget.
const (
	goModuleScanMaxFiles     = 10_000
	goModuleScanHeaderBytes  = 64 << 10
	goModuleScanMaxReadBytes = 32 << 20
)

// scanGoModule is the recipe's read of a module root, bounded, reading file
// headers only. Nested modules — directories with their own go.mod — are
// separate modules and are left out, as the go command leaves them out.
func scanGoModule(root string) (goModulePackages, error) {
	// Files are opened through the root, so nothing the checkout links to
	// outside it is read even if a file is swapped for a link mid-walk.
	contained, err := os.OpenRoot(root)
	if err != nil {
		return goModulePackages{}, fmt.Errorf("%w: %v", ErrUnsupportedBuilder, err)
	}
	defer contained.Close()
	facts := []goSourceFacts{}
	nested := []string{}
	templ, generated := []string{}, map[string]bool{}
	files := 0
	var read int64
	errStop := errors.New("Go source scan exceeded its bound")
	err = filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == "." {
				return nil
			}
			if entry.Name() == ".just-dashboard" || goSkippedDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			if regularExists(current, "go.mod") {
				nested = append(nested, rel)
				return filepath.SkipDir
			}
			return nil
		}
		// A templ component is Go source once `templ generate` has written
		// its _templ.go beside it, which a repository often leaves ignored.
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".templ") && len(templ) < goEmbedsKept {
			templ = append(templ, rel)
		}
		if strings.HasSuffix(entry.Name(), "_templ.go") {
			generated[rel] = true
		}
		// The go command ignores files whose names start with _ or ., as it
		// ignores directories named that way.
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() ||
			!strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") ||
			strings.HasPrefix(entry.Name(), "_") || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		files++
		if files > goModuleScanMaxFiles {
			return errStop
		}
		content, err := readGoHeader(contained, rel)
		if err != nil {
			return err
		}
		read += int64(len(content))
		if read > goModuleScanMaxReadBytes {
			return errStop
		}
		if file, ok := readGoSourceFacts(rel, content); ok {
			facts = append(facts, file)
		}
		return nil
	})
	if err != nil {
		return goModulePackages{}, fmt.Errorf("%w: %v", ErrUnsupportedBuilder, err)
	}
	packages := collectGoModulePackages(facts, nested)
	for _, component := range templ {
		if !generated[strings.TrimSuffix(component, ".templ")+"_templ.go"] && !goDirectorySkipped(path.Dir(component), nested) {
			packages.templMissing = append(packages.templMissing, component)
		}
	}
	return packages, nil
}

func readGoHeader(root *os.Root, rel string) ([]byte, error) {
	file, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, goModuleScanHeaderBytes))
}

// selectGoMainPackage is the package the recipe builds: the configured one,
// which must be a buildable main; with a custom build command, whatever that
// command compiles; otherwise the ranking's choice. A module the ranking
// cannot decide is refused with its main packages named, so the choice is
// made in the plan rather than guessed at build time.
func selectGoMainPackage(packages goModulePackages, config BuildPlanConfig, modulePath string) (string, error) {
	if config.GoPackage != "" {
		chosen := path.Clean(config.GoPackage)
		if !slices.Contains(packages.mains, chosen) {
			return "", fmt.Errorf("%w: the configured Go main package %s is not a buildable package main in this source",
				ErrUnsupportedBuilder, goPackageArgument(chosen))
		}
		return chosen, nil
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" && command != "go build ./..." {
		if len(packages.mains) == 1 {
			return packages.mains[0], nil
		}
		return ".", nil
	}
	if chosen, _ := chooseGoMainPackage(packages.mains, packages.serving, modulePath); chosen != "" {
		return chosen, nil
	}
	if len(packages.mains) == 0 {
		return "", fmt.Errorf("%w: the Go module has no buildable main package; it is a library, or its command lives in a nested module whose directory should be the root", ErrUnsupportedBuilder)
	}
	return "", fmt.Errorf("%w: the Go module has %d main packages (%s); choose the one to build in Build settings",
		ErrUnsupportedBuilder, len(packages.mains), goMainPackageList(packages.mains, 0))
}

// goMainPackageList names packages as the go command takes them — ./cmd/api
// — as many as fit a line of text, then how many more there are, so a
// module of many commands still reads as one bounded sentence. omitted
// counts packages a caller already left out of mains.
func goMainPackageList(mains []string, omitted int) string {
	const bound = 360
	listed := ""
	for index, main := range mains {
		next := goPackageArgument(main)
		if index > 0 {
			next = ", " + next
		}
		if len(listed)+len(next) > bound {
			if listed == "" {
				listed, _ = truncateUTF8(next, bound)
				listed += "..."
				index++
			}
			omitted += len(mains) - index
			break
		}
		listed += next
	}
	if omitted > 0 {
		listed += fmt.Sprintf(" and %d more", omitted)
	}
	return listed
}

func goPackageArgument(pkg string) string {
	if pkg == "." || pkg == "" {
		return "."
	}
	return "./" + pkg
}
