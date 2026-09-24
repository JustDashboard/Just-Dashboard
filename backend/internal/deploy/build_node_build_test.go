package deploy

import (
	"context"
	"strings"
	"testing"
)

const t3Env = `import { createEnv } from "@t3-oss/env-nextjs";
import { z } from "zod";

export const env = createEnv({
  server: {
    AUTH_SECRET:
      process.env.NODE_ENV === "production" ? z.string() : z.string().optional(),
    AUTH_DISCORD_ID: z.string(),
    DATABASE_URL: z.string().url(),
    NODE_ENV: z.enum(["development", "test", "production"]).default("development"),
    RATE: z.coerce.number().refine((n) => n > 0, { message: "positive, {braces}" }),
  },
  client: {
    NEXT_PUBLIC_POSTHOG_KEY: z.string(),
    NEXT_PUBLIC_FLAG: z.string().optional(),
  },
  runtimeEnv: {
    AUTH_SECRET: process.env.AUTH_SECRET,
  },
  skipValidation: !!process.env.SKIP_ENV_VALIDATION,
  emptyStringAsUndefined: true,
});
`

// The build command's RUN: V8's own heap, the legacy OpenSSL provider a
// webpack 4 toolchain cannot build without, SKIP_ENV_VALIDATION when the
// schema's server variables have no build value, and the env files a
// command names created empty.
func TestNodeRecipeGivesTheBuildWhatItsToolchainNeeds(t *testing.T) {
	t.Parallel()
	t3 := `{"name":"t3","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15.0.0","@t3-oss/env-nextjs":"^0.12.0","zod":"^3.24.0"}}`
	for _, test := range []struct {
		name     string
		files    map[string]string
		config   BuildPlanConfig
		bound    []string
		want     []string
		absent   []string
		findings []string
	}{
		{name: "Create React App 4 builds with the legacy provider", files: map[string]string{
			"package.json": `{"name":"cra","scripts":{"build":"react-scripts build"},"dependencies":{"react-scripts":"4.0.3"}}`},
			config:   BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "build"},
			want:     []string{`RUN export NODE_OPTIONS="--openssl-legacy-provider${NODE_OPTIONS:+ $NODE_OPTIONS}" && npm run build` + "\n"},
			findings: []string{"legacy_openssl_provider"}},
		{name: "a webpack 4 another dependency pulls in is not the build's toolchain", files: map[string]string{
			"package.json":      `{"name":"app","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.1.3"},"devDependencies":{"@storybook/react":"6.5.16"}}`,
			"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"next":"16.1.3"},"devDependencies":{"@storybook/react":"6.5.16"}},"node_modules/next":{"version":"16.1.3"},"node_modules/@storybook/react":{"version":"6.5.16"},"node_modules/webpack":{"version":"4.47.0"}}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"}, want: []string{nodeBuildRun("npm run build\n")}, absent: []string{"legacy", "NODE_OPTIONS"}},
		{name: "a stale lockfile's old webpack is not what installs", files: map[string]string{
			"package.json":      `{"name":"app","scripts":{"build":"webpack"},"devDependencies":{"webpack":"^5.90.0"}}`,
			"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"webpack":"^5.38.0"}},"node_modules/webpack":{"version":"5.38.1"}}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "dist"}, absent: []string{"legacy"}},
		{name: "a locked webpack before 5.61", files: map[string]string{
			"package.json":      `{"name":"app","scripts":{"build":"webpack"},"devDependencies":{"webpack":"^5.38.0"}}`,
			"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"webpack":"^5.38.0"}},"node_modules/webpack":{"version":"5.38.1"}}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "dist"}, want: []string{"--openssl-legacy-provider"}, findings: []string{"legacy_openssl_provider"}},
		{name: "a locked webpack 5.61 or later", files: map[string]string{
			"package.json":      `{"name":"app","scripts":{"build":"webpack"},"devDependencies":{"webpack":"^5.38.0"}}`,
			"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"webpack":"^5.38.0"}},"node_modules/webpack":{"version":"5.97.1"}}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "dist"}, want: []string{nodeBuildRun("npm run build\n")}, absent: []string{"legacy"}},
		{name: "Create React App 5 needs nothing", files: map[string]string{
			"package.json": `{"name":"cra","scripts":{"build":"react-scripts build"},"dependencies":{"react-scripts":"^5.0.1"}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "build"}, absent: []string{"legacy"}},
		{name: "T3 Env skipped while server variables have no build value", files: map[string]string{"package.json": t3, "src/env.js": t3Env},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"}, bound: []string{"DATABASE_URL"},
			want: []string{`env=DATABASE_URL,required=true export SKIP_ENV_VALIDATION="${SKIP_ENV_VALIDATION:-1}" && npm run build` + "\n"}, absent: []string{"NODE_OPTIONS", "max-old-space-size"}},
		{name: "T3 Env validates when every server variable reaches the build", files: map[string]string{"package.json": t3, "src/env.js": t3Env},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"}, bound: []string{"DATABASE_URL", "AUTH_DISCORD_ID", "RATE"},
			absent: []string{"SKIP_ENV_VALIDATION="}},
		{name: "an install-only value does not reach the build", files: map[string]string{"package.json": t3, "src/env.js": t3Env},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start",
				Secrets: []BuildSecretConfig{{Variable: "AUTH_DISCORD_ID", Step: "install"}}}, bound: []string{"DATABASE_URL", "AUTH_DISCORD_ID", "RATE"},
			want: []string{`SKIP_ENV_VALIDATION="${SKIP_ENV_VALIDATION:-1}"`}},
		{name: "a schema without the skip switch is left to fail by name", files: map[string]string{"package.json": t3,
			"src/env.js": strings.Replace(t3Env, "skipValidation: !!process.env.SKIP_ENV_VALIDATION,", "", 1)},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"}, absent: []string{"SKIP_ENV_VALIDATION"}},
		{name: "a start script's env file is created in the runtime image", files: map[string]string{
			"package.json": `{"name":"api","scripts":{"build":"tsc","start":"node --env-file=.env dist/server.js"}}`},
			config:   BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:     []string{"COPY --from=build /app /app\nRUN [ -e .env ] || : > .env\nCMD"},
			findings: []string{"env_file_placeholder"}},
		{name: "a build script's env file is created before the build", files: map[string]string{
			"package.json": `{"name":"api","scripts":{"build":"tsx --env-file './.env.local' scripts/build.ts","start":"node dist/server.js"}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:   []string{"RUN [ -e .env.local ] || : > .env.local\n" + nodeBuildRun("npm run build\n")}},
		{name: "an env file's missing directory is created with it", files: map[string]string{
			"package.json": `{"name":"api","scripts":{"build":"tsc","start":"node --env-file=config/.env dist/server.js"}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"},
			want:   []string{"COPY --from=build /app /app\nRUN [ -e config/.env ] || { mkdir -p config && : > config/.env; }\nCMD"}},
		{name: "a Procfile start command", files: map[string]string{"package.json": `{"name":"api","scripts":{"build":"tsc"}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "node --env-file .env.production dist/index.js"},
			want:   []string{"RUN [ -e .env.production ] || : > .env.production\nCMD"}},
		{name: "an env file path outside the package is not written", files: map[string]string{
			"package.json": `{"name":"api","scripts":{"start":"node --env-file=../secrets.env --env-file-if-exists=.env server.js"}}`},
			config: BuildPlanConfig{StartCommand: "npm run start"}, absent: []string{"secrets.env ]", "[ -e .env ]"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := test.config
			config.Method, config.Recipe = BuildRecipe, "node"
			prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, test.files), config, false, "t:1", test.bound...)
			if err != nil {
				t.Fatal(err)
			}
			assertDockerfile(t, prepared.DockerfilePreview, test.want, test.absent)
			plan := planNodeInstall(readNodeTree(t, test.files, "").facts, nodeInstallChoice{build: test.config.BuildCommand, start: test.config.StartCommand})
			for _, code := range test.findings {
				if findingByCode(plan.findings, code) == nil {
					t.Fatalf("findings %+v lack %s", plan.findings, code)
				}
			}
		})
	}
}

func TestEnvValidationSchemaIsReadAsText(t *testing.T) {
	t.Parallel()
	source := readNodeTree(t, map[string]string{
		"package.json": `{"name":"t3","dependencies":{"@t3-oss/env-nextjs":"^0.12.0"}}`, "src/env.js": t3Env}, "")
	validation := source.facts.envValidation
	if validation.source != "src/env.js" || !validation.skippable ||
		strings.Join(validation.server, ",") != "AUTH_DISCORD_ID,DATABASE_URL,RATE" || strings.Join(validation.client, ",") != "NEXT_PUBLIC_POSTHOG_KEY" {
		t.Fatalf("validation = %+v", validation)
	}
	if missing := validation.missing(map[string]bool{"DATABASE_URL": true}); strings.Join(missing, ",") != "AUTH_DISCORD_ID,RATE" {
		t.Fatalf("missing = %v", missing)
	}
	if unread := readNodeTree(t, map[string]string{"package.json": `{"name":"plain"}`, "src/env.js": t3Env}, "").facts.envValidation; unread.source != "" {
		t.Fatalf("a package without T3 Env read a schema: %+v", unread)
	}
}
