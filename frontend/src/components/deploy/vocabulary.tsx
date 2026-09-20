"use client"

import { useEffect, useState } from "react"
import { Box, Check, CloudUpload, Code, Layers, Servers } from "@/components/icons"
import { cn } from "@/lib/utils"
import { relativeTime } from "@/lib/format"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import type {
  DeploymentCommit,
  DeploymentEngineRun,
  DeploymentRecipe,
  DeploymentRunState,
  DeploymentRuntimeServices,
  DeploymentStep,
  DeploymentStepState,
  DeploymentSummary,
  DeployProject,
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

/** Who or what asked for the run: "by operator", "on push", "by schedule". */
export function runTriggerLine(run: Pick<DeploymentEngineRun, "trigger" | "actor">) {
  const trigger = TRIGGER_LABELS[run.trigger] ?? humanize(run.trigger)
  if (run.trigger === "manual") return run.actor ? `by ${run.actor}` : "manual"
  if (run.trigger === "git_push") return "on push"
  return run.actor && run.actor !== run.trigger ? `${trigger} · ${run.actor}` : trigger
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

export const PROJECT_LABELS: Record<ProjectState, string> = {
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
export function runtimeStopped(
  runtime: DeploymentRuntimeServices | undefined,
  liveReleaseId?: number,
) {
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

export function projectTone(state: ProjectState): DotTone {
  return PROJECT_TONES[state]
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
 * The workload's glyph. Wayfinding rather than decoration: it is the same
 * mark on the project card, the source row and the empty preview, so the eye
 * finds "the compose one" without reading.
 */
export function WorkloadMark({
  profile,
  size = "md",
  className,
}: {
  profile: WorkloadProfile
  size?: "sm" | "md"
  className?: string
}) {
  const Icon =
    profile === "compose"
      ? Layers
      : profile === "game"
        ? Servers
        : profile === "worker"
          ? Code
          : profile === "image" || profile === "service" || profile === "imported"
            ? Box
            : CloudUpload
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center rounded-md border border-hairline bg-control text-muted-foreground",
        size === "sm" ? "size-8 [&>svg]:size-4" : "size-10 [&>svg]:size-5",
        className,
      )}
    >
      <Icon />
    </span>
  )
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
]

export function frameworkLabel(value: string) {
  return FRAMEWORK_LABELS[value] ?? value
}

export const DATABASE_ENGINE_LABELS: Record<string, string> = {
  postgres: "PostgreSQL",
  mysql: "MySQL",
  mariadb: "MariaDB",
  redis: "Redis",
  mongodb: "MongoDB",
}

export function humanize(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
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

export type SourceLine = {
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
      return {
        kind: "image",
        primary: deployment.sourceRef || shortIdentity(deployment.sourceRevision ?? "image"),
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

/** "deployed 3h ago by operator" for the run that recorded the live release. */
export function liveReleaseLine(run: DeploymentEngineRun | undefined) {
  if (!run) return undefined
  const when = relativeTime(run.endedAt ?? run.requestedAt)
  return run.trigger === "manual" && run.actor
    ? `deployed ${when} by ${run.actor}`
    : `deployed ${when}`
}

// ---------------------------------------------------------------------------
// The release path
// ---------------------------------------------------------------------------

export const RELEASE_GROUPS = [
  { label: "Source", keys: ["resolve_source", "acquire_source", "analyze_plan"] },
  { label: "Build", keys: ["prepare_context", "build_artifact"] },
  { label: "Release", keys: ["render_runtime", "release_task", "backup_gate"] },
  // Its own node rather than a detail of Release: a deployment publishing a
  // name for the first time spends real seconds here talking to a certificate
  // authority, and a progress line that reads "Release" throughout looks stuck.
  { label: "Certificate", keys: ["provision_certificate"] },
  { label: "Start", keys: ["start_candidate"] },
  { label: "Verify", keys: ["verify_readiness", "verify_smoke"] },
  { label: "Route", keys: ["activate", "retire_previous", "record_release", "notify"] },
] as const

const LEGACY_RELEASE_GROUPS = [
  { label: "Compatibility pipeline", keys: ["legacy_pipeline"] },
] as const

export type ReleaseNodeState = DeploymentStepState | "pending"

export function groupedState(steps: DeploymentStep[], keys: readonly string[]): ReleaseNodeState {
  const states = steps.filter((step) => keys.includes(step.key)).map((step) => step.state)
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
    default:
      return "Waiting"
  }
}

/** The mark a node or a step draws: a tick, a dot, or a hollow ring for "not yet". */
export function StepMark({ state, className }: { state: ReleaseNodeState; className?: string }) {
  if (state === "passed")
    return <Check className={cn("size-3.5 shrink-0 text-success", className)} />
  if (state === "running") return <StatusDot tone="warning" live className={className} />
  if (state === "failed" || state === "blocked")
    return <StatusDot tone="danger" className={className} />
  if (state === "warning") return <StatusDot tone="warning" className={className} />
  if (state === "unavailable" || state === "cancelled" || state === "skipped")
    return <StatusDot tone="stopped" className={className} />
  return (
    <span
      aria-hidden="true"
      className={cn("size-1.5 shrink-0 rounded-full border border-muted-foreground/60", className)}
    />
  )
}

/**
 * The release path as one line of readings: six words, a mark before each,
 * a hairline between them. It used to be seven framed circles with rings and
 * a connector that pointed at nothing once the row wrapped; a run's progress
 * is a reading, not a diagram.
 */
export function ReleasePath({
  steps,
  labelledBy,
  className,
}: {
  steps: DeploymentStep[]
  labelledBy?: string
  className?: string
}) {
  const groups = steps.some((step) => step.key === "legacy_pipeline")
    ? LEGACY_RELEASE_GROUPS
    : RELEASE_GROUPS
  return (
    <ol
      aria-label={labelledBy ? undefined : "Release path"}
      aria-labelledby={labelledBy}
      className={cn("flex min-w-0 flex-wrap items-center gap-x-2 gap-y-2", className)}
    >
      {groups.map((group, index) => {
        const state = groupedState(steps, group.keys)
        return (
          <li
            key={group.label}
            className="flex min-w-0 items-center gap-2"
            aria-current={state === "running" ? "step" : undefined}
          >
            {index > 0 && <span aria-hidden="true" className="h-px w-3 shrink-0 bg-hairline" />}
            <span className="inline-flex items-center gap-1.5 text-xs whitespace-nowrap">
              <StepMark state={state} />
              <span
                className={cn(
                  "font-medium",
                  state === "pending" && "text-muted-foreground",
                  (state === "failed" || state === "blocked") && "text-destructive",
                )}
              >
                {group.label}
              </span>
              <span className="sr-only">{stepStateLabel(state)}</span>
            </span>
          </li>
        )
      })}
    </ol>
  )
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
 * The three steps of making a project, as the reader walks them.
 *
 * They are the draft's own `source` → `configuration` → commit sequence
 * (`new-project/draft.ts`) rather than a decorative count, so the spine cannot
 * drift from the state machine underneath it.
 *
 * It lives here because two screens draw it from opposite ends: `/deploy/new`
 * while the reader is in it, and the first run's page once the commit has
 * happened. It was written out twice, with a comment on each copy saying it
 * had to agree with the other word for word — which is the shape of a label
 * that is about to disagree.
 */
export const CREATION_STEPS = [
  { key: "source", label: "Source" },
  { key: "configure", label: "Configure" },
  { key: "deploy", label: "Deploy" },
]
