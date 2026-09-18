"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import { ApiError, put, refusedIndex } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import type {
  DeploymentConfiguration,
  DeploymentEnvironmentConfiguration,
  DeploymentRecipe,
} from "@/lib/types"
import { Field, FormNote, OptionRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useProject } from "@/components/deploy/project-context"
import {
  withPackageManagerRunner,
  validateConfiguration,
} from "@/components/deploy/deployment-defaults"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { ReleaseTasks, type ReleaseTask } from "@/components/deploy/settings/release-tasks"

/**
 * How the release is built: the method, the language it is read as, and the
 * gates that run once the artifact exists.
 */

type BuildPlan = DeploymentConfiguration["build"]

const BUILD_METHODS: [BuildPlan["method"], string][] = [
  ["recipe", "Automatic recipe"],
  ["dockerfile", "Dockerfile"],
  ["static", "Static site"],
  ["image", "Docker image"],
  ["compose", "Compose"],
  ["none", "None"],
]

/** The scalar `build.*` fields a validation refusal can name. */
const BUILD_FIELD_IDS: Record<string, string> = {
  "build.method": "build-method",
  "build.recipe": "build-recipe",
  "build.packageManager": "build-package-manager",
  "build.rootDirectory": "build-root",
  "build.goVersion": "build-go-version",
  "build.pythonVersion": "build-python-version",
  "build.spaFallback": "build-spa",
  "build.dockerfile": "build-dockerfile",
  "build.buildCommand": "build-command",
  "build.startCommand": "build-start",
  "build.outputDirectory": "build-output",
  "build.targetPlatform": "build-platform",
}

// `validateConfiguration` wants the plan's variable shape (a value or
// reference the operator typed); the read model's `DeploymentVariable` never
// carries a value and reports a typed reference as an object. Only the name
// and scopes matter to this validator, so that is all the adapter keeps.
function planVariables(configuration: DeploymentEnvironmentConfiguration) {
  return configuration.variables.map((variable) => ({
    name: variable.name,
    sensitivity: variable.sensitivity,
    scopes: variable.scopes as string[],
  }))
}

export function BuildSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const project = useProject()
  const state = useConfiguration(projectId, environmentId)
  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        // No `key={configuration.revision}` here: every save on this page
        // goes through `refresh()`, which bumps the shared revision, and a
        // key tied to it remounted this whole subtree on ANY save — wiping
        // an unsaved release task the instant the Build card's own save
        // landed. Each card below already reads its starting values into its
        // own `useState` once; that is the only reset it needs.
        <div className="space-y-6">
          <PendingChanges pending={configuration.pending} />
          {configuration.build.method === "legacy_compose" ? (
            <SettingCard title="Build">
              <FormNote>
                Legacy Compose projects build from their compose file directly and have no build
                settings here.
              </FormNote>
            </SettingCard>
          ) : (
            <>
              <BuildForm
                projectId={projectId}
                environmentId={environmentId}
                configuration={configuration}
                profile={project.detail.deployment.profile}
                onSaved={state.refresh}
              />
              <ReleaseTasksCard
                projectId={projectId}
                environmentId={environmentId}
                configuration={configuration}
                profile={project.detail.deployment.profile}
                onSaved={state.refresh}
              />
            </>
          )}
        </div>
      )}
    </ConfigurationState>
  )
}

function BuildForm({
  projectId,
  environmentId,
  configuration,
  profile,
  onSaved,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  profile: Parameters<typeof validateConfiguration>[1]
  onSaved: () => void
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const [build, setBuild] = useState<BuildPlan>(configuration.build)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [fieldError, setFieldError] = useState<{ id: string; message: string }>()
  const errorFor = (id: string) => (fieldError?.id === id ? fieldError.message : undefined)

  const save = async (event: FormEvent) => {
    event.preventDefault()
    const errors = validateConfiguration(
      { ...configuration, build, variables: planVariables(configuration) },
      profile,
    )
    if (errors.buildMethod || errors.buildSecrets) {
      setError(errors.buildMethod || errors.buildSecrets)
      return
    }
    setError(undefined)
    setFieldError(undefined)
    setBusy(true)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/configuration`, {
        revision: configuration.revision,
        build,
        runtime: configuration.runtime,
        dependencies: configuration.dependencies,
        checks: configuration.checks,
        domains: configuration.domains,
      })
      notify.success("Build settings saved", {
        description: "They will apply on your next deployment.",
      })
      onSaved()
    } catch (caught) {
      if (caught instanceof ApiError && caught.field && BUILD_FIELD_IDS[caught.field]) {
        setFieldError({ id: BUILD_FIELD_IDS[caught.field], message: caught.message })
      } else {
        notify.error("Could not save build settings", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  const buildVariables = configuration.variables.filter((variable) =>
    variable.scopes.includes("build"),
  )

  return (
    <form onSubmit={save}>
      <SettingCard
        title="Build"
        note="Saving prepares the next release."
        action={
          canEdit && (
            <Button size="sm" type="submit" pending={busy}>
              Save
            </Button>
          )
        }
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Build method" htmlFor="build-method" error={errorFor("build-method")}>
            <Select
              value={build.method}
              disabled={!canEdit}
              onValueChange={(method: BuildPlan["method"]) =>
                setBuild({
                  ...build,
                  method,
                  secrets: method === "recipe" ? build.secrets : [],
                  goVersion: method === "recipe" ? build.goVersion : undefined,
                  pythonVersion: method === "recipe" ? build.pythonVersion : undefined,
                  packageManager: method === "recipe" ? build.packageManager : undefined,
                  spaFallback:
                    method === "recipe" || method === "static" ? build.spaFallback : undefined,
                })
              }
            >
              <SelectTrigger id="build-method" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {BUILD_METHODS.map(([method, label]) => (
                  <SelectItem key={method} value={method}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          {build.method === "recipe" && (
            <Field label="Language" htmlFor="build-recipe" error={errorFor("build-recipe")}>
              <Select
                value={build.recipe ?? "node"}
                disabled={!canEdit}
                onValueChange={(recipe: DeploymentRecipe) =>
                  setBuild({
                    ...build,
                    recipe,
                    goVersion: recipe === "go" ? build.goVersion : undefined,
                    pythonVersion: recipe === "python" ? build.pythonVersion : undefined,
                    packageManager: recipe === "node" ? build.packageManager : undefined,
                  })
                }
              >
                <SelectTrigger id="build-recipe" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="node">JavaScript / TypeScript</SelectItem>
                  <SelectItem value="go">Go</SelectItem>
                  <SelectItem value="python">Python</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}
          {build.method === "recipe" && (build.recipe ?? "node") === "node" && (
            <Field
              label="Package manager"
              htmlFor="build-package-manager"
              hint="Choose one when the repository has more than one lockfile."
              error={errorFor("build-package-manager")}
            >
              <Select
                value={build.packageManager ?? "lockfile"}
                disabled={!canEdit}
                onValueChange={(value) => {
                  const packageManager =
                    value === "lockfile" ? undefined : (value as BuildPlan["packageManager"])
                  setBuild({
                    ...build,
                    packageManager,
                    buildCommand:
                      build.buildCommand &&
                      withPackageManagerRunner(build.buildCommand, packageManager),
                    startCommand:
                      build.startCommand &&
                      withPackageManagerRunner(build.startCommand, packageManager),
                  })
                }}
              >
                <SelectTrigger id="build-package-manager" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="lockfile">From the lockfile</SelectItem>
                  <SelectItem value="bun">Bun</SelectItem>
                  <SelectItem value="npm">npm</SelectItem>
                  <SelectItem value="pnpm">pnpm</SelectItem>
                  <SelectItem value="yarn">Yarn</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}
          <Field label="Root directory" htmlFor="build-root" error={errorFor("build-root")}>
            <Input
              id="build-root"
              value={build.rootDirectory ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("build-root"))}
              className="font-mono"
              onChange={(event) => setBuild({ ...build, rootDirectory: event.target.value })}
            />
          </Field>
          {build.method === "recipe" && build.recipe === "go" && (
            <Field
              label="Go version (optional)"
              htmlFor="build-go-version"
              error={errorFor("build-go-version")}
            >
              <Input
                id="build-go-version"
                value={build.goVersion ?? ""}
                readOnly={!canEdit}
                className="font-mono"
                placeholder="1.25"
                onChange={(event) => setBuild({ ...build, goVersion: event.target.value })}
              />
            </Field>
          )}
          {build.method === "recipe" && build.recipe === "python" && (
            <Field
              label="Python version (optional)"
              htmlFor="build-python-version"
              hint="Leave empty to use .python-version, runtime.txt or pyproject.toml."
              error={errorFor("build-python-version")}
            >
              <Input
                id="build-python-version"
                value={build.pythonVersion ?? ""}
                readOnly={!canEdit}
                className="font-mono"
                placeholder="3.13"
                onChange={(event) => setBuild({ ...build, pythonVersion: event.target.value })}
              />
            </Field>
          )}
          {build.method === "dockerfile" && (
            <Field
              label="Dockerfile path"
              htmlFor="build-dockerfile"
              error={errorFor("build-dockerfile")}
            >
              <Input
                id="build-dockerfile"
                value={build.dockerfile ?? ""}
                readOnly={!canEdit}
                className="font-mono"
                onChange={(event) => setBuild({ ...build, dockerfile: event.target.value })}
              />
            </Field>
          )}
          {(build.method === "recipe" || build.method === "static") && (
            <>
              <Field
                label="Build command"
                htmlFor="build-command"
                error={errorFor("build-command")}
              >
                <Input
                  id="build-command"
                  value={build.buildCommand ?? ""}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("build-command"))}
                  className="font-mono"
                  onChange={(event) => setBuild({ ...build, buildCommand: event.target.value })}
                />
              </Field>
              <Field label="Start command" htmlFor="build-start" error={errorFor("build-start")}>
                <Input
                  id="build-start"
                  value={build.startCommand ?? ""}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("build-start"))}
                  className="font-mono"
                  onChange={(event) => setBuild({ ...build, startCommand: event.target.value })}
                />
              </Field>
              <Field
                label="Output directory"
                htmlFor="build-output"
                error={errorFor("build-output")}
              >
                <Input
                  id="build-output"
                  value={build.outputDirectory ?? ""}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(errorFor("build-output"))}
                  className="font-mono"
                  onChange={(event) => setBuild({ ...build, outputDirectory: event.target.value })}
                />
              </Field>
            </>
          )}
          <Field
            label="Target platform"
            htmlFor="build-platform"
            hint="Optional OCI platform, for example linux/amd64."
            error={errorFor("build-platform")}
          >
            <Input
              id="build-platform"
              value={build.targetPlatform ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("build-platform"))}
              className="font-mono"
              placeholder="linux/amd64"
              onChange={(event) => setBuild({ ...build, targetPlatform: event.target.value })}
            />
          </Field>
        </div>

        {(build.method === "static" ||
          (build.method === "recipe" && Boolean(build.outputDirectory))) && (
          <OptionRow
            title="Single-page application"
            hint="Answers paths without a file with index.html, so client-side routes open directly."
            checked={build.spaFallback ?? false}
            onCheckedChange={(spaFallback) =>
              setBuild({ ...build, spaFallback: spaFallback || undefined })
            }
            disabled={!canEdit}
          />
        )}

        <OptionRow
          title="Force a clean build"
          hint="Ignores the build cache and rebuilds every layer from scratch."
          checked={build.noCache ?? false}
          onCheckedChange={(noCache) => setBuild({ ...build, noCache })}
          disabled={!canEdit}
        />

        {build.method === "recipe" && buildVariables.length > 0 && (
          <div className="space-y-3">
            <p className="text-hint leading-relaxed text-muted-foreground">
              Build-scoped variables reach the build command automatically. Limit a private package
              credential to dependency installation below. Values compiled into browser assets are
              public regardless of their secret setting.
            </p>
            {buildVariables.map((variable) => (
              <Field
                key={variable.name}
                htmlFor={`build-stage-${variable.name}`}
                label={`Stage for ${variable.name}`}
              >
                <Select
                  value={
                    build.secrets?.find((binding) => binding.variable === variable.name)?.step ??
                    "build"
                  }
                  disabled={!canEdit}
                  onValueChange={(step) =>
                    setBuild({
                      ...build,
                      secrets: [
                        ...(build.secrets ?? []).filter(
                          (binding) => binding.variable !== variable.name,
                        ),
                        ...(step === "install"
                          ? [{ variable: variable.name, step: "install" as const }]
                          : []),
                      ],
                    })
                  }
                >
                  <SelectTrigger id={`build-stage-${variable.name}`}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="build">Build command</SelectItem>
                    <SelectItem value="install">Dependency installation only</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            ))}
          </div>
        )}

        {error && <FormNote tone="danger">{error}</FormNote>}
      </SettingCard>
    </form>
  )
}

function ReleaseTasksCard({
  projectId,
  environmentId,
  configuration,
  profile,
  onSaved,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  profile: Parameters<typeof validateConfiguration>[1]
  onSaved: () => void
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const [tasks, setTasks] = useState<ReleaseTask[]>(configuration.build.releaseTasks ?? [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [rowError, setRowError] = useState<{ index: number; message: string }>()

  const save = async (event: FormEvent) => {
    event.preventDefault()
    const build = { ...configuration.build, releaseTasks: tasks }
    const errors = validateConfiguration(
      { ...configuration, build, variables: planVariables(configuration) },
      profile,
    )
    if (errors.releaseTasks) {
      setError(errors.releaseTasks)
      return
    }
    setError(undefined)
    setRowError(undefined)
    setBusy(true)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/configuration`, {
        revision: configuration.revision,
        build,
        runtime: configuration.runtime,
        dependencies: configuration.dependencies,
        checks: configuration.checks,
        domains: configuration.domains,
      })
      notify.success("Build settings saved", {
        description: "They will apply on your next deployment.",
      })
      onSaved()
    } catch (caught) {
      const index =
        caught instanceof ApiError ? refusedIndex(caught.field, "build.releaseTasks") : undefined
      if (caught instanceof ApiError && index !== undefined) {
        setRowError({ index, message: caught.message })
      } else {
        notify.error("Could not save release tasks", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SettingCard
        title="Release tasks"
        note="Runs after the artifact is recorded and before activation."
        action={
          canEdit && (
            <Button size="sm" type="submit" pending={busy}>
              Save
            </Button>
          )
        }
      >
        <ReleaseTasks
          tasks={tasks}
          variables={configuration.variables}
          disabled={!canEdit}
          onChange={setTasks}
          rowError={(index) => (rowError?.index === index ? rowError.message : undefined)}
        />
        {error && <FormNote tone="danger">{error}</FormNote>}
      </SettingCard>
    </form>
  )
}
