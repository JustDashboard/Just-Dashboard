package deploy

import (
	"context"
	"strings"
)

// settleElixirImage keeps an exact Elixir pin buildable when the official
// image has no tag for that release and OTP: the family's newest release
// on the same OTP builds instead.
func (b *ArtifactBuilder) settleElixirImage(ctx context.Context, recipe *elixirRecipe) []string {
	if recipe.version.exact == "" {
		return nil
	}
	if _, err := b.backend.ResolveImage(ctx, recipe.version.image(), ""); err == nil {
		return nil
	}
	exact := recipe.version.image()
	recipe.version.exact = ""
	return []string{"there is no " + exact + " image; building on " + recipe.version.image()}
}

// renderElixirDockerfile renders the stages phx.gen.release writes — deps,
// asset tooling, compile, assets, release — with the dependency fetch under
// the install secret mount, then runs the release on the image that built
// it as an unprivileged user.
func renderElixirDockerfile(recipe elixirRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	elixir, err := resolveCatalogueImage(bases, recipe.version.image())
	if err != nil {
		return nil, err
	}
	var lines []string
	if recipe.node != nil {
		stage, err := recipe.node.toolchainStage(bases)
		if err != nil {
			return nil, err
		}
		lines = append(lines, stage...)
	}
	lines = append(lines,
		"FROM "+immutableImageReference(elixir)+" AS build",
		// The slim image has no CA certificates, without which Hex cannot
		// reach its repository.
		debianPackagesLine([]string{"build-essential", "ca-certificates", "git"}),
		"WORKDIR /app",
		"ENV MIX_ENV=prod LANG=C.UTF-8",
		"RUN mix local.hex --force && mix local.rebar --force",
	)
	if recipe.node != nil {
		lines = append(lines, recipe.node.copyLines()...)
	}
	lines = append(lines, "COPY . .")
	fetch := "mix deps.get --only prod"
	if recipe.locked {
		// A mix.lock that no longer matches mix.exs is refused rather than
		// re-resolved, like every other recipe's lockfile.
		fetch += " --check-locked"
	}
	lines = append(lines, "RUN "+installSecrets+fetch, "RUN mix deps.compile")
	if recipe.aliases["assets.setup"] {
		lines = append(lines, "RUN "+installSecrets+"mix assets.setup")
	}
	// An umbrella's asset aliases belong to the application that defines
	// them and run from its directory.
	for _, app := range recipe.assetApps {
		if app.aliases["assets.setup"] {
			lines = append(lines, "RUN "+installSecrets+"cd "+app.dir+" && mix assets.setup")
		}
	}
	if recipe.node != nil {
		lines = append(lines, recipe.node.installLines(installSecrets)...)
	}
	lines = append(lines, "RUN "+buildSecrets+"mix compile")
	if recipe.aliases["assets.deploy"] {
		lines = append(lines, "RUN "+buildSecrets+"mix assets.deploy")
	}
	for _, app := range recipe.assetApps {
		if app.aliases["assets.deploy"] {
			lines = append(lines, "RUN "+buildSecrets+"cd "+app.dir+" && mix assets.deploy")
		}
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	release := "mix release --overwrite"
	if recipe.releaseNamed {
		release = "mix release " + recipe.release + " --overwrite"
	}
	lines = append(lines,
		"RUN "+buildSecrets+release,
		"RUN test -x _build/prod/rel/"+recipe.release+"/bin/"+recipe.release+" || (echo 'mix release must write _build/prod/rel/"+recipe.release+"' >&2; exit 1)",
		"FROM "+immutableImageReference(elixir),
		debianPackagesLine([]string{"ca-certificates"}),
		unprivilegedDebianUser,
		"WORKDIR /app",
	)
	environment := "ENV MIX_ENV=prod LANG=C.UTF-8"
	if recipe.phoenix {
		// A release starts Phoenix's endpoint only when told to, which is
		// what phx.gen.release's bin/server sets.
		environment += " PHX_SERVER=true"
	}
	lines = append(lines, environment,
		"COPY --from=build --chown=10001:10001 /app/_build/prod/rel/"+recipe.release+"/ /app/",
		"USER 10001",
	)
	start := strings.TrimSpace(config.StartCommand)
	if start == "" {
		start = "/app/bin/" + recipe.release + " start"
	}
	return append(lines, shellCMD(start)), nil
}
