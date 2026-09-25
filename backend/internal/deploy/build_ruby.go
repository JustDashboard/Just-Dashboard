package deploy

import (
	"context"
	"strings"
)

// rubyBundle is where Bundler installs and finds the gems, in the build and
// the runtime alike; deployment mode would otherwise put them in
// vendor/bundle.
const rubyBundle = "BUNDLE_PATH=/usr/local/bundle BUNDLE_WITHOUT=development:test"

// settleRubyImage keeps an exact Ruby pin buildable: the official image has
// no tag for a few releases (3.4.0 among them), and then the family's
// newest release builds instead, which Bundler refuses only when the
// Gemfile demands that exact release.
func (b *ArtifactBuilder) settleRubyImage(ctx context.Context, recipe *rubyRecipe) []string {
	if recipe.version.exact == "" {
		return nil
	}
	if _, err := b.backend.ResolveImage(ctx, recipe.version.image(), ""); err == nil {
		return nil
	}
	exact := recipe.version.exact
	recipe.version.exact = ""
	return []string{"Ruby " + exact + " has no " + "ruby:" + exact + "-slim image; building on " + recipe.version.image()}
}

// renderRubyDockerfile renders the build stage — system headers for the
// locked gems, Node borrowed for an asset pipeline, the frozen bundle, the
// JavaScript install and the precompile — and a runtime stage with the
// bundle, the application and only the libraries the gems load.
func renderRubyDockerfile(recipe rubyRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	ruby, err := resolveCatalogueImage(bases, recipe.version.image())
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
	environment := strings.Join(rubyEnvironment(recipe.framework), " ") + " " + rubyBundle
	lines = append(lines,
		"FROM "+immutableImageReference(ruby)+" AS build",
		"WORKDIR /app",
		"ENV "+environment,
		debianPackagesLine(recipe.buildPackages),
	)
	if recipe.node != nil {
		lines = append(lines, recipe.node.copyLines()...)
	}
	lines = append(lines, "COPY . .")
	deployment := ""
	if recipe.locked {
		deployment = "BUNDLE_DEPLOYMENT=1 "
		if recipe.addPlatform != "" {
			// A lock resolved on a Mac, or on the other architecture, has no
			// platform for this server, and the frozen install refuses it.
			lines = append(lines, "RUN "+installSecrets+"bundle lock --add-platform "+recipe.addPlatform)
		}
	}
	lines = append(lines, "RUN "+installSecrets+deployment+"bundle install && rm -rf ~/.bundle /usr/local/bundle/ruby/*/cache /usr/local/bundle/ruby/*/bundler/gems/*/.git")
	if recipe.locked {
		lines = append(lines, "ENV BUNDLE_DEPLOYMENT=1")
	}
	if recipe.node != nil {
		lines = append(lines, recipe.node.installLines(installSecrets)...)
	}
	if recipe.precompile != "" {
		lines = append(lines, "RUN "+buildSecrets+recipe.precompile)
	}
	if command := strings.TrimSpace(config.BuildCommand); command != "" {
		lines = append(lines, "RUN "+buildSecrets+command)
	}
	// The runtime image has no Node, and a pid file left in the tree would
	// make every container's server think another one is running.
	lines = append(lines, "RUN rm -rf node_modules tmp/cache tmp/pids")
	lines = append(lines, "FROM "+immutableImageReference(ruby), "WORKDIR /app")
	if len(recipe.packages) > 0 {
		lines = append(lines, debianPackagesLine(recipe.packages))
	}
	if recipe.locked {
		environment += " BUNDLE_DEPLOYMENT=1"
	}
	lines = append(lines,
		"ENV "+environment,
		"COPY --from=build /usr/local/bundle /usr/local/bundle",
		"COPY --from=build /app /app",
		shellCMD(config.StartCommand),
	)
	return lines, nil
}
