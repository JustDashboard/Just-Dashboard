import { plural } from "@/lib/format"
import type { DotTone } from "@/components/tone"
import type {
  SmartStackAccess,
  SmartStackAction,
  SmartStackCatalogueEntry,
  SmartStackCatalogueService,
  SmartStackCredential,
  SmartStackPreset,
  SmartStackReadiness,
  SmartStackService,
  SmartStackStage,
  SmartStackState,
  SmartStackStatus,
  SmartStackStep,
  SmartStackStepStatus,
} from "@/lib/types"

/**
 * The words and reads of a smart stack, for the Stacks tab on `/deploy/new`
 * and a stack project's own page, kept apart from the components so they are
 * unit-tested (stack-model.test.js) and so the two surfaces say one thing.
 *
 * The rule the wording keeps: a stack is never called configured, connected
 * or ready on anything but the server's own word for it. Installed, healthy,
 * connected, able to fetch and able to play are five different facts.
 */

/** How a service is reached, as one word on its row. */
export const ACCESS_WORD: Record<SmartStackAccess, string> = {
  public: "Public",
  private: "Private",
  worker: "Background",
}

const ACCESS_MEANING: Record<SmartStackAccess, string> = {
  public: "public is on the project's address",
  private: "private is on this server only (127.0.0.1)",
  worker: "background has no web interface",
}

/** A memory figure as the tab says it: megabytes under a gigabyte, else gigabytes. */
export function memoryWords(megabytes: number): string {
  if (megabytes < 1024) return `${megabytes} MB`
  const gigabytes = Math.round((megabytes / 1024) * 10) / 10
  return `${gigabytes} GB`
}

/** The preset's services in its own order, each as the catalogue describes it. */
export function presetServices(
  entry: Pick<SmartStackCatalogueEntry, "services">,
  preset: Pick<SmartStackPreset, "services">,
): SmartStackCatalogueService[] {
  const known = new Map(entry.services.map((service) => [service.name, service]))
  return preset.services.flatMap((name) => {
    const service = known.get(name)
    return service ? [service] : []
  })
}

/** The preset the tab opens on: the one asked for, else the recommended, else the first. */
export function chosenPreset(
  entry: Pick<SmartStackCatalogueEntry, "presets">,
  id: string | undefined,
): SmartStackPreset | undefined {
  return (
    entry.presets.find((preset) => preset.id === id) ??
    entry.presets.find((preset) => preset.recommended) ??
    entry.presets[0]
  )
}

/** What a preset asks of the server, in one sentence. Media is not in the estimate. */
export function footprintWords(preset: Pick<SmartStackPreset, "memoryMb" | "diskGb">): string {
  return `Memory limits add up to ${memoryWords(preset.memoryMb)}, and the applications' own data takes about ${preset.diskGb} GB of disk. Your media takes what you add on top.`
}

const ACCESS_ORDER = ["public", "private", "worker"] as const

/** How many of each kind of access a set of services has: "1 public, 6 private, 1 background". */
export function accessCounts(services: Pick<SmartStackCatalogueService, "access">[]): string {
  return ACCESS_ORDER.map((access) => {
    const count = services.filter((service) => service.access === access).length
    return count > 0 ? `${count} ${ACCESS_WORD[access].toLowerCase()}` : undefined
  })
    .filter(Boolean)
    .join(", ")
}

/** The counts, then what each word on the rows means, for the kinds this set has. */
export function accessSentence(services: Pick<SmartStackCatalogueService, "access">[]): string {
  const meanings = ACCESS_ORDER.filter((access) =>
    services.some((service) => service.access === access),
  ).map((access) => ACCESS_MEANING[access])
  if (meanings.length === 0) return ""
  return `${capital(accessCounts(services))}: ${meanings.join(", ")}.`
}

// ---------------------------------------------------------------------------
// The stack's page
// ---------------------------------------------------------------------------

/** The overall state as a dot and a word. Only `ready` is said as ready. */
export const STACK_STATUS: Record<SmartStackStatus, { tone: DotTone; label: string }> = {
  waiting: { tone: "unknown", label: "Not installed yet" },
  configuring: { tone: "notice", label: "Setting up" },
  ready: { tone: "running", label: "Ready" },
  action_required: { tone: "warning", label: "Needs you" },
  failed: { tone: "danger", label: "Setup failed" },
}

/** How often the page asks again: a pass or a first release moves every second or two. */
export function stackPollInterval(status: SmartStackStatus | undefined): number {
  return status === "waiting" || status === "configuring" ? 2_000 : 15_000
}

export type ReadinessLine = { key: keyof SmartStackReadiness; met: boolean; label: string }

const READINESS: [key: keyof SmartStackReadiness, met: string, unmet: string][] = [
  ["installed", "Installed", "Not installed"],
  ["healthy", "Every service healthy", "Not every service is healthy"],
  ["connected", "Services connected", "Services not connected"],
  ["acquisition", "Can fetch new titles", "Cannot fetch new titles"],
  ["playback", "Can play your library", "Cannot play yet"],
]

/**
 * The five facts, each in its own words whichever way it stands. Nothing
 * after "installed" is claimed for a stack with no live release: its
 * containers do not exist, so "not healthy" would be a reading of nothing.
 */
export function readinessLines(readiness: SmartStackReadiness): ReadinessLine[] {
  const lines = READINESS.map(([key, met, unmet]) => ({
    key,
    met: readiness[key],
    label: readiness[key] ? met : unmet,
  }))
  return readiness.installed ? lines : lines.slice(0, 1)
}

/** What only the person can do, the ones that hold the stack from ready first. */
export function orderedActions(actions: SmartStackAction[]): SmartStackAction[] {
  return [...actions.filter((action) => action.blocking), ...actions.filter((a) => !a.blocking)]
}

const STAGES: [stage: SmartStackStage, title: string][] = [
  ["readiness", "Waiting for the applications to answer"],
  ["initialize", "Setting up each application"],
  ["link", "Connecting them to each other"],
  ["verify", "Checking the connections"],
]

export type StageGroup = {
  stage: SmartStackStage
  title: string
  /** The steps that ran or will run; skipped ones are said once, by `skippedLine`. */
  steps: SmartStackStep[]
  done: number
}

/** The steps by stage, in the order a pass runs them, with no stage that has nothing to show. */
export function stageGroups(steps: SmartStackStep[]): StageGroup[] {
  return STAGES.flatMap(([stage, title]) => {
    const shown = steps.filter((step) => step.stage === stage && step.status !== "skipped")
    if (shown.length === 0) return []
    return [
      { stage, title, steps: shown, done: shown.filter((step) => step.status === "done").length },
    ]
  })
}

/**
 * Every skipped step in one line, by reason. A preset without the managers
 * skips a dozen steps for one reason, and a row for each would be a list of
 * things that did not happen.
 */
export function skippedLine(steps: SmartStackStep[]): string | undefined {
  const skipped = steps.filter((step) => step.status === "skipped")
  if (skipped.length === 0) return undefined
  const reasons = new Map<string, number>()
  for (const step of skipped) {
    const reason = sentenceEnd(step.message?.trim() || "no reason given")
    reasons.set(reason, (reasons.get(reason) ?? 0) + 1)
  }
  const said =
    reasons.size === 1
      ? [...reasons.keys()][0]
      : [...reasons].map(([reason, count]) => `${reason} (${count})`).join("; ")
  return `${plural(skipped.length, "step")} skipped: ${said}.`
}

function sentenceEnd(text: string) {
  return text.replace(/[.\s]+$/, "")
}

/** The steps whose error the page shows in full. */
export function failedSteps(steps: SmartStackStep[]): SmartStackStep[] {
  return steps.filter((step) => step.status === "failed")
}

export const STEP_STATUS: Record<
  Exclude<SmartStackStepStatus, "skipped">,
  { tone: DotTone; label: string }
> = {
  pending: { tone: "unknown", label: "Waiting" },
  running: { tone: "notice", label: "Running" },
  done: { tone: "running", label: "Done" },
  failed: { tone: "danger", label: "Failed" },
  action_required: { tone: "warning", label: "Needs you" },
}

/** The line under a step's title: its error, what it did, or how often it has tried. */
export function stepDetail(step: SmartStackStep): string | undefined {
  if (step.status === "failed") return step.error || step.message || "Failed with no error recorded"
  if (step.message) return step.message
  if (step.status === "running" && step.attempts > 1) return `Attempt ${step.attempts}`
  return undefined
}

/**
 * A service's state as one dot and word: what the stack's setup says of it
 * when that needs saying, else what Docker says of its container.
 */
export function serviceState(service: Pick<SmartStackService, "status" | "container">): {
  tone: DotTone
  label: string
} {
  if (service.status === "failed") return { tone: "danger", label: "Setup failed" }
  if (service.status === "action_required") return { tone: "warning", label: "Needs you" }
  const container = service.container
  if (!container) return { tone: "unknown", label: "No container" }
  if (container.state !== "running") {
    const tone: DotTone =
      container.state === "restarting"
        ? "warning"
        : container.state === "exited" || container.state === "dead"
          ? "danger"
          : "stopped"
    return { tone, label: capital(container.state) }
  }
  if (container.health === "unhealthy") return { tone: "danger", label: "Unhealthy" }
  if (container.health === "starting") return { tone: "notice", label: "Starting" }
  if (service.status === "configuring") return { tone: "notice", label: "Being set up" }
  return { tone: "running", label: container.health === "healthy" ? "Healthy" : "Running" }
}

function capital(word: string) {
  return word ? word[0].toUpperCase() + word.slice(1) : word
}

/** The port of a published address: "127.0.0.1:23950" and "[::1]:23950" are 23950. */
export function addressPort(address: string | undefined): number | undefined {
  const match = /:(\d{1,5})$/.exec(address ?? "")
  const port = match ? Number(match[1]) : NaN
  return port > 0 && port < 65536 ? port : undefined
}

/**
 * How a private service is opened from another computer: a tunnel to the
 * loopback address it is published on. `host` is the name this dashboard was
 * opened at, which is the server; the account is the reader's own.
 */
export function tunnelFor(
  address: string | undefined,
  host: string,
): { command: string; url: string } | undefined {
  const port = addressPort(address)
  if (!port) return undefined
  return {
    command: `ssh -L ${port}:127.0.0.1:${port} user@${host || "your-server"}`,
    url: `http://127.0.0.1:${port}`,
  }
}

/** The accounts that exist to be revealed, and how many are still to be made. */
export function splitCredentials(credentials: SmartStackCredential[]) {
  return {
    stored: credentials.filter((credential) => credential.stored),
    pending: credentials.filter((credential) => !credential.stored).length,
  }
}

/**
 * A refusal to start a pass, in words: `stack_busy` and `stack_waiting`.
 * Anything else is the caller's to report as an error, `stack_not_preset`
 * included, which the page never meets: a project whose source left the
 * stack's presets is not drawn as a stack.
 */
export function configureRefusal(code: string | undefined): string | undefined {
  if (code === "stack_busy") return "A setup pass is already running. This page follows it."
  if (code === "stack_waiting")
    return "There is no live release yet, so there is nothing to set up. A pass starts by itself once the deployment's run has brought the release up."
  return undefined
}

/** The verb of the one button: a failed pass is retried, anything else is repaired. */
export function configureVerb(status: SmartStackStatus): { label: string; progressive: string } {
  return status === "failed"
    ? { label: "Retry", progressive: "Retrying…" }
    : { label: "Repair", progressive: "Starting…" }
}

/** Whether a pass can be asked for now: not before a release is live, not during one. */
export function canConfigure(state: Pick<SmartStackState, "status">): boolean {
  return state.status !== "waiting" && state.status !== "configuring"
}
