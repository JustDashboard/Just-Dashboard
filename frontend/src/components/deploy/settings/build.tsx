"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import { Copy, LockClosed } from "@/components/icons"
import { ApiError, get, post, refusedIndex } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentBuildEvidence,
  DeploymentConfiguration,
  DeploymentDetectionChange,
  DeploymentDetectionProposal,
  DeploymentEnvironmentConfiguration,
  DeploymentRecipe,
  DeploymentRunSnapshot,
  DeploymentStep,
  NodePackageManager,
} from "@/lib/types"
import { ChoiceGrid, ProductCard } from "@/components/choice-card"
import { Disclosure, Field, FieldRow, FormNote, OptionRow } from "@/components/form"
import { Well } from "@/components/panel"
import {
  ProductGlyph,
  ProductGlyphs,
  ProductLogo,
  buildMethodProduct,
  frameworkProduct,
  imageProduct,
  imageProducts,
  packageManagerProduct,
  recipeProduct,
  variableProduct,
} from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { useProject } from "@/components/deploy/project-context"
import {
  BROWSER_PREFIX,
  GO_VERSION,
  GO_VERSIONS,
  PYTHON_VERSION,
  automaticPackageManagerHint,
  commandsForPackageManager,
  dockerfileStageHint,
  goMainPackageList,
  packageManagerOptions,
  packageManagerReading,
  validateConfiguration,
} from "@/components/deploy/deployment-defaults"
import {
  BUILD_METHOD_SHORT,
  RECIPE_SHORT,
  formatDuration,
  frameworkLabel,
} from "@/components/deploy/vocabulary"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { ReleaseTasks, spoken, type ReleaseTask } from "@/components/deploy/settings/release-tasks"
import { Segments } from "@/components/deploy/settings/segments"
import { DetectionProposalPanel } from "@/components/deploy/settings/detection-proposal"
import {
  applyDetectionChanges,
  buildFieldChange,
  proposedValue,
} from "@/components/deploy/settings/detection-changes"

/**
 * How the release is built — which toolchain, from which directory, into
 * what image — and the tasks that run between the build and the release.
 *
 * It opens on four readings (§15 pass 2, which this page had skipped: twelve
 * grey fields and not one figure, on the one page that is about which
 * toolchain makes the project). What it builds with and the release tasks
 * are read from the draft, because what is being set is what the page is
 * about; how long the last build took and what it made are read from the live
 * release's own Build step, because that is the one thing the form cannot
 * say. The builder is a grid of the products themselves rather than a select
 * of their names (§14), and it offers all eight recipes the backend builds —
 * the select it replaced offered three.
 *
 * Two forms, two saves: Build (four rail sections, one PUT) and Release
 * tasks. Each keeps its own draft, keyed on its own saved value, so saving
 * one never restarts the other's unsaved edits.
 */

type BuildPlan = DeploymentConfiguration["build"]

/**
 * The part of the build plan the Build form edits. Release tasks are their
 * own form, and the framework is the server's to record — neither belongs in
 * this draft, or saving release tasks would restart it.
 */
type BuildDraft = Omit<BuildPlan, "releaseTasks" | "framework">

function buildDraftOf(build: BuildPlan): BuildDraft {
  const draft: BuildPlan = { ...build }
  delete draft.releaseTasks
  delete draft.framework
  return draft
}

function useBuildDraft(projectId: number, configuration: DeploymentEnvironmentConfiguration) {
  return useSettingDraft(`deploy.${projectId}.settings.build`, buildDraftOf(configuration.build))
}

function useTasksDraft(projectId: number, configuration: DeploymentEnvironmentConfiguration) {
  return useSettingDraft<ReleaseTask[]>(
    `deploy.${projectId}.settings.releaseTasks`,
    configuration.build.releaseTasks ?? [],
  )
}

/** The scalar `build.*` fields a validation refusal can name, and the field each is drawn in. */
const BUILD_FIELD_IDS: Record<string, string> = {
  "build.method": "build-method",
  "build.recipe": "build-method",
  "build.packageManager": "build-package-manager",
  "build.rootDirectory": "build-root",
  "build.goVersion": "build-go-version",
  "build.goPackage": "build-go-package",
  "build.cargoBin": "build-cargo-bin",
  "build.pythonVersion": "build-python-version",
  "build.spaFallback": "build-spa",
  "build.dockerfile": "build-dockerfile",
  "build.target": "build-target",
  "build.primaryService": "build-primary-service",
  "build.buildCommand": "build-command",
  "build.startCommand": "build-start",
  "build.outputDirectory": "build-output",
  "build.targetPlatform": "build-platform",
}

/** Which rail head a refused field belongs to, so the head says "Not saved" too. */
const FIELD_SECTION: Record<string, "build" | "commands" | "image"> = {
  "build-method": "build",
  "build-package-manager": "build",
  "build-go-version": "build",
  "build-go-package": "build",
  "build-cargo-bin": "build",
  "build-python-version": "build",
  "build-dockerfile": "build",
  "build-target": "build",
  "build-primary-service": "build",
  "build-root": "commands",
  "build-command": "commands",
  "build-start": "commands",
  "build-output": "commands",
  "build-spa": "commands",
  "build-platform": "image",
}

const GO_ERROR = `Use Go ${GO_VERSIONS.slice(0, -1).join(", ")} or ${GO_VERSIONS.at(-1)}, or leave empty to follow go.mod.`

type Builder = {
  key: string
  label: string
  product: string
  detail: string
  method: "recipe" | "dockerfile" | "static"
  recipe?: DeploymentRecipe
}

/**
 * Every way a Git or local source can be built: the eight recipes the
 * backend's `validRecipe` accepts, then a Dockerfile of the project's own and
 * a static site. The detail is a word about the toolchain or what decides it,
 * short enough for the five-across grid at 1280, where a card has about 60px
 * for it.
 */
const BUILDERS: Builder[] = [
  {
    key: "node",
    label: RECIPE_SHORT.node,
    product: "nodejs",
    detail: "JavaScript",
    method: "recipe",
    recipe: "node",
  },
  {
    key: "python",
    label: RECIPE_SHORT.python,
    product: "python",
    detail: "3.10–3.13",
    method: "recipe",
    recipe: "python",
  },
  {
    key: "go",
    label: RECIPE_SHORT.go,
    product: "go",
    detail: `${GO_VERSIONS[0]}–${GO_VERSIONS.at(-1)}`,
    method: "recipe",
    recipe: "go",
  },
  {
    key: "rust",
    label: RECIPE_SHORT.rust,
    product: "rust",
    detail: "Cargo",
    method: "recipe",
    recipe: "rust",
  },
  {
    key: "java",
    label: RECIPE_SHORT.java,
    product: "java",
    detail: "Maven/Gradle",
    method: "recipe",
    recipe: "java",
  },
  {
    key: "dotnet",
    label: RECIPE_SHORT.dotnet,
    product: "dotnet",
    detail: "ASP.NET Core",
    method: "recipe",
    recipe: "dotnet",
  },
  {
    key: "deno",
    label: RECIPE_SHORT.deno,
    product: "deno",
    detail: "deno.json",
    method: "recipe",
    recipe: "deno",
  },
  {
    key: "php",
    label: RECIPE_SHORT.php,
    product: "php",
    detail: "FrankenPHP",
    method: "recipe",
    recipe: "php",
  },
  {
    key: "dockerfile",
    label: "Dockerfile",
    product: "docker",
    detail: "your own",
    method: "dockerfile",
  },
  {
    key: "static",
    label: "Static site",
    product: "nginx-static",
    detail: "nginx",
    method: "static",
  },
]

/**
 * Each manager's lockfile by name, for a project whose saved evidence predates
 * detection reading them; with it, each card says whether its lockfile
 * matches package.json.
 */
const LOCKFILE_NAMES: Record<NodePackageManager, string> = {
  bun: "bun.lock",
  npm: "package-lock",
  pnpm: "pnpm-lock",
  yarn: "yarn.lock",
}

const PYTHON_VERSIONS = ["3.10", "3.11", "3.12", "3.13"]

/** What a recipe falls back to when nothing in the draft or the last build names it. */
const RECIPE_DEFAULT: Record<DeploymentRecipe, string> = {
  node: "lockfile decides",
  python: ".python-version decides",
  go: "go.mod decides",
  rust: "Cargo",
  java: "Maven or Gradle",
  dotnet: ".NET SDK",
  deno: "deno.json",
  php: "Composer · FrankenPHP",
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

/** The live release's Build step, as the readings and the Image section read it. */
type LastBuild = {
  /** `none` when nothing is live, so nothing was built for it; `unreadable` when the run could not be read. */
  state: "none" | "loading" | "ready" | "unreadable"
  step?: DeploymentStep
  evidence?: DeploymentBuildEvidence
}

/**
 * The record of the build behind the live release: one read of that run's
 * steps, for how long the Build step took and the image it made. Read once
 * for the page, and handed to the readings and the form, which both draw
 * from it.
 */
function useLastBuild(projectId: number): LastBuild {
  const project = useProject()
  const runId = project.liveRelease?.runId ?? project.liveRun?.id
  const snapshot = usePoll(
    (signal) => get<DeploymentRunSnapshot>(`/deploy/${projectId}/runs/${runId}`, undefined, signal),
    0,
    [projectId, runId],
    { enabled: runId !== undefined },
  )
  if (runId === undefined) return { state: "none" }
  if (snapshot.error && !snapshot.data) return { state: "unreadable" }
  if (!snapshot.data) return { state: "loading" }
  const step = snapshot.data.steps.filter((one) => one.key === "build_artifact").at(-1)
  return { state: "ready", step, evidence: step?.evidence as DeploymentBuildEvidence | undefined }
}

export function BuildSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  const lastBuild = useLastBuild(projectId)
  const legacy = state.configuration?.build.method === "legacy_compose"
  return (
    <SettingsPage
      state={state}
      pageKinds={["build"]}
      readings={
        legacy
          ? undefined
          : (configuration) => (
              <BuildReadings
                projectId={projectId}
                configuration={configuration}
                lastBuild={lastBuild}
              />
            )
      }
    >
      {(configuration) =>
        legacy ? (
          <SettingSection
            id="build"
            title="Build"
            state={
              <span className="flex items-center gap-1.5">
                <ProductGlyph id="docker-compose" />
                Compose, as checked out
              </span>
            }
          >
            <FormNote>
              Legacy Compose projects build from their compose file directly and have no build
              settings here.
            </FormNote>
          </SettingSection>
        ) : (
          <>
            <BuildForm
              projectId={projectId}
              configuration={configuration}
              lastBuild={lastBuild}
              save={state.save}
            />
            <ReleaseTasksForm
              projectId={projectId}
              configuration={configuration}
              save={state.save}
            />
          </>
        )
      }
    </SettingsPage>
  )
}

type Save = ReturnType<typeof useConfiguration>["save"]

/**
 * What a build method builds with, as a reading says it: the short name, a
 * line of what decides the details, and the products it runs — the
 * framework detection recorded, the language, the package manager.
 */
function builderReading(
  build: BuildDraft,
  saved: BuildPlan,
  evidence: DeploymentBuildEvidence | undefined,
  image: string | undefined,
): { value: string; detail: string; products: string[] } {
  if (build.method === "recipe") {
    const recipe = build.recipe ?? "node"
    // The server drops the framework once the method or recipe moves, so a
    // draft that changed either no longer has one.
    const framework =
      saved.method === "recipe" && (saved.recipe ?? "node") === recipe ? saved.framework : undefined
    const prepared = evidence?.result?.prepared
    const detail =
      recipe === "node"
        ? (build.packageManager ?? RECIPE_DEFAULT.node)
        : recipe === "python"
          ? (build.pythonVersion ?? RECIPE_DEFAULT.python)
          : recipe === "go"
            ? (build.goVersion ?? RECIPE_DEFAULT.go)
            : prepared?.recipe === recipe && prepared.toolchain
              ? prepared.toolchain
              : RECIPE_DEFAULT[recipe]
    return {
      value: RECIPE_SHORT[recipe],
      detail: framework ? `${frameworkLabel(framework)} · ${detail}` : detail,
      products: [
        ...new Set(
          [
            frameworkProduct(framework),
            recipeProduct(recipe),
            packageManagerProduct(build.packageManager),
          ].filter((id): id is string => Boolean(id)),
        ),
      ],
    }
  }
  const product = buildMethodProduct(build.method, { image })
  const detail =
    build.method === "dockerfile"
      ? `${build.dockerfile || "Dockerfile"}${build.target ? ` · stage ${build.target}` : ""}`
      : build.method === "static"
        ? "served by nginx"
        : build.method === "image"
          ? (image ?? "a prebuilt image")
          : build.method === "compose"
            ? "the compose file"
            : "runs what it is given"
  return {
    value: build.method === "none" ? "Nothing to build" : BUILD_METHOD_SHORT[build.method],
    detail,
    products: product ? [product] : [],
  }
}

/**
 * The four figures this page sets or produces, above the forms that set them.
 * They read the drafts — the same session keys the forms write — so a
 * builder picked below is the builder named here before anything is saved.
 */
function BuildReadings({
  projectId,
  configuration,
  lastBuild,
}: {
  projectId: number
  configuration: DeploymentEnvironmentConfiguration
  lastBuild: LastBuild
}) {
  const build = useBuildDraft(projectId, configuration).value
  const tasks = useTasksDraft(projectId, configuration).value
  const builder = builderReading(
    build,
    configuration.build,
    lastBuild.evidence,
    configuration.source?.image,
  )

  const image = lastBuild.evidence?.result?.image
  const { step } = lastBuild
  const took =
    step?.startedAt && step.endedAt
      ? (Date.parse(step.endedAt) - Date.parse(step.startedAt)) / 1000
      : undefined

  const timeout = tasks.reduce((sum, task) => sum + (task.timeoutSeconds || 0), 0)

  const buildVariables = configuration.variables.filter((variable) =>
    variable.scopes.includes("build"),
  )
  const installOnly = new Set(
    (build.secrets ?? []).filter((one) => one.step === "install").map((one) => one.variable),
  )
  const secret = buildVariables.filter((variable) => variable.sensitivity === "secret")
  const exposed = secret.find(
    (variable) => BROWSER_PREFIX.test(variable.name) && !installOnly.has(variable.name),
  )

  return (
    <StatGrid columns={4} dense>
      <StatTile
        label="Builds with"
        value={builder.value}
        hint={
          <>
            {builder.detail} <ProductGlyphs ids={builder.products} />
          </>
        }
      />
      {lastBuild.state === "loading" ? (
        <StatTile
          key="loading"
          label="Last build"
          value={<Skeleton className="inline-block h-6 w-20 align-middle" />}
          hint="reading the live release's build"
        />
      ) : (
        <StatTile
          key="ready"
          className={lastBuild.state === "ready" ? "animate-rise" : undefined}
          label="Last build"
          value={took !== undefined ? formatDuration(took) : "—"}
          hint={
            took !== undefined && step?.endedAt ? (
              <>
                {image?.sizeBytes !== undefined && `${bytes(image.sizeBytes)} · `}
                {image?.os && image.architecture && `${image.os}/${image.architecture} · `}
                <time dateTime={step.endedAt} title={timestamp(step.endedAt)}>
                  {relativeTime(step.endedAt)}
                </time>
              </>
            ) : lastBuild.state === "unreadable" ? (
              "could not read the last build"
            ) : (
              "nothing built yet"
            )
          }
        />
      )}
      <StatTile
        label="Release tasks"
        value={tasks.length > 0 ? tasks.length : "None"}
        hint={
          tasks.length > 0
            ? [
                tasks.map((task) => task.name.trim() || "Unnamed").join(" · "),
                // A budget someone set, said the way the task's own timeout
                // says it — not in the measured-time format of "Last build".
                timeout > 0 && `up to ${spoken(timeout)}`,
              ]
                .filter(Boolean)
                .join(" · ")
            : "nothing runs before the release"
        }
      />
      <StatTile
        label="Build variables"
        value={buildVariables.length > 0 ? buildVariables.length : "None"}
        tone={exposed ? "warning" : "default"}
        hint={
          exposed
            ? `${exposed.name} ships to browsers`
            : buildVariables.length > 0
              ? `${installOnly.size} install-only · ${secret.length} secret`
              : "no variable reaches the build"
        }
      />
    </StatGrid>
  )
}

/**
 * The Build form: which builder, the commands it runs, the image it makes and
 * which variables reach which stage — four rail heads, one save, because one
 * PUT writes all of it.
 */
function BuildForm({
  projectId,
  configuration,
  lastBuild,
  save,
}: {
  projectId: number
  configuration: DeploymentEnvironmentConfiguration
  lastBuild: LastBuild
  save: Save
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const project = useProject()
  const { deployment } = project.detail
  const draft = useBuildDraft(projectId, configuration)
  const build = draft.value
  const setBuild = (next: BuildDraft) => draft.set(next)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [fieldError, setFieldError] = useState<{ id: string; message: string }>()
  const errorFor = (id: string) => (fieldError?.id === id ? fieldError.message : undefined)
  const refusedIn = (section: string) =>
    fieldError !== undefined && FIELD_SECTION[fieldError.id] === section

  // Detect again: the source read now, compared with the saved plan. Only the
  // build fields are this form's to apply, into the draft; a field already
  // matching what detection proposes has nothing left to offer.
  const [proposal, setProposal] = useState<DeploymentDetectionProposal>()
  const [detecting, setDetecting] = useState(false)
  const detect = async () => {
    setDetecting(true)
    try {
      setProposal(
        await post<DeploymentDetectionProposal>(
          `/deploy/${projectId}/environments/${project.environmentId}/detect`,
          {},
        ),
      )
    } catch (caught) {
      notify.error("Could not read the source again", caught)
    } finally {
      setDetecting(false)
    }
  }
  const draftValue = (field: string) =>
    field === "build.spaFallback"
      ? String(Boolean(build.spaFallback))
      : String(build[field.slice("build.".length) as keyof BuildDraft] ?? "")
  const proposed = (proposal?.changes ?? [])
    .filter(buildFieldChange)
    .filter((change) => draftValue(change.field) !== change.detected)
  const proposedFor = (field: string) => {
    const change = proposed.find((one) => one.field === field)
    return change && `Detection proposes ${proposedValue(change, change.detected)}.`
  }
  const applyProposed = (changes: DeploymentDetectionChange[]) =>
    setBuild(applyDetectionChanges(build, configuration.runtime, changes).build)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    // Said field by field rather than spread over the saved plan: a field the
    // draft cleared comes back from session storage absent, and a spread would
    // put the saved value back under it.
    const next: BuildPlan = {
      ...build,
      releaseTasks: configuration.build.releaseTasks,
      framework: configuration.build.framework,
    }
    if (
      next.method === "recipe" &&
      next.recipe === "go" &&
      next.goVersion &&
      !GO_VERSION.test(next.goVersion)
    ) {
      setFieldError({ id: "build-go-version", message: GO_ERROR })
      return
    }
    const errors = validateConfiguration(
      { ...configuration, build: next, variables: planVariables(configuration) },
      deployment.profile,
    )
    if (errors.target) {
      setFieldError({ id: "build-target", message: errors.target })
      return
    }
    const refusal = errors.buildMethod || errors.buildSecrets || errors.pythonVersion
    if (refusal) {
      setError(refusal)
      return
    }
    setError(undefined)
    setFieldError(undefined)
    setSaving(true)
    try {
      await save({ build: next })
      notify.success("Build settings saved", {
        description: "They will apply on your next deployment.",
      })
    } catch (caught) {
      if (caught instanceof ApiError && caught.field && BUILD_FIELD_IDS[caught.field]) {
        setFieldError({ id: BUILD_FIELD_IDS[caught.field], message: caught.message })
      } else if (caught instanceof ApiError && (caught.status === 422 || caught.status === 400)) {
        // Most plan refusals name no field — a path outside the source, a
        // recipe that cannot build this — so they are the form's sentence.
        setError(caught.message)
      } else {
        notify.error("Could not save build settings", caught)
      }
    } finally {
      setSaving(false)
    }
  }

  const buildable =
    build.method === "recipe" || build.method === "dockerfile" || build.method === "static"
  // A Compose stack from a repository is built here too — every service's
  // image, from the root directory, for the target platform, with or without
  // the cache (build_artifacts.go) — so it keeps those fields. What it has no
  // use for is a builder: a card pressed in the grid would quietly turn the
  // project away from Compose.
  const builds = buildable || build.method === "compose"
  const picks = (deployment.sourceKind === "git" || deployment.sourceKind === "local") && buildable
  const recipe = build.recipe ?? "node"
  const evidence = lastBuild.evidence?.result
  const buildVariables = configuration.variables.filter((variable) =>
    variable.scopes.includes("build"),
  )
  const stageOf = (name: string) =>
    build.secrets?.find((binding) => binding.variable === name)?.step ?? "build"
  const installOnly = buildVariables.filter((variable) => stageOf(variable.name) === "install")
  const subdirectory = configuration.source?.subdirectory?.replace(/^\/+|\/+$/g, "")
  const root = build.rootDirectory?.replace(/^\/+|\/+$/g, "")
  const runsIn = [subdirectory, root].filter(Boolean).join("/")
  const bunLastBuild = evidence?.prepared?.baseImages?.some((base) =>
    base.reference.startsWith("oven/bun"),
  )
  // What detection read for the code this build still describes; absent
  // once the build moved to a directory or recipe detection did not read.
  const detected = configuration.detected
  // "Lockfile" resolves to the manager detection chose, so a command left on
  // another manager's runner moves with it instead of staying behind on an
  // image that lacks that runner.
  const choosePackageManager = (packageManager: NodePackageManager | undefined) =>
    setBuild({
      ...build,
      packageManager,
      ...(recipe === "php" ? {} : commandsForPackageManager(detected, build, packageManager)),
    })
  // What the last build did is said only while the draft still builds the
  // same way: a Node toolchain or a Node Dockerfile under a Python recipe
  // would be a stale fact dressed as a current one.
  const asLastBuilt =
    evidence?.prepared?.method === build.method &&
    (build.method !== "recipe" || evidence.prepared.recipe === recipe)
  const preview = asLastBuilt ? evidence?.prepared?.dockerfilePreview?.trimEnd() : undefined

  const choose = (choice: Builder) => {
    // Pressing the builder already chosen changes nothing, not even a recipe
    // saved without one that the grid draws as Node.
    if (
      choice.method === "recipe"
        ? build.method === "recipe" && recipe === choice.recipe
        : build.method === choice.method
    )
      return
    if (choice.method === "recipe") {
      const next = choice.recipe
      setBuild({
        ...build,
        method: "recipe",
        recipe: next,
        secrets: build.method === "recipe" ? build.secrets : [],
        goVersion: next === "go" ? build.goVersion : undefined,
        goPackage: next === "go" ? build.goPackage : undefined,
        cargoBin: next === "rust" ? build.cargoBin : undefined,
        pythonVersion: next === "python" ? build.pythonVersion : undefined,
        // The PHP recipe's asset stage installs through the same Node
        // install, so the choice survives the move between the two.
        packageManager: next === "node" || next === "php" ? build.packageManager : undefined,
      })
      return
    }
    // A recipe is valid only for the automatic builder (planning_model.go),
    // so leaving it for a Dockerfile or a static site clears everything that
    // belongs to one — the save used to be refused for the recipe left behind.
    setBuild({
      ...build,
      method: choice.method,
      recipe: undefined,
      secrets: [],
      goVersion: undefined,
      goPackage: undefined,
      cargoBin: undefined,
      pythonVersion: undefined,
      packageManager: undefined,
      spaFallback: choice.method === "static" ? build.spaFallback : undefined,
      target: choice.method === "dockerfile" ? build.target : undefined,
    })
  }

  return (
    <SettingForm
      name="Build"
      onSubmit={submit}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={() => {
        setError(undefined)
        setFieldError(undefined)
        draft.discard()
      }}
      applies="next-deployment"
      error={error}
    >
      <SettingSection
        id="build"
        title="Build"
        // The builder and its products are the "Builds with" reading's; the
        // head keeps what only the last build knows.
        state={
          asLastBuilt &&
          evidence?.prepared?.toolchain && (
            <span className="block truncate font-mono">
              {evidence.prepared.nodeVersion && `node ${evidence.prepared.nodeVersion} · `}
              {evidence.prepared.toolchain} · last build
            </span>
          )
        }
        status={settingStatus({
          dirty: draft.changed([
            "method",
            "recipe",
            "packageManager",
            "goVersion",
            "goPackage",
            "cargoBin",
            "pythonVersion",
            "dockerfile",
            "target",
            "primaryService",
          ]),
          refused: refusedIn("build") || Boolean(error),
        })}
        actions={
          canEdit &&
          picks && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => void detect()}
              pending={detecting}
            >
              Detect again
            </Button>
          )
        }
      >
        {proposal && (
          <DetectionProposalPanel
            projectId={projectId}
            title="Detection proposes"
            proposal={proposal}
            changes={proposed}
            canEdit={canEdit}
            onApply={applyProposed}
            onDismiss={() => setProposal(undefined)}
          />
        )}
        {picks ? (
          <Field label="Builder" error={errorFor("build-method")}>
            <ChoiceGrid id="build-method" columns="compact" role="group" aria-label="Builder">
              {BUILDERS.map((choice) => (
                <ProductCard
                  key={choice.key}
                  product={choice.product}
                  label={choice.label}
                  detail={choice.detail}
                  selected={
                    choice.method === "recipe"
                      ? build.method === "recipe" && recipe === choice.recipe
                      : build.method === choice.method
                  }
                  disabled={!canEdit}
                  onClick={() => choose(choice)}
                />
              ))}
            </ChoiceGrid>
          </Field>
        ) : (
          <PrebuiltFact
            method={build.method}
            image={configuration.source?.image ?? deployment.sourceRepository}
            files={configuration.source?.composeFiles?.map((file) => file.path)}
          />
        )}

        {build.method === "recipe" &&
          (recipe === "node" ||
            (recipe === "php" && (detected?.nodeInstalls?.length ?? 0) > 0)) && (
            <Field
              label="Package manager"
              hint={
                proposedFor("build.packageManager") ??
                packageManagerReading(detected, build.packageManager) ??
                "Pick one when the repository has more than one lockfile."
              }
              error={errorFor("build-package-manager")}
            >
              <ChoiceGrid
                id="build-package-manager"
                columns="compact"
                role="group"
                aria-label="Package manager"
              >
                <ProductCard
                  fallback={LockClosed}
                  label="Lockfile"
                  detail={
                    automaticPackageManagerHint(detected) ||
                    (bunLastBuild ? "bun last build" : "decides")
                  }
                  selected={build.packageManager === undefined}
                  disabled={!canEdit}
                  onClick={() => choosePackageManager(undefined)}
                />
                {packageManagerOptions(detected).map((option) => (
                  <ProductCard
                    key={option.value}
                    product={option.value}
                    fallback={LockClosed}
                    label={option.label}
                    detail={option.hint || LOCKFILE_NAMES[option.value]}
                    selected={build.packageManager === option.value}
                    disabled={!canEdit || option.disabled}
                    onClick={() => choosePackageManager(option.value)}
                  />
                ))}
              </ChoiceGrid>
            </Field>
          )}

        {build.method === "recipe" && recipe === "python" && (
          <Field
            label="Python version"
            hint="Auto reads .python-version, runtime.txt or pyproject.toml."
            error={errorFor("build-python-version")}
          >
            <Segments
              id="build-python-version"
              label="Python version"
              fill
              value={build.pythonVersion ?? "auto"}
              disabled={!canEdit}
              onChange={(next) =>
                setBuild({ ...build, pythonVersion: next === "auto" ? undefined : next })
              }
              options={[
                { value: "auto", label: "Auto" },
                ...PYTHON_VERSIONS.map((version) => ({
                  value: version,
                  label: version,
                  mono: true,
                })),
                // A version saved before the choices were closed, kept on
                // screen so the refusal under it names something visible.
                ...(build.pythonVersion && !PYTHON_VERSION.test(build.pythonVersion)
                  ? [{ value: build.pythonVersion, label: build.pythonVersion, mono: true }]
                  : []),
              ]}
            />
          </Field>
        )}

        {build.method === "recipe" && recipe === "go" && (
          <Field
            label="Go main package"
            htmlFor="build-go-package"
            hint={
              proposedFor("build.goPackage") ??
              (proposal?.candidate?.goMainPackages?.length && !proposal.elsewhere
                ? `Main packages: ${goMainPackageList(proposal.candidate)}.`
                : "The directory of the command to build, such as cmd/api; empty lets the recipe choose.")
            }
            error={errorFor("build-go-package")}
          >
            <InputGroup>
              <InputGroupAddon align="inline-start">
                <InputGroupText className="font-mono">./</InputGroupText>
              </InputGroupAddon>
              <InputGroupInput
                id="build-go-package"
                value={build.goPackage ?? ""}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("build-go-package"))}
                className="font-mono"
                placeholder="recipe chooses"
                autoComplete="off"
                spellCheck={false}
                onChange={(event) =>
                  setBuild({
                    ...build,
                    goPackage: event.target.value.replace(/^\.\//, "") || undefined,
                  })
                }
              />
            </InputGroup>
          </Field>
        )}

        {build.method === "recipe" && recipe === "rust" && (
          <Field
            label="Rust binary"
            htmlFor="build-cargo-bin"
            hint={
              proposedFor("build.cargoBin") ??
              ((proposal?.candidate?.rust?.binaries?.length ?? 0) > 1 && !proposal?.elsewhere
                ? `Binaries: ${proposal?.candidate?.rust?.binaries?.join(", ")}.`
                : "The binary target to serve; empty lets the recipe choose (default-run, or the one that starts a server).")
            }
            error={errorFor("build-cargo-bin")}
          >
            <Input
              id="build-cargo-bin"
              value={build.cargoBin ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("build-cargo-bin"))}
              className="font-mono"
              placeholder={proposal?.candidate?.rust?.binary || "recipe chooses"}
              autoComplete="off"
              spellCheck={false}
              onChange={(event) =>
                setBuild({ ...build, cargoBin: event.target.value.trim() || undefined })
              }
            />
          </Field>
        )}

        {build.method === "recipe" && recipe === "go" && (
          <Field label="Go version" htmlFor="build-go-version" error={errorFor("build-go-version")}>
            <InputGroup>
              <InputGroupAddon align="inline-start">
                <ProductGlyph id="go" />
              </InputGroupAddon>
              <InputGroupInput
                id="build-go-version"
                value={build.goVersion ?? ""}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("build-go-version"))}
                className="font-mono"
                placeholder="go.mod decides"
                onChange={(event) =>
                  setBuild({ ...build, goVersion: event.target.value || undefined })
                }
              />
            </InputGroup>
          </Field>
        )}

        {build.method === "dockerfile" && (
          <Field
            label="Dockerfile path"
            htmlFor="build-dockerfile"
            hint="Relative to the root directory."
            error={errorFor("build-dockerfile")}
          >
            <InputGroup>
              <InputGroupAddon align="inline-start">
                <ProductGlyph id="docker" />
              </InputGroupAddon>
              <InputGroupInput
                id="build-dockerfile"
                value={build.dockerfile ?? ""}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("build-dockerfile"))}
                className="font-mono"
                placeholder="Dockerfile"
                // A stage belongs to the file it was read from; another file
                // keeping it would fail with "not a stage".
                onChange={(event) =>
                  setBuild({ ...build, dockerfile: event.target.value, target: undefined })
                }
              />
            </InputGroup>
          </Field>
        )}
        {build.method === "compose" && (
          <Field
            label="Primary service"
            htmlFor="build-primary-service"
            hint="The service readiness and the release's container follow. Leave empty for the one detection chose: it builds or publishes a port, never a database."
            error={errorFor("build-primary-service")}
          >
            <Input
              id="build-primary-service"
              value={build.primaryService ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("build-primary-service"))}
              className="font-mono"
              placeholder="web"
              onChange={(event) =>
                setBuild({ ...build, primaryService: event.target.value.trim() || undefined })
              }
            />
          </Field>
        )}
        {build.method === "dockerfile" && (
          <Field
            label="Stage"
            htmlFor="build-target"
            hint={proposedFor("build.target") ?? dockerfileStageHint()}
            error={errorFor("build-target")}
          >
            <Input
              id="build-target"
              value={build.target ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("build-target"))}
              className="font-mono"
              placeholder="the last stage"
              onChange={(event) =>
                setBuild({ ...build, target: event.target.value.trim() || undefined })
              }
            />
          </Field>
        )}
      </SettingSection>

      {builds && (
        <SettingSection
          id="commands"
          title="Commands"
          state={
            <span className="block space-y-1">
              <span className="block truncate">
                runs in <span className="font-mono text-foreground">/{runsIn}</span>
              </span>
              {build.method !== "dockerfile" && build.buildCommand && (
                <span className="block truncate font-mono" title={build.buildCommand}>
                  $ {build.buildCommand}
                </span>
              )}
            </span>
          }
          status={settingStatus({
            dirty: draft.changed([
              "rootDirectory",
              "buildCommand",
              "startCommand",
              "outputDirectory",
              "spaFallback",
            ]),
            refused: refusedIn("commands"),
          })}
        >
          <Field
            label="Root directory"
            htmlFor="build-root"
            hint="Inside the source's own root directory."
            error={errorFor("build-root")}
          >
            <InputGroup>
              <InputGroupAddon align="inline-start">
                <InputGroupText className="max-w-32 font-mono">
                  <span className="truncate">{subdirectory ? `/${subdirectory}/` : "/"}</span>
                </InputGroupText>
              </InputGroupAddon>
              <InputGroupInput
                id="build-root"
                value={build.rootDirectory ?? ""}
                readOnly={!canEdit}
                aria-invalid={Boolean(errorFor("build-root"))}
                className="font-mono"
                placeholder="source root"
                onChange={(event) => setBuild({ ...build, rootDirectory: event.target.value })}
              />
            </InputGroup>
          </Field>

          {(build.method === "recipe" || build.method === "static") && (
            <>
              {build.method === "static" ? (
                <CommandField
                  id="build-command"
                  label="Build command"
                  value={build.buildCommand ?? ""}
                  hint={proposedFor("build.buildCommand")}
                  error={errorFor("build-command")}
                  readOnly={!canEdit}
                  onChange={(buildCommand) => setBuild({ ...build, buildCommand })}
                />
              ) : (
                // A static site has no start command: nginx serves its output.
                <FieldRow>
                  <CommandField
                    id="build-command"
                    label="Build command"
                    value={build.buildCommand ?? ""}
                    hint={proposedFor("build.buildCommand")}
                    error={errorFor("build-command")}
                    readOnly={!canEdit}
                    onChange={(buildCommand) => setBuild({ ...build, buildCommand })}
                  />
                  <CommandField
                    id="build-start"
                    label="Start command"
                    value={build.startCommand ?? ""}
                    hint={proposedFor("build.startCommand")}
                    error={errorFor("build-start")}
                    readOnly={!canEdit}
                    onChange={(startCommand) => setBuild({ ...build, startCommand })}
                  />
                </FieldRow>
              )}
              <Field
                label="Output directory"
                htmlFor="build-output"
                hint={proposedFor("build.outputDirectory")}
                error={errorFor("build-output")}
              >
                <InputGroup>
                  <InputGroupAddon align="inline-start">
                    <InputGroupText className="max-w-32 font-mono">
                      <span className="truncate">{root ? `${root}/` : "./"}</span>
                    </InputGroupText>
                  </InputGroupAddon>
                  <InputGroupInput
                    id="build-output"
                    value={build.outputDirectory ?? ""}
                    readOnly={!canEdit}
                    aria-invalid={Boolean(errorFor("build-output"))}
                    className="font-mono"
                    placeholder="dist"
                    onChange={(event) =>
                      setBuild({ ...build, outputDirectory: event.target.value })
                    }
                  />
                </InputGroup>
              </Field>
            </>
          )}

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
        </SettingSection>
      )}

      {builds && (
        <SettingSection
          id="image"
          title="Image"
          // Its size and platform are the "Last build" reading's; what it was
          // built on is only here.
          state={
            <span className="block space-y-1">
              {evidence?.prepared?.baseImages && evidence.prepared.baseImages.length > 0 && (
                <span className="flex min-w-0 items-center gap-1.5">
                  last image from
                  <ProductGlyphs
                    ids={imageProducts(evidence.prepared.baseImages.map((base) => base.reference))}
                  />
                </span>
              )}
              <span className="block">
                {build.noCache ? "clean every build" : "cached layers reused"}
              </span>
            </span>
          }
          status={settingStatus({
            dirty: draft.changed(["targetPlatform", "noCache"]),
            refused: refusedIn("image"),
          })}
        >
          <Field
            label="Target platform"
            htmlFor="build-platform"
            hint="Empty builds for this server."
            error={errorFor("build-platform")}
          >
            <Input
              id="build-platform"
              value={build.targetPlatform ?? ""}
              readOnly={!canEdit}
              aria-invalid={Boolean(errorFor("build-platform"))}
              className="font-mono"
              placeholder={
                evidence?.image?.os && evidence.image.architecture
                  ? `${evidence.image.os}/${evidence.image.architecture}`
                  : "linux/amd64"
              }
              onChange={(event) =>
                setBuild({ ...build, targetPlatform: event.target.value || undefined })
              }
            />
          </Field>
          <OptionRow
            title="Force a clean build"
            hint="Ignores the build cache and rebuilds every layer from scratch."
            checked={build.noCache ?? false}
            onCheckedChange={(noCache) => setBuild({ ...build, noCache: noCache || undefined })}
            disabled={!canEdit}
          />
          {preview && (
            <Disclosure
              quiet
              summary={
                evidence?.prepared?.method === "dockerfile"
                  ? "Dockerfile the last build used"
                  : "Dockerfile the recipe wrote"
              }
              facts={`${preview.split("\n").length} lines`}
            >
              <div className="space-y-1.5">
                <div className="flex justify-end">
                  <Button
                    type="button"
                    size="xs"
                    variant="ghost"
                    onClick={() => void copyText(preview, "Dockerfile copied")}
                  >
                    <Copy />
                    Copy
                  </Button>
                </div>
                <Well className="max-h-72 whitespace-pre">{preview}</Well>
              </div>
            </Disclosure>
          )}
        </SettingSection>
      )}

      {build.method === "recipe" && buildVariables.length > 0 && (
        <SettingSection
          id="build-variables"
          title="Build variables"
          state={
            <>
              <span className="numeric">{buildVariables.length - installOnly.length}</span> reach
              the build
            </>
          }
          status={settingStatus({ dirty: draft.changed(["secrets"]) })}
        >
          <ul className="divide-y divide-hairline" aria-label="Build variables">
            {buildVariables.map((variable) => {
              const stage = stageOf(variable.name)
              const product = variableProduct(variable.name)
              const ships =
                variable.sensitivity === "secret" &&
                stage !== "install" &&
                BROWSER_PREFIX.test(variable.name)
              return (
                <li key={variable.name} className="min-w-0 py-2 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
                    <span className="flex min-w-0 flex-1 items-center gap-2">
                      {/* A slot of its own, so names line up whether or not a
                          variable is drawn as the service that holds it. */}
                      <span className="flex size-3.5 shrink-0 items-center justify-center">
                        {product ? (
                          <ProductGlyph id={product} />
                        ) : (
                          variable.sensitivity === "secret" && (
                            <LockClosed aria-hidden className="size-3.5 text-muted-foreground" />
                          )
                        )}
                      </span>
                      <span className="truncate font-mono text-body">{variable.name}</span>
                      {/* A secret drawn as its service still says it is one,
                          with the lock the Variables page draws a secret by. */}
                      {product && variable.sensitivity === "secret" && (
                        <LockClosed aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                      )}
                      {variable.sensitivity === "secret" && <span className="sr-only">secret</span>}
                    </span>
                    <Segments
                      label={`Stage for ${variable.name}`}
                      value={stage}
                      disabled={!canEdit}
                      onChange={(step) =>
                        setBuild({
                          ...build,
                          secrets: [
                            ...(build.secrets ?? []).filter(
                              (binding) => binding.variable !== variable.name,
                            ),
                            // "build" is what an unmapped build value already gets.
                            ...(step === "build" ? [] : [{ variable: variable.name, step }]),
                          ],
                        })
                      }
                      options={[
                        { value: "build", label: "Build" },
                        { value: "install", label: "Install only" },
                        { value: "install_and_build", label: "Both" },
                      ]}
                    />
                  </div>
                  {ships && (
                    <FormNote tone="warning" className="mt-1.5">
                      {variable.name} is compiled into the JavaScript browsers download — secret or
                      not, it is public.
                    </FormNote>
                  )}
                </li>
              )
            })}
          </ul>
          <FormNote>
            Install only keeps a private registry token out of the build command. Both also mounts
            the value in the install, for a postinstall script that reads it.
          </FormNote>
        </SettingSection>
      )}
    </SettingForm>
  )
}

/** A command the build runs, with the prompt it runs at. */
function CommandField({
  id,
  label,
  value,
  hint,
  error,
  readOnly,
  onChange,
}: {
  id: string
  label: string
  value: string
  hint?: string
  error?: string
  readOnly: boolean
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} htmlFor={id} hint={hint} error={error}>
      <InputGroup>
        <InputGroupAddon align="inline-start">
          <InputGroupText className="font-mono">$</InputGroupText>
        </InputGroupAddon>
        <InputGroupInput
          id={id}
          value={value}
          readOnly={readOnly}
          aria-invalid={Boolean(error)}
          className="font-mono"
          placeholder="recipe default"
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => onChange(event.target.value)}
        />
      </InputGroup>
    </Field>
  )
}

/**
 * What a source that is not built here runs: a prebuilt image, a Compose
 * file, or nothing to build. A fact rather than a picker, because a builder
 * is not something an image or a stack can be given.
 */
function PrebuiltFact({
  method,
  image,
  files,
}: {
  method: BuildPlan["method"]
  image?: string
  files?: string[]
}) {
  const product =
    method === "image"
      ? image
        ? imageProduct(image)
        : "docker"
      : method === "compose"
        ? "docker-compose"
        : undefined
  const title =
    method === "image" ? "Prebuilt image" : method === "compose" ? "Compose" : "Nothing to build"
  const detail =
    method === "image"
      ? image
      : method === "compose"
        ? files?.join(" · ") || "compose.yml"
        : undefined
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ProductLogo id={product} size="md" />
      <div className="min-w-0">
        <p className="text-body font-medium">{title}</p>
        {detail && (
          <p className="truncate font-mono text-hint text-muted-foreground" title={detail}>
            {detail}
          </p>
        )}
      </div>
    </div>
  )
}

/**
 * The tasks that run between the build and the release — a migration, a
 * cache warm — as their own form, because they are their own save.
 */
function ReleaseTasksForm({
  projectId,
  configuration,
  save,
}: {
  projectId: number
  configuration: DeploymentEnvironmentConfiguration
  save: Save
}) {
  const { can } = useAuth()
  const canEdit = can("system.admin")
  const { deployment } = useProject().detail
  const draft = useTasksDraft(projectId, configuration)
  const tasks = draft.value
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [rowError, setRowError] = useState<{ index: number; message: string }>()

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const build = { ...configuration.build, releaseTasks: tasks }
    const errors = validateConfiguration(
      { ...configuration, build, variables: planVariables(configuration) },
      deployment.profile,
    )
    if (errors.releaseTasks) {
      setError(errors.releaseTasks)
      return
    }
    setError(undefined)
    setRowError(undefined)
    setSaving(true)
    try {
      await save({ build })
      notify.success("Release tasks saved", {
        description: "They will apply on your next deployment.",
      })
    } catch (caught) {
      const index =
        caught instanceof ApiError ? refusedIndex(caught.field, "build.releaseTasks") : undefined
      if (caught instanceof ApiError && index !== undefined) {
        setRowError({ index, message: caught.message })
      } else if (caught instanceof ApiError && (caught.status === 422 || caught.status === 400)) {
        setError(caught.message)
      } else {
        notify.error("Could not save release tasks", caught)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Release tasks"
      onSubmit={submit}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={() => {
        setError(undefined)
        setRowError(undefined)
        draft.discard()
      }}
      applies="next-deployment"
      error={error}
    >
      {/* No state under the title: the "Release tasks" reading has the count,
          the names and the budget, and the editor's release path says where
          and in what order they run. */}
      <SettingSection
        id="release-tasks"
        title="Release tasks"
        status={settingStatus({
          dirty: draft.dirty,
          refused: Boolean(error) || rowError !== undefined,
        })}
      >
        <ReleaseTasks
          tasks={tasks}
          variables={configuration.variables}
          buildMethod={configuration.build.method}
          disabled={!canEdit}
          onChange={(next) => draft.set(next)}
          rowError={(index) => (rowError?.index === index ? rowError.message : undefined)}
        />
      </SettingSection>
    </SettingForm>
  )
}
