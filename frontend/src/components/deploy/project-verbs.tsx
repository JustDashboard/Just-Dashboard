"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Archive,
  ArrowCircleUp,
  ArrowRight,
  Box,
  BranchPlus,
  Copy,
  External,
  GitTag,
  GridSquare,
  Layers,
  Logs,
  Play,
  RefreshClockwise,
  RotateClockwise,
  RotateCounterClockwise,
  Servers,
  SettingsGear,
  StopCircle,
  Trash,
} from "@/components/icons"
import { del, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import type { DeploymentEngineRun, DeploymentRuntimeServices, DeploymentSummary } from "@/lib/types"
import type { ConfirmRequest, useConfirm } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"
import { FormFact } from "@/components/form"
import { SourcePull } from "@/components/git/glyphs"
import { ProductGlyph } from "@/components/product-logo"
import type { ProjectOperation } from "@/components/deploy/project-context"
import { ProjectMark } from "@/components/deploy/project-mark"
import { runPlanIsStale } from "@/components/deploy/failure-cause"
import {
  deploymentURL,
  hostOf,
  isCancellable,
  isRetryable,
  projectState,
  runFailed,
  sourceLine,
  sourceProduct,
} from "@/components/deploy/vocabulary"

type Confirm = ReturnType<typeof useConfirm>["confirm"]

/** What to say when the request that starts a run never reaches the server. */
const START_FAILURE: Record<ProjectOperation, string> = {
  deploy: "Could not start the deployment",
  redeploy: "Could not start the redeploy",
  restart: "Could not restart the application",
  force_build: "Could not start the rebuild",
  stop: "Could not stop the application",
  start: "Could not start the application",
}

/**
 * Enqueue a run for a project and open its page — for a surface that has the
 * project's summary but not the project page's context, such as a card on
 * the projects grid. `starting` is the operation whose request is in flight,
 * for the present participle while it is (§13).
 *
 */
export function useProjectStart(
  summary: Pick<DeploymentSummary, "id" | "environmentId"> & Partial<DeploymentSummary>,
  refresh: () => void,
) {
  const router = useRouter()
  const [starting, setStarting] = useState<ProjectOperation>()
  const start = async (operation: ProjectOperation) => {
    setStarting(operation)
    try {
      const run = await post<DeploymentEngineRun>(
        `/deploy/${summary.id}/environments/${summary.environmentId}/runs`,
        { operation },
      )
      router.push(`/deploy/${summary.id}/runs/${run.id}`)
    } catch (error) {
      notify.error(START_FAILURE[operation], error)
      setStarting(undefined)
      refresh()
    }
  }
  return { start, starting, gate: null }
}

/** Docker drawn as itself in a menu's glyph slot, for the verb that opens its page. */
function DockerGlyph({ className }: { className?: string }) {
  return <ProductGlyph id="docker" className={className} />
}

const COMMANDS = new Set(["view", "start", "deploy", "promote", "redeploy"])

/**
 * The project's one command: the verb a header draws as its button, and every
 * other verb goes in the menu beside it. It is the first of View deployment,
 * Start, Deploy and Redeploy in the list, which is ordered so that while
 * changes are waiting "Deploy changes" comes before "Redeploy live release".
 */
export function projectCommand(verbs: Verb[]) {
  return verbs.find((verb) => COMMANDS.has(verb.key))
}

/**
 * What can be done to a project, declared once (§13).
 *
 * The projects grid and the project's own header each had a list, and they
 * disagreed: the card could only redeploy, the header could stop, rebuild and
 * duplicate but not cancel or retry. Both read this now, each verb with its
 * capability applied, grouped as the menu shows them — what keeps it running
 * (start, restart, stop), what builds a release (deploy, redeploy, rebuild,
 * retry, a specific version), the project (where to go, what to copy), then
 * the danger zone under its own rule.
 *
 * `start` is how a run is enqueued: the project page passes its context's, so
 * the header's button and the menu share one request in flight, and a card
 * passes `useProjectStart`'s. `navigation` adds the ways into the project and
 * Visit inline, for a surface that is not already inside it. `runtime` names
 * the live containers for Open in Docker; `onVersion`, `onTestPull` and
 * `onDuplicate` open the dialogs the page owns. Archiving leaves by
 * `onArchived`, which is the list's refresh unless the caller has somewhere
 * to go.
 *
 * Staging is the same project read through its other environment, so the
 * summary decides which verbs make sense: the project-wide ones (duplicate,
 * test a pull request, archive) stay with production, staging adds Promote
 * and its own removal, and a production that takes only promotions offers
 * Promote where it would offer a build. `onPromote`, `onAddStaging` and
 * `onRemoveStaging` open the dialogs the shell owns for them.
 */
export function useProjectVerbs(
  summary: DeploymentSummary,
  {
    confirm,
    refresh,
    start,
    starting,
    runtime,
    archived = false,
    navigation = false,
    onVersion,
    onTestPull,
    onDuplicate,
    onArchived,
    hasStaging = false,
    promotionOnly = false,
    onPromote,
    onAddStaging,
    onRemoveStaging,
  }: {
    confirm: Confirm
    refresh: () => void
    start: (operation: ProjectOperation) => void | Promise<void>
    starting?: ProjectOperation
    runtime?: DeploymentRuntimeServices
    archived?: boolean
    navigation?: boolean
    onVersion?: () => void
    onTestPull?: () => void
    onDuplicate?: () => void
    onArchived?: () => void
    /** The deployment has a staging environment. */
    hasStaging?: boolean
    /** Production refuses builds and takes only promotions from staging. */
    promotionOnly?: boolean
    onPromote?: () => void
    onAddStaging?: () => void
    onRemoveStaging?: () => void
  },
): Verb[] {
  const router = useRouter()
  const { can } = useAuth()
  const [working, setWorking] = useState<"cancel" | "retry" | "wake">()
  const base = `/deploy/${summary.id}`
  const url = deploymentURL(summary.endpoint)
  const state = projectState(summary, runtime, archived)
  const live = Boolean(summary.liveReleaseId)
  // The compatibility pipeline takes only "deploy"; the engine's other
  // operations belong to the projects it runs.
  const normalized = summary.buildMethod !== "legacy_compose"
  const control = can("service.control") && !archived
  const canRun = control && normalized
  const staging = summary.environmentKind === "staging"
  // A build is refused in a production that takes only promotions, so the
  // verbs that would ask for one are not offered there at all.
  const builds = !promotionOnly || staging
  const active = summary.activeRun
  const last = summary.lastRun
  const busy = Boolean(active) || Boolean(starting) || Boolean(working)

  const cancel = async (run: DeploymentEngineRun) => {
    setWorking("cancel")
    try {
      await post(`${base}/runs/${run.id}/cancel`, {})
      notify.success(`Cancelling ${summary.name}`, {
        description: "The run page will show cleanup progress.",
      })
      refresh()
    } catch (error) {
      notify.error("Could not cancel deployment", error)
    } finally {
      setWorking(undefined)
    }
  }

  // Waking is what the next visitor's request would do; it waits for the
  // application to pass its readiness checks, so the press reads the outcome.
  const wake = async () => {
    setWorking("wake")
    try {
      await post(`${base}/environments/${summary.environmentId}/sleep/wake`, {})
      notify.success(`${summary.name} is awake`)
      refresh()
    } catch (error) {
      notify.error("Could not wake the deployment", error)
    } finally {
      setWorking(undefined)
    }
  }

  const retry = async (run: DeploymentEngineRun) => {
    setWorking("retry")
    try {
      const created = await post<DeploymentEngineRun>(`${base}/runs/${run.id}/retry`, {})
      router.push(`${base}/runs/${created.id}`)
    } catch (error) {
      notify.error("Could not retry deployment", error)
      setWorking(undefined)
    }
  }

  const runVerb = (
    key: ProjectOperation,
    label: string,
    icon: Verb["icon"],
    progressive: string,
  ): Verb => ({
    key,
    label,
    icon,
    progressive,
    group: RUNNING.has(key) ? "Running" : "Building",
    disabled: busy,
    run: () => void start(key),
  })

  const source = sourceLine(summary)
  const project: ProjectSubject = {
    id: summary.id,
    mark: <ProjectMark deployment={summary} size="sm" />,
    name: summary.name,
    facts: (
      <>
        {url && (
          <FormFact label="Answers at" mono>
            {hostOf(url)}
          </FormFact>
        )}
        <FormFact label="Source" mono={source.mono || Boolean(summary.sourceRepository)}>
          {summary.sourceRepository || source.primary}
        </FormFact>
      </>
    ),
  }

  const verbs: Verb[] = []
  if (navigation && url) {
    verbs.push({
      key: "visit",
      label: "Visit",
      icon: External,
      inline: true,
      run: () => window.open(url, "_blank", "noopener,noreferrer"),
    })
  }

  if (active) {
    verbs.push({
      key: "view",
      label: "View deployment",
      icon: ArrowRight,
      group: "Building",
      run: () => router.push(`${base}/runs/${active.id}`),
    })
    if (control && isCancellable(active.state) && !active.cancelRequested) {
      verbs.push({
        key: "cancel",
        label: "Cancel deployment",
        icon: StopCircle,
        group: "Building",
        progressive: "Cancelling…",
        disabled: Boolean(working),
        run: () => void cancel(active),
      })
    }
  } else if (control) {
    if (state === "stopped") {
      verbs.push(runVerb("start", "Start", Play, "Starting…"))
    } else if (state === "asleep") {
      verbs.push({
        key: "wake",
        label: "Wake now",
        icon: Play,
        group: "Running",
        progressive: "Waking…",
        disabled: busy,
        run: () => void wake(),
      })
    } else if (!builds) {
      if (onPromote) {
        verbs.push({
          key: "promote",
          label: "Promote from staging…",
          icon: ArrowCircleUp,
          group: "Building",
          run: onPromote,
        })
      }
      if (live) verbs.push(runVerb("redeploy", "Redeploy", RefreshClockwise, "Redeploying…"))
    } else if (!live || summary.pendingChanges || !normalized) {
      verbs.push(
        runVerb(
          "deploy",
          !live ? "Deploy" : summary.pendingChanges ? "Deploy changes" : "Redeploy",
          live ? RefreshClockwise : Play,
          "Deploying…",
        ),
      )
    } else {
      verbs.push(runVerb("redeploy", "Redeploy", RefreshClockwise, "Redeploying…"))
    }
  }

  if (canRun && live) {
    // Stopping or restarting takes the service down, so like the Docker
    // container verbs it needs the destructive capability; starting a
    // stopped deployment only needs service control.
    if (state !== "stopped" && can("destructive")) {
      // Each interrupts whoever is using the site, and each is one press from
      // a fleet card's menu, so each asks first — as the container verbs do.
      const asking = (verb: Verb, operation: ProjectOperation, sentence: string): Verb => ({
        ...verb,
        run: () =>
          confirm({
            title: `${verb.label} ${summary.name}?`,
            confirmLabel: verb.label,
            subject: project,
            description: <p>{sentence}</p>,
            action: async () => {
              await start(operation)
            },
          }),
      })
      verbs.push(
        asking(
          runVerb("restart", "Restart", RotateClockwise, "Restarting…"),
          "restart",
          "The live release stops and starts again. Visitors get an error until it answers.",
        ),
        asking(
          runVerb("stop", "Stop", StopCircle, "Stopping…"),
          "stop",
          "The live containers stop. Visitors get an error until you start it again.",
        ),
      )
    }
    if (summary.pendingChanges && builds) {
      verbs.push(runVerb("redeploy", "Redeploy live release", RefreshClockwise, "Redeploying…"))
    }
    // Promoting changes production, so it takes what deploying production
    // takes; staging's own live release is what it offers first.
    if (staging && onPromote) {
      verbs.push({
        key: "promote",
        label: "Promote to production…",
        icon: ArrowCircleUp,
        group: "Building",
        run: onPromote,
      })
    }
  }
  if (control && !active && last && runFailed(last.state) && isRetryable(last.state)) {
    // A retry replays the plan and variables the run used; once newer
    // settings are saved, the name says so, since Deploy is what uses them.
    const stale = runPlanIsStale(last, summary)
    verbs.push({
      key: "retry",
      label: stale ? "Retry with the settings it used" : "Retry",
      icon: RotateCounterClockwise,
      group: "Building",
      progressive: "Retrying…",
      disabled: busy,
      run: () => void retry(last),
    })
  }
  if (canRun && builds) {
    verbs.push(runVerb("force_build", "Rebuild without cache", Box, "Rebuilding…"))
  }
  if (canRun && builds && summary.sourceKind === "git" && onVersion) {
    verbs.push({
      key: "version",
      label: "Deploy a specific version…",
      icon: GitTag,
      group: "Building",
      disabled: busy,
      run: onVersion,
    })
  }
  // Testing a pull request is the administrator's approval of its head, so
  // it takes the capability approving a preview does, not service control;
  // and it builds in the preview's own environment, so a production run in
  // flight is no reason to hold it back.
  if (
    can("system.admin") &&
    !archived &&
    !staging &&
    summary.sourceKind === "git" &&
    sourceProduct(summary) === "github" &&
    onTestPull
  ) {
    verbs.push({
      key: "test-pull",
      label: "Test a pull request…",
      icon: SourcePull,
      group: "Building",
      run: onTestPull,
    })
  }

  if (navigation) {
    // The rail's own glyphs for the same pages, so the menu and the rail
    // point at one place with one mark.
    verbs.push(
      {
        key: "open",
        label: "Open",
        icon: GridSquare,
        group: "Project",
        run: () => router.push(base),
      },
      {
        key: "deployments",
        label: "Deployments",
        icon: Layers,
        group: "Project",
        run: () => router.push(`${base}/deployments`),
      },
      {
        key: "logs",
        label: "Logs",
        icon: Logs,
        group: "Project",
        run: () => router.push(`${base}/logs`),
      },
      {
        key: "runtime",
        label: "Runtime",
        icon: Servers,
        group: "Project",
        run: () => router.push(`${base}/runtime`),
      },
      {
        key: "settings",
        label: "Settings",
        icon: SettingsGear,
        group: "Project",
        run: () => router.push(`${base}/settings/general`),
      },
    )
  }
  if (
    can("system.admin") &&
    !archived &&
    !staging &&
    !hasStaging &&
    normalized &&
    summary.sourceKind !== "compose" &&
    summary.buildMethod !== "compose" &&
    onAddStaging
  ) {
    verbs.push({
      key: "add-staging",
      label: "Add staging environment…",
      icon: BranchPlus,
      group: "Project",
      run: onAddStaging,
    })
  }
  if (can("system.admin") && !archived && !staging && onDuplicate) {
    verbs.push({
      key: "duplicate",
      label: "Duplicate project…",
      icon: Copy,
      group: "Project",
      run: onDuplicate,
    })
  }
  if (runtime?.status === "available" && runtime.services.length > 0) {
    const container = (
      runtime.services.find((service) => service.liveRelease) ?? runtime.services[0]
    ).containerId
    verbs.push({
      key: "docker",
      label: "Open in Docker",
      icon: DockerGlyph,
      group: "Project",
      run: () => router.push(`/docker/containers?${new URLSearchParams({ container })}`),
    })
  }

  if (can("destructive") && !archived && staging && onRemoveStaging) {
    verbs.push({
      key: "remove-staging",
      label: "Remove staging…",
      icon: Trash,
      danger: true,
      disabled: busy,
      run: onRemoveStaging,
    })
  }
  if (can("destructive") && !archived && !staging) {
    verbs.push({
      key: "archive",
      label: "Archive deployment",
      icon: Archive,
      danger: true,
      progressive: "Archiving…",
      disabled: busy,
      run: () => confirm(archiveRequest(project, { onDone: onArchived ?? refresh })),
    })
  }
  if (can("destructive") && archived) {
    verbs.push({
      key: "purge",
      label: "Delete permanently",
      icon: Trash,
      danger: true,
      run: () =>
        confirm(purgeRequest(project, { onDone: () => router.push("/deploy?view=archived") })),
    })
  }
  // Declared where each is decided, drawn by group: the menu labels a group
  // each time it changes, so Restart and Stop declared after the command
  // would split Building in two. The sort is stable, so each group keeps its
  // order — and the command stays the first of its keys.
  return verbs.sort((a, b) => verbRank(a) - verbRank(b))
}

/** A project as a confirmation draws it: its mark, its name, and what tells it apart. */
export type ProjectSubject = {
  id: number
  name: string
  mark: React.ReactNode
  facts?: React.ReactNode
}

/**
 * Archiving a project, asked the same way wherever it is offered — the
 * header's menu, a fleet card's, the Danger zone. `reach` is what the caller
 * has read about what archiving turns off and what it keeps.
 */
export function archiveRequest(
  project: ProjectSubject,
  { reach, onDone }: { reach?: React.ReactNode; onDone?: () => void },
): ConfirmRequest {
  return {
    title: `Archive ${project.name}`,
    confirmLabel: "Archive deployment",
    subject: project,
    description: (
      <>
        <p>It leaves the active list and stops deploying by itself. Its history is kept.</p>
        <p>
          Running containers, routes, and persistent data remain until you remove them from Settings
          → Danger zone.
        </p>
        {reach}
      </>
    ),
    action: async () => {
      await post(`/deploy/${project.id}/archive`, {})
    },
    onDone,
  }
}

/**
 * Deleting an archived project for good, asked the same way wherever it is
 * offered — the header's menu, the archive's row, the Danger zone. It is rare
 * and cannot be undone, so the name is typed first. What goes and what stays
 * are two lists, because they are the facts the decision is made on.
 */
export function purgeRequest(
  project: ProjectSubject,
  { variables, releases, onDone }: { variables?: number; releases?: number; onDone?: () => void },
): ConfirmRequest {
  return {
    title: `Delete ${project.name} permanently`,
    confirmLabel: "Delete permanently",
    phrase: project.name,
    subject: project,
    description: (
      <div className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <p className="eyebrow text-destructive">Deleted for good</p>
            <ul className="space-y-1 text-body">
              <li>Saved configuration</li>
              <li>{variables === undefined ? "Variables" : plural(variables, "variable")}</li>
              <li>
                {releases === undefined ? "Release history" : plural(releases, "release")} and
                deployment logs
              </li>
            </ul>
          </div>
          <div className="space-y-1.5">
            <p className="eyebrow">Stays on the server</p>
            <ul className="space-y-1 text-body">
              <li>Running containers and routes</li>
              <li>Images and files</li>
              <li>Persistent data</li>
            </ul>
          </div>
        </div>
        <p className="text-muted-foreground">
          The dashboard forgets that it owns what stays, and its persistent data remain on the
          server. To remove them too, use{" "}
          <Link
            href={`/deploy/${project.id}/settings/danger`}
            className="rounded-sm text-foreground underline-offset-2 focus-ring hover:underline"
          >
            Settings → Danger zone
          </Link>{" "}
          first. This cannot be undone.
        </p>
      </div>
    ),
    action: async (phrase) => {
      await del(`/deploy/${project.id}/permanent`, { confirm: phrase })
    },
    onDone,
  }
}

/** What keeps the release serving; everything else in the run's groups builds one. */
const RUNNING = new Set<ProjectOperation>(["start", "restart", "stop"])

const GROUP_RANK: Record<string, number> = { Running: 1, Building: 2, Project: 3 }

/** Inline verbs first, then Running, Building and Project, then the danger zone. */
function verbRank(verb: Verb) {
  return verb.danger ? 4 : verb.group ? (GROUP_RANK[verb.group] ?? 3) : 0
}
