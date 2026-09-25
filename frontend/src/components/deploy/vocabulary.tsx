"use client"

import { useEffect, useState } from "react"
import {
  Box,
  Check,
  Code,
  Envelope,
  GitBranch,
  GitHubMark,
  Globe,
  GridMasonry,
  Inspect,
  Layers,
  Servers,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural } from "@/lib/format"
import type { GitTone } from "@/lib/git-status"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { FolderIcon } from "@/components/files/file-icon"
import {
  ProductGlyph,
  ProductLogo,
  buildMethodProduct,
  channelProduct,
  frameworkProduct,
  gitProviderProduct,
  hasProductLogo,
  hostProduct,
  imageProduct,
  imageProducts,
  issuerProduct,
  webhookProduct,
} from "@/components/product-logo"
import { wordsProduct } from "@/lib/clients"
import type {
  BlueprintSummary,
  DeploymentBuildMethod,
  DeploymentCommit,
  DeploymentDatabaseLink,
  DeploymentDomainRoute,
  DeploymentDraftSource,
  DeploymentEnvironmentConfiguration,
  DeploymentGitWatch,
  DeploymentRelease,
  DeploymentSourceKind,
  DeploymentEngineRun,
  DeploymentRecipe,
  DeploymentRunState,
  DeploymentRuntimeServices,
  DeploymentStep,
  DeploymentStepState,
  DeploymentStorageMount,
  DeploymentSummary,
  DeployProject,
  NotificationChannel,
  ReleaseListChange,
  WorkloadProfile,
} from "@/lib/types"

/**
 * The words and marks every deployment screen shares.
 *
 * A run's state, a project's state, a workload's glyph, the line that says
 * where a project comes from: each of these was spelled out by the screen
 * that needed it, and the projects grid, the overview and the run page had
 * arrived at three different words for the same run state. Named once here,
 * and read as the reader reads Vercel — "Building", "Ready", "Failed" — rather
 * than as the engine's state machine names them.
 */

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

export const RUN_LABELS: Record<DeploymentRunState, string> = {
  requested: "Queued",
  validating: "Queued",
  queued: "Queued",
  preparing: "Preparing",
  running: "Building",
  verifying: "Verifying",
  activating: "Activating",
  failed_activation: "Activation failed",
  restoring_previous: "Rolling back",
  cancelling: "Cancelling",
  succeeded: "Ready",
  failed: "Failed",
  cancelled: "Cancelled",
  rolled_back: "Rolled back",
  superseded: "Superseded",
}

const ACTIVE_RUN_STATES = new Set<DeploymentRunState>([
  "requested",
  "validating",
  "queued",
  "preparing",
  "running",
  "verifying",
  "activating",
  "failed_activation",
  "restoring_previous",
  "cancelling",
])

export function isActiveRun(state: DeploymentRunState | string | undefined) {
  return state ? ACTIVE_RUN_STATES.has(state as DeploymentRunState) : false
}

// Once activation has begun, cancelling would leave the runtime in whichever
// half-swapped state it was in when the request landed; the engine answers
// `409 run_not_cancellable` for these three rather than attempt it.
const NON_CANCELLABLE_STATES = new Set<DeploymentRunState>([
  "activating",
  "failed_activation",
  "restoring_previous",
])

export function isCancellable(state: DeploymentRunState | string | undefined) {
  return isActiveRun(state) && !NON_CANCELLABLE_STATES.has(state as DeploymentRunState)
}

/**
 * The engine retries only a run that failed outright or was cancelled; a
 * run that rolled back or failed activation already restored the previous
 * release, and asking for it again answers `run_not_retryable`.
 */
export function isRetryable(state: DeploymentRunState | string | undefined) {
  return state === "failed" || state === "cancelled"
}

/** A run that ended badly, whichever of the engine's three words it used. */
export function runFailed(state: DeploymentRunState | string | undefined) {
  return state === "failed" || state === "failed_activation" || state === "rolled_back"
}

export function runTone(state: DeploymentRunState | string): DotTone {
  switch (state) {
    case "succeeded":
      return "running"
    case "failed":
    case "failed_activation":
      return "danger"
    case "rolled_back":
      return "warning"
    case "cancelled":
    case "superseded":
      return "stopped"
    default:
      return "warning"
  }
}

/**
 * The one way a run's state is drawn: a dot and the reader's word for it.
 * `live` is only honest on the run page, where the row is fed by the stream.
 */
export function RunStatus({
  state,
  live,
  className,
}: {
  state: DeploymentRunState | string
  live?: boolean
  className?: string
}) {
  const label = RUN_LABELS[state as DeploymentRunState] ?? humanize(state)
  return (
    <Status
      tone={runTone(state)}
      label={label}
      live={Boolean(live) && isActiveRun(state)}
      className={className}
    />
  )
}

const OPERATION_LABELS: Record<string, string> = {
  deploy: "Deploy",
  redeploy: "Redeploy",
  force_build: "Rebuild",
  rollback: "Rollback",
  restart: "Restart",
  stop: "Stop",
  start: "Start",
  preview_create: "Preview",
  preview_update: "Preview update",
  preview_remove: "Preview removal",
  scheduled_action: "Scheduled",
  import_adopt: "Adopt",
  remove_managed: "Remove resources",
}

export function operationLabel(operation: string) {
  return OPERATION_LABELS[operation] ?? humanize(operation)
}

/** "#12 Deploy" — the run's name wherever it is listed. */
export function runTitle(run: Pick<DeploymentEngineRun, "runNumber" | "operation">) {
  return `#${run.runNumber} ${operationLabel(run.operation)}`
}

const TRIGGER_LABELS: Record<string, string> = {
  manual: "manual",
  git_push: "push",
  legacy_hook: "hook",
  generic_hook: "webhook",
  github: "GitHub",
  gitlab: "GitLab",
  bitbucket: "Bitbucket",
  gitea: "Gitea",
  api: "API",
  schedule: "schedule",
  preview: "preview",
  rollback: "rollback",
  migration: "migration",
}

/**
 * Who or what started a run, read once for the line that says it and the mark
 * that draws it.
 *
 * The engine records an actor on every run, and for the machine triggers it
 * is the machine's own name — `git-monitor` for the watcher, `webhook` for a
 * forge's delivery, `scheduler:4` for a schedule. Printed, those read as three
 * people nobody has met ("by git-monitor", "GitHub · webhook"). A person is
 * only ever the sign-in name on a run somebody pressed a button for: a manual
 * run, a rollback, a preview somebody approved.
 */
export type RunActor =
  | { kind: "person"; name: string }
  | { kind: "push" | "provider"; product: string }
  | { kind: "schedule"; name?: string }
  | { kind: "api" | "hook" | "preview" | "system" }

const MACHINE_ACTORS = /^(webhook|git-monitor|system|scheduler:\d+)$/

export function runActor(
  run: Pick<DeploymentEngineRun, "trigger" | "actor" | "metadata">,
): RunActor {
  const person = run.actor && !MACHINE_ACTORS.test(run.actor) ? run.actor : undefined
  const forge = gitProviderProduct(run.trigger)
  if (forge) return { kind: "provider", product: forge }
  switch (run.trigger) {
    case "manual":
    case "rollback":
      return person ? { kind: "person", name: person } : { kind: "system" }
    case "preview":
      return person ? { kind: "person", name: person } : { kind: "preview" }
    case "git_push":
      return { kind: "push", product: "git" }
    case "schedule": {
      const name = run.metadata?.scheduleName
      return { kind: "schedule", name: typeof name === "string" && name ? name : undefined }
    }
    case "api":
      return { kind: "api" }
    case "generic_hook":
    case "legacy_hook":
      return { kind: "hook" }
    default:
      return { kind: "system" }
  }
}

/**
 * Who or what asked for the run, in the words a person would use: "by
 * operator", "on push", "GitHub push", "on schedule · nightly", "by webhook".
 */
export function runTriggerLine(run: Pick<DeploymentEngineRun, "trigger" | "actor" | "metadata">) {
  const actor = runActor(run)
  switch (actor.kind) {
    case "person":
      return `by ${actor.name}`
    case "push":
      return "on push"
    case "provider":
      return `${TRIGGER_LABELS[run.trigger]} push`
    case "schedule":
      return actor.name ? `on schedule · ${actor.name}` : "on schedule"
    case "api":
      return "by API"
    case "hook":
      return "by webhook"
    case "preview":
      return "on pull request"
    default:
      return TRIGGER_LABELS[run.trigger] ?? humanize(run.trigger)
  }
}

/**
 * The commit a run built: its own revision, the commit it recorded, or the
 * release it produced — never the project's current revision, which is the
 * newest run's and would put today's sha on last week's row.
 */
export function runRevision(
  run: Pick<DeploymentEngineRun, "sourceRevision" | "metadata">,
  release?: Pick<DeploymentRelease, "sourceRevision">,
) {
  return run.sourceRevision || runCommit(run)?.sha || release?.sourceRevision || undefined
}

/** The commit a Git run built, when the engine recorded one. */
export function runCommit(run: Pick<DeploymentEngineRun, "metadata"> | undefined) {
  const commit = run?.metadata?.commit
  if (!commit || typeof commit !== "object" || Array.isArray(commit)) return undefined
  const record = commit as Record<string, unknown>
  if (typeof record.sha !== "string") return undefined
  const read = (key: string) =>
    typeof record[key] === "string" ? (record[key] as string) : undefined
  return {
    sha: record.sha,
    subject: read("subject"),
    author: read("author"),
    authoredAt: read("authoredAt"),
  } satisfies DeploymentCommit
}

export function runSubject(run: Pick<DeploymentEngineRun, "metadata"> | undefined) {
  return runCommit(run)?.subject
}

/**
 * The branch, tag or commit a run was asked to build. A run normally follows
 * the configured branch; "Deploy a specific version" records the name it was
 * given instead, and a bare commit id is named as such rather than as a branch
 * it may never have been on.
 */
export function runRef(
  run: Pick<DeploymentEngineRun, "metadata"> | undefined,
  configured: string | undefined,
) {
  const metadata = run?.metadata
  if (typeof metadata?.requestedRef === "string" && metadata.requestedRef) {
    return metadata.requestedRef
  }
  if (typeof metadata?.requestedRevision === "string" && metadata.requestedRevision) {
    return "commit"
  }
  return configured || "main"
}

/** Seconds a run has taken, or took; undefined while it has not started. */
export function runDurationSeconds(
  run: Pick<DeploymentEngineRun, "requestedAt" | "claimedAt" | "endedAt">,
  now = Date.now(),
) {
  const start = new Date(run.claimedAt ?? run.requestedAt).getTime()
  const end = run.endedAt ? new Date(run.endedAt).getTime() : now
  if (Number.isNaN(start) || Number.isNaN(end)) return undefined
  return Math.max(0, (end - start) / 1000)
}

/** "42s", "1m 12s", "1h 03m" — the shape a build time is read in. */
export function formatDuration(seconds: number | undefined) {
  if (seconds === undefined) return "—"
  const total = Math.max(0, Math.round(seconds))
  if (total < 60) return `${total}s`
  const minutes = Math.floor(total / 60)
  if (minutes < 60) return `${minutes}m ${total % 60}s`
  const hours = Math.floor(minutes / 60)
  // Past two days, hours beside a relative time in days read as two units for one span.
  if (hours >= 48) return `${Math.floor(hours / 24)}d ${hours % 24}h`
  return `${hours}h ${String(minutes % 60).padStart(2, "0")}m`
}

/** A clock for a figure that ticks while something is in flight. */
export function useNow(intervalMs = 1000, enabled = true) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!enabled) return
    const timer = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs, enabled])
  return now
}

// ---------------------------------------------------------------------------
// Projects
// ---------------------------------------------------------------------------

export type ProjectState =
  "deploying" | "ready" | "failed" | "unhealthy" | "stopped" | "not_deployed" | "archived"

const PROJECT_LABELS: Record<ProjectState, string> = {
  deploying: "Deploying",
  ready: "Ready",
  failed: "Failed",
  unhealthy: "Unhealthy",
  stopped: "Stopped",
  not_deployed: "Not deployed",
  archived: "Archived",
}

const PROJECT_TONES: Record<ProjectState, DotTone> = {
  deploying: "warning",
  ready: "running",
  failed: "danger",
  unhealthy: "danger",
  stopped: "stopped",
  not_deployed: "stopped",
  archived: "stopped",
}

/**
 * Whether the live release's containers are all down. The runtime observation
 * lists managed containers by release, so a release whose every container has
 * exited — by a Stop, or by somebody stopping it in Docker — reads as stopped
 * rather than as "healthy" on the strength of a check that passed hours ago.
 */
function runtimeStopped(runtime: DeploymentRuntimeServices | undefined, liveReleaseId?: number) {
  if (!runtime || runtime.status !== "available" || !liveReleaseId) return false
  const live = runtime.services.filter((service) => service.releaseId === liveReleaseId)
  return live.length > 0 && live.every((service) => service.state !== "running")
}

export function projectState(
  summary: DeploymentSummary,
  runtime?: DeploymentRuntimeServices,
  archived?: boolean,
): ProjectState {
  if (archived) return "archived"
  if (summary.activeRun) return "deploying"
  if (!summary.liveReleaseId) return runFailed(summary.lastRun?.state) ? "failed" : "not_deployed"
  if (summary.stopped || runtimeStopped(runtime, summary.liveReleaseId)) return "stopped"
  // A failed run newer than the live release is news; a failure the live
  // release has since replaced is history and lives on the Deployments tab.
  const last = summary.lastRun
  if (last && runFailed(last.state) && last.releaseId !== summary.liveReleaseId) return "failed"
  if (summary.health === "unhealthy" || summary.health === "failed") return "unhealthy"
  return "ready"
}

export function ProjectStatus({
  summary,
  runtime,
  archived,
  live,
  className,
}: {
  summary: DeploymentSummary
  runtime?: DeploymentRuntimeServices
  archived?: boolean
  live?: boolean
  className?: string
}) {
  const state = projectState(summary, runtime, archived)
  return (
    <Status
      tone={PROJECT_TONES[state]}
      label={PROJECT_LABELS[state]}
      live={Boolean(live) && state === "deploying"}
      className={className}
    />
  )
}

export function HealthStatus({ health, className }: { health: string; className?: string }) {
  if (health === "healthy" || health === "passed")
    return <Status tone="running" label="Healthy" className={className} />
  if (health === "unhealthy" || health === "failed")
    return <Status tone="danger" label="Unhealthy" className={className} />
  if (health === "warning") return <Status tone="warning" label="Warning" className={className} />
  if (health === "disabled")
    return <Status tone="stopped" label="Checks disabled" className={className} />
  return <Status tone="unknown" label="Not observed" className={className} />
}

/**
 * What the chooser called each kind of source, so step two says the word step
 * one said. `humanize` on the wire value gives "Blueprint" for the thing the
 * reader picked as "Template". `import` is no longer offered as a source, and
 * keeps its word because projects adopted while it was still carry it.
 */
export const SOURCE_KIND_LABELS: Record<DeploymentSourceKind, string> = {
  git: "Git repository",
  local: "Local checkout",
  image: "Docker image",
  compose: "Compose",
  blueprint: "Template",
  import: "Existing workload",
}

/**
 * Where a deployment comes from, as the mark the chooser offered it under.
 *
 * The chooser (`new-project.tsx`'s five tabs) and the plan drawing on Configure
 * each had their own kind-to-glyph ternary, and they disagreed: a repository
 * picked under GitHub's own mark arrived on the next screen drawn as `Share` —
 * three connected nodes, which in this product means a branch and in every
 * other one means "send this somewhere". The reader who had just pressed
 * "Git repository" was shown a different product's icon for the thing they had
 * chosen. One mapping, so step one and step two cannot drift again (§4).
 *
 * GitHub is spelled with its own brand mark rather than a generic git glyph,
 * for the same reason the chooser does: the identity that reaches the
 * repository is the fact the reader is checking here, and a repository on some
 * other host is genuinely a different thing. A non-GitHub remote keeps the
 * branch glyph — a made-up logo would be worse than none.
 */
export function SourceMark({
  source,
  className,
}: {
  source: DeploymentDraftSource
  className?: string
}) {
  // Each branch returns the glyph rather than choosing one into a variable:
  // a component resolved during render resets its state every pass, and the
  // React compiler's lint rule refuses it.
  switch (source.kind) {
    case "image":
      return <Box className={className} />
    case "compose":
      return <Layers className={className} />
    case "blueprint":
      return <GridMasonry className={className} />
    case "import":
      return <Inspect className={className} />
    default:
      return isGitHubSource(source) ? (
        <GitHubMark className={className} />
      ) : (
        <GitBranch className={className} />
      )
  }
}

/** Whether a git source is on github.com, which is what `githubRepo` means. */
function isGitHubSource(source: DeploymentDraftSource) {
  if (source.kind !== "git" && source.kind !== "local") return false
  // A connected repository is always GitHub — it arrived through the App —
  // and a pasted URL is one when it names the host.
  return source.mode === "connected_repository" || /github\.com/i.test(source.url ?? "")
}

/**
 * The kind of workload, for a project nothing names as a product: a website
 * is the web it answers on, a stack its layers, a worker its code. It used to
 * be the upload cloud for every website — the mark of the act of deploying,
 * drawn on the thing deployed.
 *
 * A table to index rather than a function to call: a glyph a function returns
 * is a component created during render as far as the React compiler's lint
 * rule can tell, and it refuses one drawn as a tag.
 */
export const WORKLOAD_GLYPH: Record<WorkloadProfile, Icon> = {
  web: Globe,
  static: Globe,
  worker: Code,
  image: Box,
  compose: Layers,
  service: Box,
  game: Servers,
  imported: Box,
}

/**
 * A project drawn without its website's icon: the product it is, on
 * ProductLogo's tile, and the workload's glyph on the same tile when nothing
 * names one. It was a grey button-coloured square of its own, so a template
 * and the project it became were two different tiles (§14), and a project's
 * mark changed shape the moment its favicon loaded.
 *
 * `lg` is the 48px block-rank tile a header's identity line opens with.
 */
export function WorkloadMark({
  profile,
  product,
  size = "md",
  className,
}: {
  profile: WorkloadProfile
  /** What the project is, when something names it — see `projectProduct`. */
  product?: string
  size?: "sm" | "md" | "lg"
  className?: string
}) {
  return (
    <ProductLogo
      id={product}
      size={size === "sm" ? "sm" : "md"}
      fallback={WORKLOAD_GLYPH[profile]}
      className={cn(size === "lg" && "size-12 rounded-xl [&_img]:size-7 [&_svg]:size-5", className)}
    />
  )
}

/** An image's product, or nothing when all it can say is "a Docker image". */
function namedImage(reference: string | undefined) {
  const id = reference ? imageProduct(reference) : undefined
  return id === "docker" ? undefined : id
}

/**
 * What a project *is*, as the product a reader knows it by — the mark on its
 * card, its header and its empty preview when it has no favicon of its own.
 *
 * A template is its own product (every reviewed one has a logo, keyed by the
 * id in `n8n@1.2.3`); an image is the product the image is, and Docker's
 * whale when nothing more specific is, which is at least true; a Compose
 * stack is Compose; an adopted workload is the product its containers run,
 * if any names one. A repository is what detection recognised it as — the
 * framework — and failing that what its build makes of it: the recipe's
 * language, Docker for a Dockerfile, nginx for a static site.
 *
 * `configuration` and `blueprint` are what the project pages read beside the
 * summary; they say the same things more precisely when they are in hand.
 * Nothing is guessed: a project none of these name returns nothing, and its
 * mark keeps the workload's glyph.
 */
export function projectProduct(
  summary: Pick<
    DeploymentSummary,
    | "sourceKind"
    | "sourceRef"
    | "sourceRepository"
    | "buildMethod"
    | "recipe"
    | "framework"
    | "images"
  >,
  configuration?: Pick<DeploymentEnvironmentConfiguration, "build" | "source">,
  blueprint?: Pick<BlueprintSummary, "id" | "image">,
): string | undefined {
  switch (summary.sourceKind) {
    case "blueprint": {
      const id =
        blueprint?.id ?? configuration?.source?.blueprintId ?? summary.sourceRef?.split("@")[0]
      return hasProductLogo(id) ? id : namedImage(blueprint?.image ?? summary.sourceRepository)
    }
    case "image":
      return imageProduct(
        summary.sourceRepository || configuration?.source?.image || summary.sourceRef || "",
      )
    case "compose":
      return "docker-compose"
    case "import":
      return imageProducts(summary.images ?? []).find((id) => id !== "docker")
    default: {
      const build = configuration?.build
      return (
        frameworkProduct(build?.framework ?? summary.framework) ??
        buildMethodProduct(build?.method ?? summary.buildMethod, {
          recipe: build?.recipe ?? summary.recipe,
          packageManager: build?.packageManager,
        })
      )
    }
  }
}

/**
 * Where a project comes from, as the product that holds it: the forge a
 * repository lives on (GitHub, GitLab, Codeberg — from the remote itself, or
 * the provider the source was connected through), git for a remote no forge
 * name is in, the product an image is, Compose, and the template. An adopted
 * workload came out of Docker.
 */
export function sourceProduct(
  summary: Pick<
    DeploymentSummary,
    "sourceKind" | "sourceRef" | "sourceRepository" | "sourceRemote"
  >,
  source?: Pick<DeploymentDraftSource, "url" | "provider" | "mode" | "image" | "blueprintId">,
): string | undefined {
  switch (summary.sourceKind) {
    case "git":
    case "local":
      return (
        hostProduct(summary.sourceRemote || source?.url) ??
        gitProviderProduct(source?.provider) ??
        (source?.mode === "connected_repository" ? "github" : "git")
      )
    case "image":
      return imageProduct(summary.sourceRepository || source?.image || summary.sourceRef || "")
    case "compose":
      return "docker-compose"
    case "blueprint": {
      const id = source?.blueprintId ?? summary.sourceRef?.split("@")[0]
      return hasProductLogo(id) ? id : undefined
    }
    default:
      return "docker"
  }
}

export const WORKLOAD_LABELS: Record<WorkloadProfile, string> = {
  web: "Web application",
  static: "Static website",
  worker: "Worker",
  image: "Docker image",
  compose: "Compose stack",
  service: "Template",
  game: "Game server",
  imported: "Adopted workload",
}

/**
 * How the backend's catalogue names a framework, spelled the way its own
 * documentation does. An unknown name passes through unchanged.
 */
const FRAMEWORK_LABELS: Record<string, string> = {
  nextjs: "Next.js",
  sveltekit: "SvelteKit",
  vite: "Vite",
  astro: "Astro",
  nuxt: "Nuxt",
  remix: "Remix",
  "react-router": "React Router",
  "solid-start": "SolidStart",
  "tanstack-start": "TanStack Start",
  nitro: "Nitro",
  angular: "Angular",
  nestjs: "NestJS",
  gatsby: "Gatsby",
  docusaurus: "Docusaurus",
  vitepress: "VitePress",
  eleventy: "Eleventy",
  "create-react-app": "Create React App",
  "vue-cli": "Vue CLI",
  ember: "Ember",
  parcel: "Parcel",
  express: "Express",
  fastify: "Fastify",
  hono: "Hono",
  koa: "Koa",
  elysia: "Elysia",
  hapi: "hapi",
  go: "Go",
  gin: "Gin",
  echo: "Echo",
  fiber: "Fiber",
  chi: "chi",
  gorilla: "Gorilla",
  python: "Python",
  django: "Django",
  fastapi: "FastAPI",
  flask: "Flask",
  streamlit: "Streamlit",
  gradio: "Gradio",
  rust: "Rust",
  axum: "axum",
  "actix-web": "Actix Web",
  rocket: "Rocket",
  warp: "warp",
  poem: "Poem",
  salvo: "Salvo",
  loco: "Loco",
  java: "Java",
  "spring-boot": "Spring Boot",
  quarkus: "Quarkus",
  micronaut: "Micronaut",
  javalin: "Javalin",
  ktor: "Ktor",
  helidon: "Helidon",
  vertx: "Vert.x",
  dotnet: ".NET",
  aspnet: "ASP.NET Core",
  deno: "Deno",
  fresh: "Fresh",
  php: "PHP",
  laravel: "Laravel",
  symfony: "Symfony",
  slim: "Slim",
  ruby: "Ruby",
  rails: "Rails",
  hanami: "Hanami",
  sinatra: "Sinatra",
  elixir: "Elixir",
  phoenix: "Phoenix",
  scala: "Scala",
  play: "Play",
  http4s: "http4s",
  "akka-http": "Akka HTTP",
  "pekko-http": "Pekko HTTP",
  "zio-http": "ZIO HTTP",
  clojure: "Clojure",
  ring: "Ring",
  compojure: "Compojure",
  pedestal: "Pedestal",
  "http-kit": "http-kit",
  dart: "Dart",
  dart_frog: "Dart Frog",
  shelf: "shelf",
  serverpod: "Serverpod",
  gleam: "Gleam",
  wisp: "Wisp",
  mist: "Mist",
}

/** The Language select, in the order a reader expects to find their stack. */
export const RECIPE_LABELS: [DeploymentRecipe, string][] = [
  ["node", "JavaScript / TypeScript"],
  ["python", "Python"],
  ["go", "Go"],
  ["rust", "Rust"],
  ["java", "Java / Kotlin (Maven or Gradle)"],
  ["dotnet", ".NET"],
  ["deno", "Deno"],
  ["php", "PHP (Composer, FrankenPHP)"],
  ["ruby", "Ruby (Rails, Hanami, Sinatra, Rack)"],
  ["elixir", "Elixir (Phoenix, mix release)"],
  ["scala", "Scala (sbt)"],
  ["clojure", "Clojure (Leiningen or tools.build)"],
  ["dart", "Dart (Dart Frog, shelf)"],
  ["gleam", "Gleam (Erlang shipment)"],
]

/**
 * A recipe's language in the word a reading uses — "Node.js · bun", "Python
 * 3.12". `RECIPE_LABELS` is the select's longer wording, and the new-project
 * flow keys on its strings, so the two stay separate.
 */
export const RECIPE_SHORT: Record<DeploymentRecipe, string> = {
  node: "Node.js",
  python: "Python",
  go: "Go",
  rust: "Rust",
  java: "Java",
  dotnet: ".NET",
  deno: "Deno",
  php: "PHP",
  ruby: "Ruby",
  elixir: "Elixir",
  scala: "Scala",
  clojure: "Clojure",
  dart: "Dart",
  gleam: "Gleam",
}

/** How a project is built, in the word a reading uses beside its product. */
export const BUILD_METHOD_SHORT: Record<DeploymentBuildMethod, string> = {
  recipe: "Automatic",
  dockerfile: "Dockerfile",
  static: "Static site",
  image: "Docker image",
  compose: "Compose",
  none: "No build",
  legacy_compose: "Compose",
}

export function frameworkLabel(value: string) {
  return FRAMEWORK_LABELS[value] ?? value
}

export const DATABASE_ENGINE_LABELS: Record<string, string> = {
  postgres: "PostgreSQL",
  pgvector: "PostgreSQL + pgvector",
  postgis: "PostgreSQL + PostGIS",
  mysql: "MySQL",
  mariadb: "MariaDB",
  redis: "Redis",
  mongodb: "MongoDB",
}

/** The hosted protocols a driver may speak instead of its engine's own. */
export const HOSTED_DATABASE_LABELS: Record<string, string> = {
  "neon-http": "Neon's HTTP protocol",
  "neon-ws": "Neon's WebSocket protocol",
  "vercel-postgres": "Vercel Postgres's pooled protocol",
  "planetscale-http": "PlanetScale's HTTP protocol",
  "prisma-accelerate": "Prisma Accelerate's prisma:// protocol",
  "upstash-rest": "Upstash's REST protocol",
}

export function humanize(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
}

/** "health_gate_failed" → "Health gate failed": a machine code in the product's sentence case. */
export function sentence(code: string) {
  const words = code.replaceAll("_", " ")
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** A terminal code under the status that already says it failed: "Health gate", "Activation". */
export function terminalLabel(code: string) {
  return sentence(code.replace(/_failed$/, ""))
}

export function shortIdentity(value: string) {
  if (value.startsWith("sha256:")) return `sha256:${value.slice(7, 19)}…`
  return value.length > 12 ? value.slice(0, 12) : value
}

export function shortRevision(value: string | undefined) {
  if (!value) return undefined
  return /^[0-9a-f]{12,64}$/i.test(value) ? value.slice(0, 7) : shortIdentity(value)
}

export function releaseLabel(deployment: DeploymentSummary) {
  if (!deployment.liveReleaseId) return "No live release"
  const source = deployment.sourceRevision || deployment.sourceRef
  return source ? shortIdentity(source) : `Release ${deployment.liveReleaseId}`
}

export function reachableAt(deployment: DeploymentSummary) {
  if (deployment.endpoint) return deployment.endpoint
  if (deployment.hostPort) return `:${deployment.hostPort}`
  if (deployment.internalPort) return `Internal :${deployment.internalPort}`
  return "Private"
}

export function deploymentURL(endpoint?: string): string | undefined {
  if (!endpoint) return undefined
  try {
    const url = new URL(endpoint.includes("://") ? endpoint : `https://${endpoint}`)
    if (!["https:", "http:"].includes(url.protocol) || url.username || url.password)
      return undefined
    return url.href
  } catch {
    return undefined
  }
}

export function hostOf(url: string | undefined) {
  if (!url) return undefined
  try {
    return new URL(url).host
  } catch {
    return undefined
  }
}

type SourceLine = {
  kind: "git" | "image" | "compose" | "blueprint" | "import" | "local"
  /** The branch, the image, the template — what the reader recognises the project by. */
  primary: string
  /** The commit subject when it is known, otherwise the short revision. */
  secondary?: string
  /** The secondary is a literal from the host — a sha, a digest. */
  mono?: boolean
}

/**
 * Where a project comes from, in one line, the way a Vercel card says
 * "main · Fix checkout". The commit subject arrives on the run's metadata once
 * the engine records it; until then the short revision stands in.
 */
export function sourceLine(
  deployment: DeploymentSummary,
  project?: Pick<DeployProject, "branch" | "repoPath">,
  run?: Pick<DeploymentEngineRun, "metadata"> | undefined,
): SourceLine {
  const commit = runCommit(run ?? deployment.lastRun)
  switch (deployment.sourceKind) {
    case "git":
    case "local": {
      const ref = deployment.sourceRef || project?.branch || "main"
      if (commit?.subject)
        return { kind: deployment.sourceKind, primary: ref, secondary: commit.subject }
      const revision = shortRevision(deployment.sourceRevision)
      return { kind: deployment.sourceKind, primary: ref, secondary: revision, mono: true }
    }
    case "image":
      // An image's reference is its repository; its ref is empty, and the
      // digest this fell back to named nothing a reader would recognise.
      return {
        kind: "image",
        primary:
          deployment.sourceRepository ||
          deployment.sourceRef ||
          shortIdentity(deployment.sourceRevision ?? "image"),
        mono: true,
      }
    case "compose":
      return { kind: "compose", primary: "Compose stack", secondary: deployment.sourceRef }
    case "blueprint":
      return { kind: "blueprint", primary: deployment.sourceRef || "Template" }
    default:
      return { kind: "import", primary: WORKLOAD_LABELS[deployment.profile] }
  }
}

/** What the server accepts as a deployment name, checked before it is sent. */
export const DEPLOYMENT_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

/**
 * A repository, image or template name as a deployment name. The sources a
 * name is derived from have their own rules — GitHub allows a leading dot, a
 * registry path allows slashes — so the field arrives filled in and legal, and
 * the operator edits a name rather than a validation message.
 */
export function deploymentName(raw: string) {
  const cleaned = raw
    .trim()
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    .replace(/^[^A-Za-z0-9]+/, "")
    .slice(0, 64)
  return cleaned || "app"
}

function autoDeployStopped(watch: DeploymentGitWatch) {
  switch (watch.reason) {
    case "ref_not_found":
      return `Auto-deploy stopped: ${watch.branch ?? "the branch"} no longer exists`
    case "source_auth_failed":
      return "Auto-deploy stopped: the credential was refused"
    case "source_repository_missing":
      return "Auto-deploy stopped: repository not found"
    case "source_unreachable":
      return "Auto-deploy paused: the Git remote is unreachable"
  }
  return undefined
}

/**
 * Whether a repository deploys itself, as one reading. The project header and
 * the overview's wiring each had their own copy, and they had already drifted:
 * the wiring said how often it looked and the header did not. `interval` is
 * set only while it is on, for the surface with room to say "every 5s".
 * Nothing for a source the watcher does not apply to.
 */
export function autoDeployReading(
  watch: DeploymentGitWatch,
): { tone: DotTone; label: string; interval?: number } | undefined {
  if (watch.status === "not_applicable") return undefined
  const automatic = watch.policy?.automatic ?? watch.automatic
  if (["unavailable", "stale", "policy_conflict"].includes(watch.status)) {
    // A branch the watcher could not read says why when git's answer did.
    const stopped = watch.status === "unavailable" ? autoDeployStopped(watch) : undefined
    return { tone: "warning", label: stopped ?? "Auto-deploy needs attention" }
  }
  if (!automatic) return { tone: "stopped", label: "Manual deployments" }
  if (watch.status === "awaiting_first_deployment") {
    return { tone: "stopped", label: "Auto-deploy after first deployment" }
  }
  return { tone: "running", label: "Auto-deploy on", interval: watch.intervalSeconds }
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

/**
 * A change to a saved plan, marked the way git marks a changed file — added,
 * modified, deleted — in the `--git-*` hues. The status colours are readings
 * of state (§3), and a removed variable is not a failure: drawn in the danger
 * red, a routine cleanup read as an alarm. An unchanged row takes no colour.
 */
export const CHANGE_TONE: Record<ReleaseListChange["change"], GitTone | undefined> = {
  added: "added",
  changed: "modified",
  removed: "deleted",
  unchanged: undefined,
}

export function ChangeTag({
  change,
  className,
}: {
  change: ReleaseListChange["change"]
  className?: string
}) {
  const tone = CHANGE_TONE[change]
  return (
    <Tag className={className} style={tone ? { color: `var(--git-${tone})` } : undefined}>
      {change}
    </Tag>
  )
}

/**
 * The settings page that owns each kind of pending change, as the suffix
 * after `/deploy/<id>`, so a change in the strip is a link to where it is
 * made. `deployment` — the first release — belongs to no page.
 */
export const PENDING_KIND_PAGE: Record<string, string> = {
  source: "/settings/general#source",
  build: "/settings/build",
  runtime: "/settings/runtime",
  check: "/settings/runtime#health-checks",
  variable: "/settings/variables",
  dependency: "/settings/databases",
}

// ---------------------------------------------------------------------------
// Notification channels
// ---------------------------------------------------------------------------

/** The mail services an SMTP host names, by the word it is named with. */
const MAIL_WORDS: Record<string, string> = {
  gmail: "google",
  googlemail: "google",
  google: "google",
  sendgrid: "sendgrid",
  mailgun: "mailgun",
}

export function mailProduct(host: string | undefined) {
  return host ? wordsProduct(host.toLowerCase().split(/[.-]/), MAIL_WORDS) : undefined
}

/**
 * A channel as the service it posts to: Discord, Slack and Telegram as
 * themselves; a signed webhook as the product its host names (n8n, ntfy,
 * Home Assistant) or the webhook's own mark; an e-mail channel as the mail
 * service its SMTP host names, or nothing — e-mail is a protocol and keeps a
 * glyph (§14). One resolver, so a channel is the same mark on Notifications,
 * in a rule's "tells" line and in the Add alert sheet.
 */
export function channelMark(channel: Pick<NotificationChannel, "kind" | "url" | "via">) {
  if (channel.kind === "webhook") return webhookProduct(channel.url) ?? "webhook"
  if (channel.kind === "email") return mailProduct(channel.via)
  return channelProduct(channel.kind)
}

/**
 * A channel's mark bare, inside a line or a picture's tile: its service's own
 * artwork, or the envelope for plain e-mail. A paused channel is drawn at a
 * step of opacity, without its colour.
 */
export function ChannelGlyph({
  channel,
}: {
  channel: Pick<NotificationChannel, "kind" | "url" | "via" | "enabled">
}) {
  const product = channelMark(channel)
  const dim = !channel.enabled && "opacity-50 grayscale"
  return product ? (
    <ProductGlyph id={product} className={cn(dim)} />
  ) : (
    <Envelope aria-hidden className={cn("size-3.5 shrink-0 text-muted-foreground", dim)} />
  )
}

// ---------------------------------------------------------------------------
// Runtime readings
// ---------------------------------------------------------------------------

export const ROUTE_TONE: Record<DeploymentDomainRoute["route"], DotTone> = {
  served: "running",
  missing: "warning",
  foreign: "danger",
  conflict: "danger",
  unavailable: "unknown",
}

export const ROUTE_LABEL: Record<DeploymentDomainRoute["route"], string> = {
  served: "Routed here",
  missing: "No route",
  foreign: "Another site",
  conflict: "Conflict",
  unavailable: "Not observed",
}

const CERTIFICATE_TONE: Record<DeploymentDomainRoute["certificate"], DotTone> = {
  valid: "running",
  expiring: "warning",
  expired: "danger",
  missing: "warning",
  "not requested": "stopped",
  unavailable: "unknown",
}

export const CERTIFICATE_LABEL: Record<DeploymentDomainRoute["certificate"], string> = {
  valid: "Certificate valid",
  expiring: "Certificate expiring",
  expired: "Certificate expired",
  missing: "No certificate",
  "not requested": "HTTP only",
  unavailable: "Not observed",
}

/** A domain's certificate as one status word. */
export function CertificateStatus({ domain }: { domain: DeploymentDomainRoute }) {
  return (
    <Status
      tone={CERTIFICATE_TONE[domain.certificate]}
      label={CERTIFICATE_LABEL[domain.certificate]}
    />
  )
}

/**
 * The certificate as a reading rather than a word: who issued it, drawn as
 * itself (Let's Encrypt, from the issuer's own common name), its state, and
 * how long it has left — amber while it is running out. An issuer nothing
 * names is not drawn at all.
 */
export function CertificateReading({
  domain,
  issuer: drawIssuer = true,
  className,
}: {
  domain: DeploymentDomainRoute
  /** Off where the issuer is already the row's mark, so it is not drawn twice. */
  issuer?: boolean
  className?: string
}) {
  const issuer = drawIssuer ? issuerProduct(domain.certificateIssuer) : undefined
  const days = domain.certificateDaysLeft
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-2", className)}>
      {issuer && <ProductGlyph id={issuer} />}
      <CertificateStatus domain={domain} />
      {typeof days === "number" && days > 0 && (
        <span
          className={cn(
            "numeric shrink-0 text-hint",
            domain.certificate === "expiring" ? "text-warning" : "text-muted-foreground",
          )}
        >
          {plural(days, "day")} left
        </span>
      )}
    </span>
  )
}

export const MOUNT_STATUS: Record<
  DeploymentStorageMount["status"],
  { label: string; tone: DotTone }
> = {
  present: { label: "Present", tone: "running" },
  missing: { label: "Missing", tone: "danger" },
  unavailable: { label: "Not observed", tone: "unknown" },
}

/**
 * A mount drawn as what it is: a directory on this server as the file
 * manager's folder, in the colour it was given there, and a named volume as
 * the product of the container that keeps its data in it (§14), on the tile
 * with Docker's volumes glyph when the caller cannot name one. The engine
 * decides by the same rule — an absolute source is a path, anything else a
 * volume.
 */
export function MountMark({
  source,
  product,
  className,
}: {
  source: string
  product?: string
  className?: string
}) {
  if (source.startsWith("/")) {
    const name = source.split("/").filter(Boolean).pop() ?? source
    return <FolderIcon name={name} path={source} className={cn("size-8", className)} />
  }
  return <ProductLogo id={product} size="sm" fallback={Servers} className={className} />
}

export const LINK_STATUS: Record<
  DeploymentDatabaseLink["status"],
  { label: string; tone: DotTone }
> = {
  connected: { label: "Connected", tone: "running" },
  stale: { label: "Not observed recently", tone: "warning" },
  pending: { label: "Waiting for the first deployment", tone: "unknown" },
  unavailable: { label: "Needs reconnection", tone: "danger" },
}

// ---------------------------------------------------------------------------
// The release path
// ---------------------------------------------------------------------------

export const RELEASE_GROUPS = [
  { label: "Source", keys: ["resolve_source", "acquire_source", "analyze_plan"] },
  { label: "Build", keys: ["prepare_context", "build_artifact"] },
  { label: "Release", keys: ["render_runtime", "backup_gate", "release_task"] },
  // Its own node rather than a detail of Release: a deployment publishing a
  // name for the first time spends real seconds here talking to a certificate
  // authority, and a progress line that reads "Release" throughout looks stuck.
  { label: "Certificate", keys: ["provision_certificate"] },
  { label: "Start", keys: ["start_candidate"] },
  { label: "Verify", keys: ["verify_readiness", "verify_smoke"] },
  { label: "Route", keys: ["activate", "retire_previous", "record_release", "notify"] },
] as const

/**
 * A stage's state, or `absent`: the run has steps and none of them is this
 * stage's. A run is planned with the steps it will take, so a deployment that
 * publishes no new name has no certificate step at all — and a stage read as
 * "pending" for want of steps said Certificate was waiting on every run that
 * never needed one, including the ones that had long since succeeded.
 */
export type ReleaseNodeState = DeploymentStepState | "absent"

export function groupedState(steps: DeploymentStep[], keys: readonly string[]): ReleaseNodeState {
  const states = steps.filter((step) => keys.includes(step.key)).map((step) => step.state)
  if (states.length === 0) return steps.length > 0 ? "absent" : "pending"
  for (const state of [
    "failed",
    "blocked",
    "running",
    "warning",
    "unavailable",
    "cancelled",
  ] as const) {
    if (states.includes(state)) return state
  }
  if (states.length > 0 && states.every((state) => state === "passed" || state === "skipped")) {
    return "passed"
  }
  return "pending"
}

export function stepStateLabel(state: ReleaseNodeState) {
  switch (state) {
    case "passed":
      return "Done"
    case "running":
      return "Active"
    case "blocked":
      return "Blocked"
    case "failed":
      return "Failed"
    case "warning":
      return "Warning"
    case "unavailable":
      return "Unavailable"
    case "cancelled":
      return "Cancelled"
    case "skipped":
      return "Skipped"
    case "absent":
      return "Not part of this run"
    default:
      return "Waiting"
  }
}

/**
 * The mark a node or a step draws: a tick, a dot, or a hollow ring for "not
 * yet", in one fixed box. The tick is 14px and a dot 6px, so a column of
 * stages whose names followed their marks started each name at a different
 * place. An absent stage keeps the box and draws nothing in it.
 */
export function StepMark({ state, className }: { state: ReleaseNodeState; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn("flex size-3.5 shrink-0 items-center justify-center", className)}
    >
      <StepGlyph state={state} />
    </span>
  )
}

function StepGlyph({ state }: { state: ReleaseNodeState }) {
  if (state === "passed") return <Check className="size-3.5 text-success" />
  if (state === "running") return <StatusDot tone="warning" live />
  if (state === "failed" || state === "blocked") return <StatusDot tone="danger" />
  if (state === "warning") return <StatusDot tone="warning" />
  if (state === "unavailable" || state === "cancelled" || state === "skipped")
    return <StatusDot tone="stopped" />
  if (state === "absent") return null
  return <span className="size-1.5 rounded-full border border-muted-foreground/60" />
}

/**
 * A step's name, as the engine's read model names it (`stepLabels` in
 * `backend/internal/deploy/read_model.go`), so a list's "Readiness checks" and
 * the run page's are one name rather than the key spelled out.
 */
const STEP_LABELS: Record<string, string> = {
  resolve_source: "Resolve source",
  acquire_source: "Fetch source",
  analyze_plan: "Check plan",
  prepare_context: "Prepare build context",
  build_artifact: "Build",
  render_runtime: "Render runtime",
  release_task: "Release task",
  backup_gate: "Backup check",
  provision_certificate: "Certificate",
  start_candidate: "Start new release",
  verify_readiness: "Readiness checks",
  verify_smoke: "Smoke checks",
  activate: "Switch traffic",
  retire_previous: "Retire previous release",
  record_release: "Record release",
  notify: "Notify",
  legacy_pipeline: "Compatibility pipeline",
}

export function stepName(key: string) {
  return STEP_LABELS[key] ?? sentence(key)
}

/**
 * Seconds a step took, or — while it is running — has taken so far. A step
 * that finished without recording its end, or has not started, has no figure:
 * measuring it to the present would read a passed step as still ticking.
 */
export function stepSeconds(
  step: Pick<DeploymentStep, "state" | "startedAt" | "endedAt">,
  now: number,
) {
  if (!step.startedAt || step.state === "pending") return undefined
  const start = Date.parse(step.startedAt)
  const end = step.endedAt ? Date.parse(step.endedAt) : step.state === "running" ? now : undefined
  if (end === undefined || Number.isNaN(start) || Number.isNaN(end)) return undefined
  return Math.max(0, (end - start) / 1000)
}

/** The most recent attempt of every step, in execution order. */
export function latestAttempts(steps: DeploymentStep[]) {
  const byKey = new Map<string, DeploymentStep>()
  for (const step of steps) {
    const current = byKey.get(step.key)
    if (!current || step.attempt >= current.attempt) byKey.set(step.key, step)
  }
  return [...byKey.values()].sort((a, b) => a.ordinal - b.ordinal)
}

/**
 * The screens a new project is walked through, in order.
 *
 * These are the screens the *reader* walks, which is not the same list as the
 * draft's own steps on the server (`intent` → `source` → `detection` →
 * `configuration` → `preflight`). Configure was one screen holding nine
 * sections and something over forty controls, and a spine saying "step 2 of
 * 3" over it was telling the reader they were halfway through the one screen
 * that is all of the work. The four configure screens share the server's
 * single `configuration` step between them; none of them claims a server step
 * that has not happened, which is the part that matters — `review` is where
 * preflight actually runs.
 */
export const CREATION_STEPS = [
  { key: "source", label: "Source" },
  { key: "project", label: "Project" },
  { key: "runtime", label: "Runtime" },
  { key: "variables", label: "Variables" },
  { key: "review", label: "Review" },
]

/**
 * The same walk from the far end: the first run's page draws the five screens
 * the reader came through, all done, and the deployment they are watching as
 * the step they are on — not "step 3 of 5" over a flow that has finished.
 */
export const CREATION_SPINE = [...CREATION_STEPS, { key: "deploy", label: "Deploy" }]
