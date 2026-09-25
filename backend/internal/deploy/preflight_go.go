package deploy

import (
	"slices"
	"strings"
)

// compiledBuildFindings are what the Go and Rust recipes will do with the
// planned candidate, and what they would refuse, judged from detection's
// facts against the plan so each shows before Deploy.
func compiledBuildFindings(candidate *DetectedCandidate, configuration PlanConfiguration, source *DraftSourceConfig) []PreflightFinding {
	build := configuration.Build
	if candidate == nil || build.Method != BuildRecipe {
		return nil
	}
	recipe := build.Recipe
	if recipe == "" {
		recipe = candidate.Recipe
	}
	switch recipe {
	case "go":
		return goBuildFindings(candidate, configuration, source)
	case "rust":
		return rustBuildFindings(candidate, configuration)
	}
	return nil
}

// refusalNamed says a finding already names the recipe's refusal precisely,
// so the candidate's RecipeIssue is not relayed a second time.
func refusalNamed(findings []PreflightFinding) bool {
	return slices.ContainsFunc(findings, func(item PreflightFinding) bool {
		return (item.Severity == PreflightBlocked || item.Severity == PreflightDecision) && buildRefusalCodes[item.Code]
	})
}

// unpinnedDependenciesAction is how each ecosystem commits the lockfile
// that pins what a rebuild installs.
func unpinnedDependenciesAction(recipe string) string {
	const works = " when rebuilds must be identical; deploying as is works today."
	switch recipe {
	case "rust":
		return "Run cargo generate-lockfile and commit Cargo.lock" + works
	case "php":
		return "Run composer update --lock and commit composer.lock" + works
	case "deno":
		return "Run deno install and commit deno.lock" + works
	}
	return "Commit a lockfile (uv lock, poetry lock, or pip freeze > requirements.txt)" + works
}

func goBuildFindings(candidate *DetectedCandidate, configuration PlanConfiguration, source *DraftSourceConfig) []PreflightFinding {
	build := configuration.Build
	findings := []PreflightFinding{}
	add := func(code string, severity PreflightSeverity, title, measured, means, action, field string) {
		findings = append(findings, finding(code, severity, title, boundedFindingText(measured), means, action, "deploy", field))
	}
	if choice, err := resolveGoRecipeVersion(build.GoVersion, candidate.GoVersionFile, goCandidateVersionModule(candidate)); err == nil {
		switch {
		case choice.eol:
			add("go_version_eol", PreflightWarning, "Go "+choice.version+" no longer receives security fixes",
				"Go "+choice.version+" from "+choice.source,
				"Go supports its two newest releases; this build compiles the standard library's net/http and crypto/tls without the fixes published since.",
				"Build with Go "+defaultGoRecipeVersion+": change Go version in Build settings, or update .go-version.", "configuration.build.goVersion")
		case choice.downgraded != "":
			add("go_toolchain_downgraded", PreflightWarning, "go.mod prefers a newer Go than the recipe builds with",
				"toolchain "+choice.downgraded+"; building with Go "+choice.version,
				"The toolchain line is a preference, and the module's go line is satisfied, so the newest Go the recipe has builds it; a module that really needs the newer release fails with \"requires go >=\".",
				"Nothing, unless the build fails; then lower the toolchain line, or build from a Dockerfile.", "configuration.build.goVersion")
		default:
			if note := choice.note(); note != "" {
				add("go_version_family", PreflightPass, "Go toolchain chosen from go.mod", note,
					"go.mod's go line is a minimum, so the build uses the family's maintained patch rather than the release it names.", "", "configuration.build.goVersion")
			}
		}
	}
	facts := candidate.Go
	if facts == nil {
		return findings
	}
	custom := strings.TrimSpace(build.BuildCommand) != "" && strings.TrimSpace(build.BuildCommand) != "go build ./..."
	if len(facts.ReplacesOutside) > 0 {
		action := "Move the replaced module into this repository, or replace it with a published version."
		if source != nil && source.Subdirectory != "" {
			action = "Deploy from a subdirectory that contains the replaced module too, move it into " + source.Subdirectory + ", or replace it with a published version."
		}
		add("go_local_replace_outside_root", PreflightBlocked, "go.mod replaces a module with a directory the build does not have",
			strings.Join(facts.ReplacesOutside, ", "),
			"The build context is the checkout; a replace pointing above it resolves to nothing, and go mod download stops with \"replacement directory does not exist\".",
			action, "configuration.build.rootDirectory")
	} else if facts.Context != "" {
		reason := "the module's local replacements (" + strings.Join(facts.LocalReplaces, ", ") + ")"
		if facts.Workspace {
			reason = "the go.work that uses this module"
		}
		add("go_build_context", PreflightPass, "The build starts where the module's dependencies are", facts.Context,
			"The build context widens to "+facts.Context+", which holds "+reason+"; the module still builds from "+rootLabelOf(candidate.Root)+".", "", "configuration.build.rootDirectory")
	}
	if len(facts.CGOUnknown) > 0 {
		add("go_cgo_library_unknown", PreflightBlocked, "cgo links a library the recipe cannot install",
			goCGOPlan{unknown: facts.CGOUnknown}.unknownText(),
			"The recipe installs Alpine packages for the libraries it knows; this one would stop the build at the link.",
			"Build from a Dockerfile that installs it, or drop the cgo dependency.", "configuration.build")
	} else if len(facts.CGOModules) > 0 || len(facts.CGOLocal) > 0 {
		plan := goCGOPlan{modules: facts.CGOModules, local: facts.CGOLocal, runtime: facts.CGORuntime}
		means := "CGO_ENABLED=0 would compile a stub or drop the C code; the build installs " + strings.Join(facts.CGOPackages, " ") + " and links the binary statically."
		if plan.dynamic() {
			means = "CGO_ENABLED=0 would drop the C code; the build installs " + strings.Join(facts.CGOPackages, " ") + ", links " +
				strings.Join(facts.CGORuntime, ", ") + " dynamically, and runs on alpine " + compiledDynamicAlpine + " with them installed."
		}
		action := ""
		for _, known := range goCGOModules {
			if slices.Contains(facts.CGOModules, known.module) && known.alternative != "" {
				action = "Nothing to do. To build without a C toolchain, switch " + known.module + " to " + known.alternative + "."
				break
			}
		}
		add("go_cgo_enabled", PreflightWarning, "The build compiles C code with cgo", plan.describe(), means, action, "configuration.build")
	}
	switch {
	case facts.Vendored:
		add("go_vendored", PreflightPass, "Dependencies are vendored", "vendor/modules.txt",
			"The build compiles from vendor/ with -mod=vendor and downloads nothing, so modules only this repository can reach still build.", "", "configuration.build")
	case facts.SumMissing:
		add("go_sum_missing", PreflightWarning, "go.sum is not committed", "go.mod requires modules and there is no go.sum",
			"The go command refuses to build without checksums (\"missing go.sum entry\"), so the build runs with -mod=mod and records the checksums of whatever it downloads.",
			"Run go mod tidy and commit go.sum, so the build verifies the same modules every time.", "configuration.build")
	case len(facts.SumStale) > 0:
		add("lockfile_out_of_sync", PreflightWarning, "go.sum is out of sync with go.mod", "no entry for "+strings.Join(boundedNames(facts.SumStale), ", "),
			"A build that only reads go.sum stops at \"missing go.sum entry\", so the build runs with -mod=mod and records the missing checksums.",
			"Run go mod tidy and commit go.sum.", "configuration.build")
	}
	if len(facts.OwnerModules) > 0 && !facts.Vendored && source != nil && (source.CredentialID != 0 || source.Mode == SourceModeConnectedRepository) {
		token := slices.ContainsFunc(build.Secrets, func(secret BuildSecretConfig) bool {
			return secret.Variable == goPrivateTokenVariable && buildSecretReaches(secret.Step, "install")
		})
		if token {
			add("go_private_module", PreflightPass, "Modules from the source's account are fetched with "+goPrivateTokenVariable,
				strings.Join(boundedNames(facts.OwnerModules), ", "),
				"The install sets GOPRIVATE and fetches them directly from their Git host with the token, which only the install step's secret mount holds.", "", "variables."+goPrivateTokenVariable)
		} else {
			add("go_private_module", PreflightWarning, "The build may not be able to fetch these modules", strings.Join(boundedNames(facts.OwnerModules), ", "),
				"The source is fetched with a credential, and modules from the same account are usually private too; the build fetches modules anonymously through proxy.golang.org, which cannot see private ones.",
				"If they are private, add a build variable "+goPrivateTokenVariable+" holding a token that can read them, mapped to the install step; the recipe then fetches them directly with it.", "variables")
		}
	}
	for _, embed := range facts.Embeds {
		switch {
		case embed.Present:
		case embed.Frontend == "" && !custom:
			add("go_embed_missing", PreflightBlocked, "The Go build embeds files this commit does not have", "//go:embed "+embed.Pattern+" ("+embed.Path+")",
				"The directory is missing or holds only dotfiles, and no package.json in the module builds it, so go build stops with \"no matching files found\".",
				"Commit the files, add the front-end package that builds them, or set a build command that produces them.", "configuration.build.buildCommand")
		case embed.Frontend != "":
			label := "its package.json build"
			if embed.Framework != "" {
				label = embed.Framework
			}
			add("go_embed_frontend", PreflightPass, "The embedded front end is built first", rootLabelOf(embed.Frontend)+" → "+embed.Path,
				"//go:embed "+embed.Pattern+" names a directory the commit does not have; a Node stage builds it with "+label+" from its lockfile and copies it in before go build.", "", "configuration.build")
		}
	}
	if facts.Codegen != "" && !custom {
		add("go_codegen", PreflightPass, "templ components are generated before the build", facts.Codegen,
			"The generated _templ.go files are not committed, so the build generates them first.", "", "configuration.build")
	}
	if len(facts.CodegenMissing) > 0 {
		add("go_codegen_missing", PreflightWarning, "Generated files the build does not produce are missing", strings.Join(facts.CodegenMissing, ", "),
			"The repository's own scripts write them (a Tailwind CLI command, sqlc generate), and the automatic build does not run those, so the service starts without them.",
			"Commit the generated files, or set a build command that generates them before go build.", "configuration.build.buildCommand")
	}
	if facts.Subcommand != "" && strings.TrimSpace(build.StartCommand) == "/app "+facts.Subcommand {
		add("go_start_subcommand", PreflightWarning, "The start command runs the "+facts.Subcommand+" subcommand", build.StartCommand,
			"The binary is a command-line application; run bare it prints its help and exits, so detection starts the subcommand its source defines.",
			"Confirm the command serves the application, and add the flags it needs (a port, a data directory).", "configuration.build.startCommand")
	}
	return findings
}
