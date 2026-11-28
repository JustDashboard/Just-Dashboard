import { plural } from "@/lib/format"
import { passwordChecks } from "@/lib/password"
import type { DotTone } from "@/components/tone"
import type {
  SmartStackAction,
  SmartStackAddresses,
  SmartStackCatalogueEntry,
  SmartStackCatalogueService,
  SmartStackMissingAddress,
  SmartStackPreset,
  SmartStackReadiness,
  SmartStackService,
  SmartStackStage,
  SmartStackState,
  SmartStackStatus,
  SmartStackStep,
  SmartStackStepStatus,
  SmartStackUpgrade,
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

/** A DNS label is at most this long, so a sibling name cuts the domain's first label to fit. */
const LABEL_MAX = 63

/**
 * The address a stack's service gets beside its domain, by the server's rule
 * (`blueprint.ServiceHostname`): a sibling of the domain's first label,
 * `media.example.com` and `requests` making `media-requests.example.com`, so
 * one wildcard certificate, the just-dashboard.com app domain and sslip.io all
 * cover it. An apex such as `example.com` has no first label to join, so the
 * service's label goes in front of it instead. A first label that would make
 * the joined one longer than a DNS label is cut from its end.
 */
export function serviceHostname(domain: string, rawLabel: string): string {
  const name = domain.trim().toLowerCase().replace(/\.$/, "")
  const label = rawLabel.trim().toLowerCase()
  const labels = name.split(".")
  if (!name || !label) return ""
  if (labels.length < 3) return `${label}.${name}`
  // As the server's: a long first label gives way at its end, never ending
  // in the hyphen a cut can leave.
  const room = LABEL_MAX - label.length - 1
  let first = labels[0]
  if (first.length > room) first = first.slice(0, Math.max(room, 1)).replace(/-+$/, "")
  return [`${first}-${label}`, ...labels.slice(1)].join(".")
}

export type PlannedAddress = { service: string; title: string; hostname: string }

/**
 * The address each of a preset's apps gets from the domain typed on the
 * Stacks tab: the primary service the domain itself, the others their
 * sibling names, and a background worker none.
 */
export function plannedAddresses(
  services: Pick<SmartStackCatalogueService, "name" | "title" | "address" | "primary">[],
  domain: string,
): PlannedAddress[] {
  const name = domain.trim().toLowerCase().replace(/\.$/, "")
  if (!name) return []
  return services.flatMap((service) => {
    if (service.primary) return [{ service: service.name, title: service.title, hostname: name }]
    if (!service.address) return []
    return [
      {
        service: service.name,
        title: service.title,
        hostname: serviceHostname(name, service.address),
      },
    ]
  })
}

/**
 * What the Stacks tab says under the services: that each app answers at an
 * address of its own and signs in with one password, and which run without
 * a web interface.
 */
export function addressSentence(
  services: Pick<SmartStackCatalogueService, "title" | "access">[],
): string {
  const apps = services.filter((service) => service.access !== "worker")
  const workers = services.filter((service) => service.access === "worker")
  if (apps.length === 0) return ""
  const each =
    apps.length === 1
      ? `${apps[0].title} answers at its own HTTPS address and signs in with the stack's one password.`
      : "Every app answers at its own HTTPS address beside the domain, and they all sign in with one password."
  if (workers.length === 0) return each
  return `${each} ${listWords(workers.map((worker) => worker.title))} ${workers.length === 1 ? "runs" : "run"} in the background with no web interface.`
}

/** "Seerr", "Seerr and Radarr", "Seerr, Radarr and Sonarr". */
export function listWords(words: string[]): string {
  if (words.length <= 1) return words[0] ?? ""
  return `${words.slice(0, -1).join(", ")} and ${words.at(-1)}`
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
 * Whether a step has a record to open from its Setup line: what it did, its
 * error, its report, a retry or when it ran. A step the pass has not reached
 * has none, and a press onto its title and "Waiting" again would be a button
 * onto an empty record, twenty-seven of them while the release comes up.
 */
export function stepHasRecord(step: SmartStackStep): boolean {
  return Boolean(
    stepDetail(step) ||
    step.error ||
    (step.log && step.log.length > 0) ||
    step.attempts > 1 ||
    step.startedAt ||
    step.finishedAt,
  )
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

/**
 * How an app is reached, on its row: opened at its address, run in the
 * background with no interface, or not reachable from outside yet, with the
 * loopback address it is published on as the detail behind the words.
 */
export type AppReach =
  { kind: "open"; url: string } | { kind: "background" } | { kind: "none"; detail?: string }

export function appReach(service: Pick<SmartStackService, "access" | "url" | "address">): AppReach {
  if (service.access === "worker") return { kind: "background" }
  if (service.url) return { kind: "open", url: service.url }
  return {
    kind: "none",
    detail: service.address ? `Published on this server at ${service.address}` : undefined,
  }
}

/**
 * One thing left for the person, in the order the page offers them: what
 * holds the stack from ready, then the apps with no address, then what is
 * optional. The first is the page's one next step.
 */
export type NextStep =
  | { kind: "action"; action: SmartStackAction }
  | { kind: "addresses"; addresses: SmartStackAddresses }
  | { kind: "upgrade"; upgrade: SmartStackUpgrade }

export function nextSteps(
  state: Pick<SmartStackState, "actions" | "addresses" | "status" | "upgrade">,
): NextStep[] {
  const actions = orderedActions(state.actions)
  const blocking = actions.filter((action) => action.blocking)
  const optional = actions.filter((action) => !action.blocking)
  const missing =
    state.status !== "waiting" &&
    state.addresses &&
    (state.addresses.missing.length > 0 || (state.addresses.unpublished?.length ?? 0) > 0)
      ? [{ kind: "addresses" as const, addresses: state.addresses }]
      : []
  // The apps the template gained come before the addresses: they are part of
  // the stack the rest is set up for.
  const upgrade =
    state.status !== "waiting" && state.upgrade && state.upgrade.added.length > 0
      ? [{ kind: "upgrade" as const, upgrade: state.upgrade }]
      : []
  return [
    ...blocking.map((action) => ({ kind: "action" as const, action })),
    ...upgrade,
    ...missing,
    ...optional.map((action) => ({ kind: "action" as const, action })),
  ]
}

/** Whether an action is the one that leads to the source picker. */
export function isSourceAction(action: Pick<SmartStackAction, "id">): boolean {
  return action.id === "media-source"
}

/**
 * The sentence over "Give every app its address": which apps have none, and
 * where theirs go. Without a primary domain the names cannot be made yet, so
 * it says the domain comes first.
 */
/**
 * Whether every app has its address saved and the step is only to publish
 * the ones the live release does not serve: the release that was to failed.
 */
export function onlyUnpublished(addresses: SmartStackAddresses): boolean {
  return addresses.missing.length === 0 && (addresses.unpublished?.length ?? 0) > 0
}

export function addressesSentence(addresses: SmartStackAddresses): string {
  if (onlyUnpublished(addresses)) {
    const unpublished = addresses.unpublished ?? []
    const titles = listWords(unpublished.map((entry) => entry.title))
    const one = unpublished.length === 1
    return `${titles} ${one ? "has its address" : "have their addresses"} saved, but the release that was to publish ${one ? "it" : "them"} did not finish. Deploying again publishes ${one ? "it" : "them"}.`
  }
  const titles = listWords(addresses.missing.map((missing) => missing.title))
  const count = addresses.missing.length
  const have = count === 1 ? "has" : "have"
  if (!addresses.primary)
    return `${titles} ${have} no address yet, and neither has the stack: give it a domain and every app gets an address beside it.`
  return `${titles} ${have} no address yet. ${count === 1 ? "It gets one" : "Each gets one"} beside ${addresses.primary}, and a deployment starts to serve ${count === 1 ? "it" : "them"}.`
}

/**
 * The names the apps would get from a domain typed in the dialog, where the
 * stack has no primary domain to make them from: the server's own name when
 * it gave one, else the label's sibling of what was typed.
 */
export function missingHostnames(
  missing: SmartStackMissingAddress[],
  labels: Record<string, string | undefined>,
  domain: string,
): SmartStackMissingAddress[] {
  return missing.map((entry) => {
    if (entry.hostname) return entry
    const label = labels[entry.service]
    return { ...entry, hostname: label ? serviceHostname(domain, label) : "" }
  })
}

/**
 * The sentence over "Add the new apps": what the stack gained, and that
 * everything the deployment has stays. Saved but not live, the release that
 * was to bring them did not finish, and deploying again does.
 */
export function upgradeSentence(upgrade: SmartStackUpgrade): string {
  const titles = listWords(upgrade.added.map((service) => service.title))
  const one = upgrade.added.length === 1
  if (upgrade.saved)
    return `${titles} ${one ? "is" : "are"} part of the stack now, but the release that was to bring ${one ? "it" : "them"} did not finish. Deploying again starts ${one ? "it" : "them"}.`
  return `The stack now comes with ${titles}. Adding ${one ? "it" : "them"} deploys the stack again with ${one ? "it" : "them"} beside the other apps; your library, settings, domains and login stay, and the dashboard sets ${one ? "it" : "them"} up and connects ${one ? "it" : "them"} once the release is live.`
}

/**
 * Said beside a press that deploys (the new apps, the apps' addresses) while
 * the deployment has changes saved but not deployed: the release it starts
 * is of the saved configuration, so they go out with it, whatever they are.
 */
export const PENDING_CHANGES_NOTE =
  "Every change saved to the deployment but not deployed yet goes out with it."

/** A refusal of "Add the new apps", in words; anything else is the caller's error. */
export function upgradeRefusal(code: string | undefined): string | undefined {
  if (code === "stack_upgrade_none") return "The stack already runs every app."
  if (code === "stack_not_preset")
    return "The deployment's source is no longer the stack's template, so there is nothing of the stack's to add."
  return undefined
}

/** A refusal of "Give every app its address", in words; anything else is the caller's error. */
export function addressesRefusal(code: string | undefined): string | undefined {
  if (code === "stack_addresses_present") return "Every app already has its address."
  if (code === "stack_no_domain")
    return "The stack has no domain yet. Give the one the apps' addresses are made from."
  return undefined
}

/**
 * What still keeps a new password from being the stack's, by the dashboard's
 * own rule (`passwordChecks`): twelve characters, three kinds of them.
 */
export function loginPasswordProblem(password: string): string | undefined {
  const checks = passwordChecks(password)
  if (!password) return "Type the new password."
  if (!checks.long) return "Use at least 12 characters."
  if (!checks.mixed) return "Mix three of upper case, lower case, digits and symbols."
  return undefined
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
