package deploy

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DetectedGoBuild is what detection read about how a Go module builds,
// from the same planGoBuild the recipe runs, so preflight can say before
// Deploy what the build will do and what it will refuse without reading the
// source again. Paths are relative to the candidate's root unless named
// otherwise; nothing here is a variable's value.
type DetectedGoBuild struct {
	// Context is the directory the build context widens to, relative to
	// the checkout ("." for its top): the go.work that uses the module
	// (Workspace), or the ancestor holding its local replace targets.
	Context   string `json:"context,omitempty"`
	Workspace bool   `json:"workspace,omitempty"`
	// LocalReplaces are the local replace targets, relative to the
	// checkout; ReplacesOutside are the ones that leave it.
	LocalReplaces   []string `json:"localReplaces,omitempty"`
	ReplacesOutside []string `json:"replacesOutside,omitempty"`
	// The cgo build: the catalogue modules and local packages that need it,
	// the Alpine packages the build and runtime stages add, and the linked
	// libraries no package is known for.
	CGOModules  []string `json:"cgoModules,omitempty"`
	CGOLocal    []string `json:"cgoLocal,omitempty"`
	CGOPackages []string `json:"cgoPackages,omitempty"`
	CGORuntime  []string `json:"cgoRuntime,omitempty"`
	CGOUnknown  []string `json:"cgoUnknown,omitempty"`
	// Vendored builds from vendor/; SumMissing says go.sum is absent and
	// SumStale names requirements it has no entry for.
	Vendored   bool     `json:"vendored,omitempty"`
	SumMissing bool     `json:"sumMissing,omitempty"`
	SumStale   []string `json:"sumStale,omitempty"`
	// OwnerModules are requirements from the module's own account, which a
	// private repository's install usually needs credentials for.
	OwnerModules []string          `json:"ownerModules,omitempty"`
	Embeds       []DetectedGoEmbed `json:"embeds,omitempty"`
	// Codegen is the generator the recipe runs before compiling, and
	// CodegenMissing the generated files it does not produce.
	Codegen        string   `json:"codegen,omitempty"`
	CodegenMissing []string `json:"codegenMissing,omitempty"`
	// Subcommand is the command-line subcommand that serves, which the
	// detected start command runs.
	Subcommand string `json:"subcommand,omitempty"`
	// WorkGo and WorkToolchain are the go and toolchain lines of the go.work
	// that uses the module, which the toolchain is chosen from as well.
	WorkGo        string `json:"workGo,omitempty"`
	WorkToolchain string `json:"workToolchain,omitempty"`
}

// DetectedGoEmbed is one //go:embed directory: whether the commit has it,
// and the front-end package that builds it when it does not.
type DetectedGoEmbed struct {
	Pattern   string `json:"pattern"`
	Path      string `json:"path"`
	Present   bool   `json:"present,omitempty"`
	Frontend  string `json:"frontend,omitempty"`
	Framework string `json:"framework,omitempty"`
}

// goDetectionSumBytes bounds the go.sum one detection compares across all
// its modules; past it a module's go.sum is left unread, as too large a one
// is, and only the build checks it.
const goDetectionSumBytes = 16 << 20

// applyGoBuildFacts records what the recipe will do with a Go candidate's
// module and marks what it would refuse, which is the source's shape and not
// a setting, so it lowers the candidate the way a dry run's refusal does.
// sums is what is left of the detection's go.sum budget.
func applyGoBuildFacts(checkout string, candidate *DetectedCandidate, packages goModulePackages, marker *detectedMarkers, candidates []DetectedCandidate, sums *int64) {
	if marker == nil || len(marker.goModContent) == 0 {
		return
	}
	moduleRoot := filepath.Join(checkout, filepath.FromSlash(candidate.Root))
	config := BuildPlanConfig{Method: BuildRecipe, Recipe: "go", BuildCommand: candidate.BuildCommand}
	plan, refusal := planGoBuild(checkout, moduleRoot, marker.goModContent, packages, goEmbedsMain(candidate.GoPackage, nil, config), config, *sums)
	*sums -= plan.sumBytes
	facts := &DetectedGoBuild{
		Context: plan.context.dir, Workspace: plan.context.workspace, LocalReplaces: compiledFactList(plan.context.replaces),
		CGOModules: plan.cgo.modules, CGOLocal: compiledFactList(plan.cgo.local), CGORuntime: plan.cgo.runtime,
		CGOUnknown: compiledFactList(plan.cgo.unknown), Vendored: plan.vendored, SumMissing: plan.sumAbsent,
		SumStale: compiledFactList(plan.sumStale), OwnerModules: compiledFactList(plan.ownerModules), Codegen: plan.templ,
	}
	if plan.cgo.enabled() {
		facts.CGOPackages = plan.cgo.build
	}
	for _, replacement := range plan.module.replaces {
		if _, err := goReplaceTarget(candidate.Root, replacement); replacement.local() && err != nil && len(facts.ReplacesOutside) < 16 {
			facts.ReplacesOutside = append(facts.ReplacesOutside, replacement.target)
		}
	}
	for _, embed := range plan.embeds {
		if len(facts.Embeds) >= 16 {
			break
		}
		recorded := DetectedGoEmbed{Pattern: embed.pattern, Path: embed.dir, Present: embed.present, Frontend: embed.frontend}
		if plan.frontend != nil && embed.frontend != "" {
			recorded.Framework = plan.frontend.framework
		}
		facts.Embeds = append(facts.Embeds, recorded)
	}
	facts.CodegenMissing = goCodegenMissing(moduleRoot, plan)
	candidate.Go = facts
	if work := plan.context.work; plan.context.workspace && work.goVersion+work.toolchain != "" &&
		goVersionLineRE.MatchString(work.goVersion) && goVersionLineRE.MatchString(work.toolchain) {
		// The go.work's lines choose the toolchain too, as the recipe reads
		// them (goVersionInputs).
		facts.WorkGo, facts.WorkToolchain = work.goVersion, work.toolchain
		if version, err := chooseGoRecipeVersion("", candidate.GoVersionFile, goCandidateVersionModule(candidate)); err == nil {
			candidate.GoVersion = version
		}
	}
	if refusal != nil && candidate.RecipeIssue == "" {
		candidate.RecipeIssue = recipeRefusalText(refusal, checkout)
		if candidate.readingConfidence == "" {
			candidate.readingConfidence = candidate.Confidence
		}
		candidate.Confidence = ConfidenceLow
	}
	goBuildEvidence(candidate, plan)
	for _, embed := range plan.embeds {
		if plan.frontend == nil || embed.present || embed.frontend == "" {
			continue
		}
		frontendRoot := strings.TrimPrefix(path.Join(candidate.Root, embed.frontend), "/")
		if frontendRoot == "." {
			frontendRoot = ""
		}
		for index := range candidates {
			other := &candidates[index]
			if other.Root == frontendRoot && other.Recipe == "node" && other.ID != candidate.ID {
				// The site is part of the server's image, not a second
				// application beside it: selecting it would ship the page
				// without the API it calls.
				other.Confidence = ConfidenceLow
				if other.Demotion == "" {
					other.Demotion = boundedText("its build is embedded in the Go server in "+rootLabelOf(candidate.Root), 512)
				}
			}
		}
	}
	if sub := goServeSubcommand(moduleRoot, plan.module); sub != "" && candidate.StartCommand == "" && candidate.NotDeployable == "" {
		facts.Subcommand = sub
		candidate.StartCommand = "/app " + sub
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "go.mod"),
			Reason: "the command line's " + sub + " subcommand serves; the binary alone prints its help"})
	}
}

// goVersionLineRE bounds a go.work's go and toolchain values as a candidate
// records them; empty is the line's absence.
var goVersionLineRE = regexp.MustCompile(`^[A-Za-z0-9._+-]{0,32}$`)

// goBuildEvidence says on the candidate what the recipe will do beyond
// compiling the main package.
func goBuildEvidence(candidate *DetectedCandidate, plan goBuildPlan) {
	module := joinRoot(candidate.Root, "go.mod")
	add := func(path, reason string) {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: path, Reason: detectionLine(reason)})
	}
	if plan.context.workspace {
		start := plan.context.dir
		if start == "" {
			start = rootLabelOf(candidate.Root)
		}
		add(module, "a go.work uses this module: the build starts in "+start)
	} else if plan.context.replaceOnly {
		add(module, "local replacements "+strings.Join(plan.context.replaces, ", ")+": the build starts in "+plan.context.dir)
	}
	if plan.cgo.enabled() {
		add(module, "builds with cgo: "+plan.cgo.describe())
	}
	switch {
	case plan.vendored:
		add(joinRoot(candidate.Root, "vendor/modules.txt"), "vendored dependencies (vendor/modules.txt)")
	case plan.sumAbsent:
		add(module, "no go.sum is committed")
	case len(plan.sumStale) > 0:
		add(joinRoot(candidate.Root, "go.sum"), "go.sum has no entry for "+strings.Join(boundedNames(plan.sumStale), ", "))
	}
	if plan.templ != "" {
		add(module, "templ components without generated code; the build runs "+plan.templ+" ("+plan.templFrom+")")
	}
	for _, embed := range plan.embeds {
		if !embed.present && embed.frontend != "" {
			add(joinRoot(candidate.Root, embed.frontend), "//go:embed "+embed.pattern+" is built from "+rootLabelOf(embed.frontend)+" before the Go build")
		}
	}
}

// compiledFactList keeps the first 64 entries of a list a Go or Rust
// candidate records.
func compiledFactList(values []string) []string {
	if len(values) > 64 {
		return values[:64]
	}
	return values
}

// Code the module generates before it compiles, which the recipe does not
// produce: the CSS a Tailwind CLI command writes, and sqlc's generated Go.
var (
	tailwindOutputRE = regexp.MustCompile(`tailwindcss\b[^\n]*?(?:-o|--output)[ =]+['"]?([\w./-]+\.css)`)
	sqlcOutputRE     = regexp.MustCompile(`(?m)^\s*"?out"?\s*:\s*["']?([\w./-]+)["']?`)
)

func goCodegenMissing(moduleRoot string, plan goBuildPlan) []string {
	missing := []string{}
	exists := func(rel string) bool {
		rel = path.Clean(strings.TrimPrefix(rel, "./"))
		if rel == "." || strings.HasPrefix(rel, "..") {
			return true
		}
		_, err := os.Lstat(filepath.Join(moduleRoot, filepath.FromSlash(rel)))
		return err == nil
	}
	for _, name := range []string{"Makefile", "makefile", "Taskfile.yml", "Taskfile.yaml", "justfile", "package.json"} {
		content, err := readContainedRegular(moduleRoot, name, 64<<10)
		if err != nil {
			continue
		}
		for _, match := range tailwindOutputRE.FindAllSubmatch(content, 4) {
			if output := string(match[1]); !exists(output) && !slices.Contains(missing, output) {
				missing = append(missing, output)
			}
		}
	}
	for _, name := range []string{"sqlc.yaml", "sqlc.yml", "sqlc.json"} {
		content, err := readContainedRegular(moduleRoot, name, 64<<10)
		if err != nil {
			continue
		}
		for _, match := range sqlcOutputRE.FindAllSubmatch(content, 8) {
			if output := string(match[1]); !exists(output) && !slices.Contains(missing, output) {
				missing = append(missing, output)
			}
		}
	}
	if len(missing) > 8 {
		missing = missing[:8]
	}
	return missing
}

// Subcommands a command-line application serves from, in the order one is
// preferred when it defines several.
var (
	goServeSubcommands = []string{"serve", "server", "start"}
	// A Use line is the command's name and then its usage: `serve [flags]`
	// is serve, `server-status` is another command.
	goCobraUseRE   = regexp.MustCompile(`Use:\s*"(serve|server|start)["\s]`)
	goCobraRunRE   = regexp.MustCompile(`\.Execute(?:C|Context|ContextC)?\(`)
	goUrfaveNameRE = regexp.MustCompile(`Name:\s*"(serve|server|start)"`)
)

// goServeSubcommand finds the serve subcommand of a cobra or urfave/cli
// application. Its binary run bare prints help and exits, so the release
// never answers; the source files are read as text, bounded.
func goServeSubcommand(moduleRoot string, module goModFile) string {
	cobra, urfave := module.requirement("github.com/spf13/cobra") != "", module.requirement("github.com/urfave/cli") != ""
	if !cobra && !urfave {
		return ""
	}
	found, runs := map[string]bool{}, false
	files, read := 0, int64(0)
	errStop := errors.New("stop")
	_ = filepath.WalkDir(moduleRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if current != moduleRoot && (goSkippedDirectory(entry.Name()) || regularExists(current, "go.mod")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		if files++; files > 400 || read > 4<<20 {
			return errStop
		}
		content, _, err := readDetectionFile(current, 256<<10)
		if err != nil {
			return nil
		}
		read += int64(len(content))
		if cobra && strings.Contains(string(content), "cobra.Command") {
			for _, match := range goCobraUseRE.FindAllSubmatch(content, 8) {
				found[string(match[1])] = true
			}
		}
		// A cobra command tree answers only when main executes it.
		runs = runs || (cobra && goCobraRunRE.Match(content))
		if urfave && strings.Contains(string(content), "cli.Command") {
			for _, match := range goUrfaveNameRE.FindAllSubmatch(content, 8) {
				found["urfave:"+string(match[1])] = true
			}
		}
		return nil
	})
	for _, name := range goServeSubcommands {
		if (found[name] && runs) || found["urfave:"+name] {
			return name
		}
	}
	return ""
}

// validateDetectedGoBuild bounds what a saved candidate carries about its
// Go build, like every other detected field.
func validateDetectedGoBuild(candidate DetectedCandidate) error {
	facts := candidate.Go
	if facts == nil {
		return nil
	}
	malformed := fmt.Errorf("%w: detected Go build is malformed", ErrInvalidPlan)
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") && rejectPlanSecretLiteral("detected Go build", value) == nil
	}
	lists := [][]string{facts.LocalReplaces, facts.ReplacesOutside, facts.CGOModules, facts.CGOLocal, facts.CGOPackages,
		facts.CGORuntime, facts.CGOUnknown, facts.SumStale, facts.OwnerModules, facts.CodegenMissing}
	for _, list := range lists {
		if len(list) > 64 {
			return malformed
		}
		for _, value := range list {
			if !text(value, 512) {
				return malformed
			}
		}
	}
	if !text(facts.Context, 4096) || (facts.Context != "" && facts.Context != "." && !safeRelativePath(facts.Context)) ||
		!text(facts.Codegen, 512) || len(facts.Embeds) > 16 || !slices.Contains(append([]string{""}, goServeSubcommands...), facts.Subcommand) ||
		!goVersionLineRE.MatchString(facts.WorkGo) || !goVersionLineRE.MatchString(facts.WorkToolchain) {
		return malformed
	}
	for _, embed := range facts.Embeds {
		if !text(embed.Pattern, 512) || !text(embed.Path, 4096) || !text(embed.Frontend, 4096) || !text(embed.Framework, 128) {
			return malformed
		}
	}
	return nil
}
