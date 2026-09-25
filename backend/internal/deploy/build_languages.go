package deploy

import (
	"context"
	"fmt"
)

// languageBuild is what the Ruby, Elixir, Scala, Clojure, Dart and Gleam
// recipes prepared, one field per language.
type languageBuild struct {
	ruby   rubyRecipe
	elixir elixirRecipe
	jvm    jvmLanguageRecipe
	dart   dartRecipe
	gleam  gleamRecipe
}

func selectLanguageRecipe(root, kind string, config BuildPlanConfig) (selectedRecipe, error) {
	recipe := selectedRecipe{kind: kind, catalogueKey: kind}
	var err error
	switch kind {
	case "ruby":
		recipe.language.ruby, err = selectRubyRecipe(root, config)
		if node := recipe.language.ruby.node; node != nil {
			recipe.nodeInputs = node.inputs
		}
	case "elixir":
		recipe.language.elixir, err = selectElixirRecipe(root, config)
		if node := recipe.language.elixir.node; node != nil {
			recipe.nodeInputs = node.inputs
		}
	case "scala", "clojure":
		recipe.language.jvm, err = selectJVMLanguageRecipe(root, kind)
	case "dart":
		recipe.language.dart, err = selectDartRecipe(root, config)
	case "gleam":
		recipe.language.gleam, err = selectGleamRecipe(root)
	default:
		return selectedRecipe{}, fmt.Errorf("%w: no supported automatic recipe was selected", ErrUnsupportedBuilder)
	}
	return recipe, err
}

// prepareLanguageRecipe settles the images a language recipe builds on,
// records its toolchain and install for the build evidence, and returns the
// references to resolve in the order the Dockerfile names them.
func (b *ArtifactBuilder) prepareLanguageRecipe(ctx context.Context, recipe *selectedRecipe, prepared *PreparedBuild) []string {
	language := &recipe.language
	var node *languageNodeAssets
	var bases []string
	switch recipe.kind {
	case "ruby":
		prepared.Notes = append(prepared.Notes, b.settleRubyImage(ctx, &language.ruby)...)
		prepared.Toolchain = "ruby " + language.ruby.version.release()
		if language.ruby.addPlatform != "" {
			prepared.Notes = append(prepared.Notes, "Gemfile.lock lists no platform for this server; the build adds "+language.ruby.addPlatform+" before installing frozen")
		}
		node, bases = language.ruby.node, rubyRecipeBases(language.ruby)
	case "elixir":
		prepared.Notes = append(prepared.Notes, b.settleElixirImage(ctx, &language.elixir)...)
		prepared.Toolchain = "elixir " + language.elixir.version.release()
		node, bases = language.elixir.node, elixirRecipeBases(language.elixir)
	case "scala", "clojure":
		prepared.Toolchain = language.jvm.toolchain()
		bases = language.jvm.bases()
	case "dart":
		prepared.Toolchain = "dart " + language.dart.version
		if language.dart.frog {
			prepared.Toolchain += " (dart_frog_cli " + dartFrogCLIVersion + ")"
		}
		bases = language.dart.bases()
	case "gleam":
		prepared.Toolchain = "gleam " + gleamRecipeVersion
		bases = []string{gleamImage}
	}
	if node != nil && !node.runtimeOnly {
		b.settleBunImage(ctx, &node.plan)
		prepared.Toolchain += " · assets: " + node.plan.toolchain
		prepared.NodeVersion = node.plan.node.label()
		prepared.Install = node.plan.installLine()
		prepared.Notes = append(prepared.Notes, node.plan.notes...)
		if recipe.kind == "ruby" {
			bases = rubyRecipeBases(language.ruby)
		} else {
			bases = elixirRecipeBases(language.elixir)
		}
	}
	return bases
}

func renderLanguageDockerfile(recipe selectedRecipe, config BuildPlanConfig, bases []ResolvedImage, installSecrets, buildSecrets string) ([]string, error) {
	switch recipe.kind {
	case "ruby":
		return renderRubyDockerfile(recipe.language.ruby, config, bases, installSecrets, buildSecrets)
	case "elixir":
		return renderElixirDockerfile(recipe.language.elixir, config, bases, installSecrets, buildSecrets)
	case "scala", "clojure":
		return renderJVMLanguageDockerfile(recipe.language.jvm, config, bases, installSecrets, buildSecrets)
	case "dart":
		return renderDartDockerfile(recipe.language.dart, config, bases, installSecrets, buildSecrets)
	case "gleam":
		return renderGleamDockerfile(recipe.language.gleam, config, bases, installSecrets, buildSecrets)
	}
	return nil, ErrUnsupportedBuilder
}
