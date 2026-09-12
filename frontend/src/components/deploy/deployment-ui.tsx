"use client"

import Link from "next/link"
import {
  CheckCircle,
  Clock,
  CrossCircle,
  LoaderCircle,
  Question,
  Slash,
  Warning,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type {
  DeploymentRunState,
  DeploymentStep,
  DeploymentStepState,
  DeploymentSummary,
} from "@/lib/types"

const RUN_LABELS: Record<DeploymentRunState, string> = {
  requested: "Requested",
  validating: "Validating",
  queued: "Queued",
  preparing: "Preparing",
  running: "Deploying",
  verifying: "Verifying",
  activating: "Activating",
  failed_activation: "Activation failed",
  restoring_previous: "Restoring previous",
  cancelling: "Cancelling",
  succeeded: "Succeeded",
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

export function isActiveRun(state: DeploymentRunState | undefined) {
  return state ? ACTIVE_RUN_STATES.has(state) : false
}

export function DeploymentStatus({
  state,
  className,
}: {
  state: DeploymentRunState
  className?: string
}) {
  const label = RUN_LABELS[state]
  const iconClass = "size-3.5 shrink-0"
  const icon =
    state === "succeeded" ? (
      <CheckCircle className={cn(iconClass, "text-success")} />
    ) : state === "failed" || state === "failed_activation" ? (
      <CrossCircle className={cn(iconClass, "text-destructive")} />
    ) : state === "cancelled" || state === "superseded" ? (
      <Slash className={cn(iconClass, "text-muted-foreground")} />
    ) : state === "rolled_back" ? (
      <Clock className={cn(iconClass, "text-warning")} />
    ) : (
      <LoaderCircle
        className={cn(iconClass, "animate-spin text-warning motion-reduce:animate-none")}
      />
    )
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap",
        className,
      )}
    >
      {icon}
      <span>{label}</span>
    </span>
  )
}

export function HealthStatus({ health }: { health: string }) {
  if (health === "healthy" || health === "passed") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium">
        <CheckCircle className="size-3.5 text-success" /> Healthy
      </span>
    )
  }
  if (health === "unhealthy" || health === "failed") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium text-destructive">
        <CrossCircle className="size-3.5" /> Unhealthy
      </span>
    )
  }
  if (health === "warning") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium text-warning">
        <Question className="size-3.5" /> Warning
      </span>
    )
  }
  if (health === "disabled") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <Question className="size-3.5" /> Disabled
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
      <Question className="size-3.5" /> Not observed
    </span>
  )
}

export const RELEASE_GROUPS = [
  { label: "Source", keys: ["resolve_source", "acquire_source", "analyze_plan"] },
  { label: "Build", keys: ["prepare_context", "build_artifact"] },
  { label: "Release", keys: ["render_runtime", "release_task", "backup_gate"] },
  // Its own node rather than a detail of Release: a deployment publishing a
  // name for the first time spends real seconds here talking to a certificate
  // authority, and a progress bar that reads "Release" throughout looks stuck.
  { label: "Certificate", keys: ["provision_certificate"] },
  { label: "Start", keys: ["start_candidate"] },
  { label: "Verify", keys: ["verify_readiness", "verify_smoke"] },
  { label: "Route", keys: ["activate", "retire_previous", "record_release", "notify"] },
] as const

const LEGACY_RELEASE_GROUPS = [
  { label: "Compatibility pipeline", keys: ["legacy_pipeline"] },
] as const

type ReleaseNodeState = DeploymentStepState | "pending"

function groupedState(steps: DeploymentStep[], keys: readonly string[]): ReleaseNodeState {
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

function NodeIcon({ state }: { state: ReleaseNodeState }) {
  if (state === "passed") return <CheckCircle className="size-4 text-success" />
  if (state === "failed" || state === "blocked")
    return <CrossCircle className="size-4 text-destructive" />
  if (state === "running")
    return <LoaderCircle className="size-4 animate-spin text-warning motion-reduce:animate-none" />
  if (state === "warning") return <Warning className="size-4 text-warning" />
  if (state === "unavailable" || state === "cancelled")
    return <Slash className="size-4 text-muted-foreground" />
  return <span className="size-2 rounded-full bg-muted-foreground/45" />
}

export function ReleasePath({
  steps,
  labelledBy,
}: {
  steps: DeploymentStep[]
  labelledBy?: string
}) {
  const groups = steps.some((step) => step.key === "legacy_pipeline")
    ? LEGACY_RELEASE_GROUPS
    : RELEASE_GROUPS
  return (
    <ol
      aria-label={labelledBy ? undefined : "Release path"}
      aria-labelledby={labelledBy}
      className={cn(
        "grid min-w-0 gap-x-3 gap-y-4",
        // One node per column at xl, where the rail fits on a single line. A
        // connector drawn from a node that has wrapped onto a second row points
        // at nothing, so the rail only claims to be a sequence where it is one.
        groups.length === 1 ? "grid-cols-1" : "grid-cols-2 sm:grid-cols-4 xl:grid-cols-7",
      )}
    >
      {groups.map((group, index) => {
        const state = groupedState(steps, group.keys)
        return (
          <li
            key={group.label}
            className="relative min-w-0"
            aria-current={state === "running" ? "step" : undefined}
          >
            {index > 0 && (
              <span
                aria-hidden="true"
                className="absolute top-3 right-[calc(50%+1.1rem)] hidden h-px w-[calc(100%-1.7rem)] bg-hairline xl:block"
              />
            )}
            <div className="flex min-w-0 items-center gap-2 lg:flex-col lg:gap-1.5 lg:text-center">
              <span
                className={cn(
                  "flex size-7 shrink-0 items-center justify-center rounded-full border bg-card",
                  state === "running" && "border-rule-warning ring-4 ring-warning/10",
                  (state === "failed" || state === "blocked") && "border-rule-danger",
                  state === "passed" && "border-rule-success",
                )}
              >
                <NodeIcon state={state} />
              </span>
              <span className="min-w-0">
                <span className="block truncate text-xs font-medium">{group.label}</span>
                <span className="block text-micro text-muted-foreground">
                  {stepStateLabel(state)}
                </span>
              </span>
            </div>
          </li>
        )
      })}
    </ol>
  )
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
    default:
      return "Waiting"
  }
}

export function releaseLabel(deployment: DeploymentSummary) {
  if (!deployment.liveReleaseId) return "No live release"
  const source = deployment.sourceRevision || deployment.sourceRef
  return source ? shortIdentity(source) : `Release ${deployment.liveReleaseId}`
}

export function shortIdentity(value: string) {
  if (value.startsWith("sha256:")) return `sha256:${value.slice(7, 19)}…`
  return value.length > 12 ? value.slice(0, 12) : value
}

export function reachableAt(deployment: DeploymentSummary) {
  if (deployment.endpoint) return deployment.endpoint
  if (deployment.hostPort) return `:${deployment.hostPort}`
  if (deployment.internalPort) return `Internal :${deployment.internalPort}`
  return "Private"
}

export const PROJECT_TABS = [
  ["overview", "Overview"],
  ["deployments", "Deployments"],
  ["logs", "Runtime logs"],
  ["configuration", "Configuration"],
  ["variables", "Variables"],
  ["network", "Domains & ports"],
  ["storage", "Storage & backups"],
  ["automations", "Automations"],
  ["metrics", "Metrics"],
  ["console", "Console"],
  ["players", "Players"],
  ["settings", "Server settings"],
] as const

export function ProjectTabs({
  projectId,
  active,
  pending,
  profile,
}: {
  projectId: number
  active: string
  pending: boolean
  profile: DeploymentSummary["profile"]
}) {
  return (
    <nav
      aria-label="Deployment sections"
      className="max-w-full [scrollbar-width:none] overflow-x-auto [&::-webkit-scrollbar]:hidden"
    >
      <ul className="flex w-max min-w-full gap-1 border-b border-hairline">
        {/* Console, Players and Server settings belong to a game server; an
            ordinary web deployment has a terminal in Docker instead. */}
        {PROJECT_TABS.filter(
          ([key]) => !["players", "console", "settings"].includes(key) || profile === "game",
        ).map(([key, label]) => (
          <li key={key}>
            <Link
              href={`/deploy/${projectId}?tab=${key}`}
              aria-current={active === key ? "page" : undefined}
              className={cn(
                "relative flex min-h-11 items-center gap-1.5 px-3 text-xs font-medium whitespace-nowrap text-muted-foreground focus-ring transition-colors hover:text-foreground",
                active === key &&
                  "text-foreground after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:bg-foreground",
              )}
            >
              {label}
              {pending &&
                ["configuration", "variables", "network", "storage", "automations"].includes(
                  key,
                ) && (
                  <span
                    className="size-1.5 rounded-full bg-warning"
                    aria-label="Pending deployment"
                  />
                )}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  )
}

export function humanize(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase())
}
