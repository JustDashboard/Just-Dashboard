import type { SystemdUnit } from "@/lib/types"
import { unitBucket, type UnitBucket } from "@/components/procs/units"

/**
 * The facts the service page reads off `systemctl show`, in words: where a
 * unit's page is, what its startup and restart settings mean, which of its
 * dependencies say what, and the environment its unit file gives it.
 */

/** A unit's own page, opened on one of its views. */
export function serviceHref(name: string, tab?: string): string {
  // `@` and `:` are legal in a path segment, and `getty@tty1.service` reads
  // better in the address bar than its escape.
  const segment = encodeURIComponent(name).replace(/%40/g, "@").replace(/%3A/gi, ":")
  const base = `/processes/services/${segment}`
  return tab && tab !== "overview" ? `${base}?tab=${encodeURIComponent(tab)}` : base
}

/** The Services table's filters, which the page's rail reads as its own. */
export const SERVICE_FILTER_KEYS = {
  query: "processes.services.query",
  state: "processes.services.state",
  startup: "processes.services.startup",
} as const

export type StartupFilter = "" | "enabled" | "disabled" | "static" | "masked"

export const STARTUP: { value: Exclude<StartupFilter, "">; label: string; title: string }[] = [
  { value: "enabled", label: "Enabled", title: "Starts on boot" },
  { value: "disabled", label: "Disabled", title: "Runs only when started by hand" },
  { value: "static", label: "Static", title: "Started by another unit, not on its own" },
  { value: "masked", label: "Masked", title: "Blocked from starting at all" },
]

export const BUCKETS = new Set<string>(["active", "failed", "changing", "inactive"])
export const STARTUPS = new Set<string>(STARTUP.map((s) => s.value))

/** An enabled-runtime unit starts on this boot's next start as an enabled one does. */
export function startupOf(unit: Pick<SystemdUnit, "unitFileState">): string {
  const state = unit.unitFileState || "unknown"
  return state === "enabled-runtime" ? "enabled" : state
}

/**
 * The units the table shows for its filters: installed ones only (a unit
 * another names but no file defines is counted, not listed), narrowed by
 * state, startup and a search of the name and description.
 */
export function filterUnits(
  units: SystemdUnit[],
  { query, state, startup }: { query: string; state: UnitBucket | ""; startup: StartupFilter },
): SystemdUnit[] {
  const needle = query.trim().toLowerCase()
  return units.filter((u) => {
    if (u.loadState === "not-found") return false
    if (state && unitBucket(u) !== state) return false
    if (startup && startupOf(u) !== startup) return false
    if (!needle) return true
    return u.name.toLowerCase().includes(needle) || u.description.toLowerCase().includes(needle)
  })
}

/** A limit property as a number, or nothing when it is unset or infinite. */
export function limit(value: string | undefined): number | undefined {
  if (!value || value === "infinity") return undefined
  const n = Number(value)
  return Number.isFinite(n) && n > 0 && n < 2 ** 63 ? n : undefined
}

/** A cgroup counter, or nothing when accounting for it is off. */
export function counter(value: string | undefined): number | undefined {
  if (!value || value === "[not set]") return undefined
  const n = Number(value)
  return Number.isFinite(n) && n >= 0 && n < 2 ** 63 ? n : undefined
}

/** A space-separated list property, as systemd prints Wants= or DropInPaths=. */
export function words(value: string | undefined): string[] {
  return (value ?? "").split(/\s+/).filter(Boolean)
}

/** `CPUQuotaPerSecUSec` ("1.500000s", "500ms", "infinity") as a share of one core. */
export function cpuQuotaPercent(value: string | undefined): number | undefined {
  if (!value || value === "infinity") return undefined
  const match = value.match(/^([\d.]+)(s|ms|us)$/)
  if (!match) return undefined
  const n = Number(match[1])
  const seconds = match[2] === "s" ? n : match[2] === "ms" ? n / 1000 : n / 1e6
  return seconds > 0 ? Math.round(seconds * 100) : undefined
}

/** When systemd counts each kind of service as started. */
export const TYPE_WORDS: Record<string, string> = {
  simple: "Started as soon as its process is",
  exec: "Started once its program has been executed",
  forking: "Started when its process forks and the parent exits",
  notify: "Started when it tells systemd it is ready",
  "notify-reload": "Started when it says it is ready; reloads by signal",
  oneshot: "Runs to completion, and is started once it has",
  dbus: "Started when it takes its name on D-Bus",
  idle: "Started once every other job is done",
}

/** What the startup state means for the next boot, in words. */
export function startupSummary(state: string | undefined): {
  label: string
  fact: string
  hint: string
} {
  switch (state) {
    case "enabled":
    case "enabled-runtime":
      return { label: state, fact: "starts on boot", hint: "Starts automatically on boot" }
    case "disabled":
      return { label: state, fact: "started by hand", hint: "Only runs when started by hand" }
    case "static":
      return {
        label: state,
        fact: "started by another unit",
        hint: "Started by another unit, not on its own",
      }
    case "masked":
      return { label: state, fact: "masked", hint: "Blocked from starting at all" }
    case "generated":
      return {
        label: state,
        fact: "generated at boot",
        hint: "Written by a generator from another file, such as /etc/fstab",
      }
    case "transient":
      return {
        label: state,
        fact: "made at run time",
        hint: "Created by a program for this boot; it has no unit file",
      }
    default:
      return {
        label: state || "unknown",
        fact: "startup unknown",
        hint: "Startup state was not reported",
      }
  }
}

/** What the restart policy does the next time the process exits. */
export function restartSummary(policy: string | undefined): {
  policy: string
  label: string
  hint: string
} {
  const value = (policy || "no").toLowerCase()
  switch (value) {
    case "always":
      return { policy: value, label: "always", hint: "Restarts after every exit" }
    case "on-success":
      return { policy: value, label: "on success", hint: "Restarts after clean exits only" }
    case "on-failure":
      return { policy: value, label: "on failure", hint: "Restarts after crashes, not clean exits" }
    case "on-abnormal":
    case "on-abort":
    case "on-watchdog":
      return { policy: value, label: value, hint: "Restarts after abnormal exits only" }
    default:
      return { policy: "no", label: "never", hint: "Stays stopped until started again" }
  }
}

/**
 * The `Environment` property as its assignments. systemd prints them
 * space-separated, an assignment holding a space double-quoted with its
 * quotes and backslashes escaped.
 */
export function splitEnvironment(value: string | undefined): string[] {
  const out: string[] = []
  if (!value) return out
  let current = ""
  let quoted = false
  let started = false
  for (let i = 0; i < value.length; i++) {
    const ch = value[i]
    if (quoted) {
      if (ch === "\\" && i + 1 < value.length) {
        const next = value[++i]
        current += next === "n" ? "\n" : next === "t" ? "\t" : next
      } else if (ch === '"') {
        quoted = false
      } else {
        current += ch
      }
      continue
    }
    if (ch === '"') {
      quoted = true
      started = true
    } else if (ch === " ") {
      if (started) out.push(current)
      current = ""
      started = false
    } else {
      current += ch
      started = true
    }
  }
  if (started) out.push(current)
  return out
}

/** An `EnvironmentFiles` entry: its path, and whether a missing file is fine (`-`). */
export function environmentFiles(value: string | undefined): { path: string; optional: boolean }[] {
  // systemd prints "/etc/default/foo (ignore_errors=yes)" per file.
  return (value ?? "")
    .split(/\n/)
    .flatMap((line) => line.split(/(?<=\))\s+/))
    .map((entry) => entry.trim())
    .filter(Boolean)
    .map((entry) => {
      const match = entry.match(/^(\S+)(?:\s+\(ignore_errors=(yes|no)\))?$/)
      return match
        ? { path: match[1], optional: match[2] === "yes" }
        : { path: entry, optional: false }
    })
}

export type UnitKind =
  | "service"
  | "target"
  | "socket"
  | "timer"
  | "path"
  | "mount"
  | "automount"
  | "swap"
  | "slice"
  | "scope"
  | "device"
  | "other"

export function unitKind(name: string): UnitKind {
  const suffix = name.slice(name.lastIndexOf(".") + 1)
  switch (suffix) {
    case "service":
    case "target":
    case "socket":
    case "timer":
    case "path":
    case "mount":
    case "automount":
    case "swap":
    case "slice":
    case "scope":
    case "device":
      return suffix
    default:
      return "other"
  }
}

/** Where another unit named on this page opens: a service its page, a timer the schedule. */
export function relatedHref(name: string): string | undefined {
  switch (unitKind(name)) {
    case "service":
      return serviceHref(name)
    case "timer":
      return `/processes/scheduled?timer=${encodeURIComponent(name)}`
    default:
      return undefined
  }
}

export type RelationGroup = "starts" | "needs" | "order" | "after" | "conflicts" | "reloads"

/**
 * Every dependency systemd keeps, under the question it answers. The first
 * column of each pair is this unit's own setting; the reverse is the other
 * unit's, which systemd adds here so the graph reads from both ends.
 */
export const RELATIONS: {
  key: string
  group: RelationGroup
  label: string
  hint: string
}[] = [
  {
    key: "TriggeredBy",
    group: "starts",
    label: "Started by",
    hint: "A timer, socket or path that starts it when it fires",
  },
  {
    key: "WantedBy",
    group: "starts",
    label: "Wanted by",
    hint: "Starting these starts it too; a failure of it does not stop them",
  },
  {
    key: "RequiredBy",
    group: "starts",
    label: "Required by",
    hint: "These do not start without it",
  },
  { key: "BoundBy", group: "starts", label: "Bound by", hint: "These stop when it stops" },
  {
    key: "UpheldBy",
    group: "starts",
    label: "Upheld by",
    hint: "These start it again whenever it is down",
  },
  {
    key: "ConsistsOf",
    group: "starts",
    label: "Consists of",
    hint: "Stopping or restarting it does the same to these",
  },
  {
    key: "RequisiteOf",
    group: "starts",
    label: "Requisite of",
    hint: "These refuse to start unless it is already running",
  },
  {
    key: "Requires",
    group: "needs",
    label: "Requires",
    hint: "Started with it; if one fails to start, it does not start",
  },
  {
    key: "Requisite",
    group: "needs",
    label: "Requisite",
    hint: "Must already be running, or it refuses to start",
  },
  {
    key: "BindsTo",
    group: "needs",
    label: "Binds to",
    hint: "It stops whenever one of these stops",
  },
  {
    key: "Wants",
    group: "needs",
    label: "Wants",
    hint: "Started with it; a failure of one does not stop it",
  },
  {
    key: "Upholds",
    group: "needs",
    label: "Upholds",
    hint: "It starts these again whenever they are down",
  },
  {
    key: "PartOf",
    group: "needs",
    label: "Part of",
    hint: "Stopping or restarting one of these does the same to it",
  },
  {
    key: "JoinsNamespaceOf",
    group: "needs",
    label: "Joins the namespaces of",
    hint: "Shares their /tmp and network namespace",
  },
  {
    key: "After",
    group: "order",
    label: "Starts after",
    hint: "Ordering only: waits for these when both start",
  },
  {
    key: "Before",
    group: "order",
    label: "Starts before",
    hint: "Ordering only: these wait for it when both start",
  },
  { key: "OnFailure", group: "after", label: "On failure", hint: "Started when it fails" },
  {
    key: "OnSuccess",
    group: "after",
    label: "On success",
    hint: "Started when it finishes cleanly",
  },
  { key: "Triggers", group: "after", label: "Triggers", hint: "Units it starts when it fires" },
  {
    key: "Conflicts",
    group: "conflicts",
    label: "Conflicts with",
    hint: "Starting it stops these, and starting one stops it",
  },
  {
    key: "ConflictedBy",
    group: "conflicts",
    label: "Conflicted by",
    hint: "Starting one of these stops it",
  },
  {
    key: "PropagatesReloadTo",
    group: "reloads",
    label: "Reloads with it",
    hint: "Reloading it reloads these",
  },
  {
    key: "ReloadPropagatedFrom",
    group: "reloads",
    label: "Reloads when",
    hint: "Reloading one of these reloads it",
  },
]

export const RELATION_GROUPS: { key: RelationGroup; title: string }[] = [
  { key: "starts", title: "What starts it" },
  { key: "needs", title: "What it needs" },
  { key: "after", title: "What it starts" },
  { key: "order", title: "Start order" },
  { key: "conflicts", title: "What it cannot run with" },
  { key: "reloads", title: "Reloads" },
]

/** Every unit this one names in a dependency property, by property. */
export function relations(properties: Record<string, string>): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const { key } of RELATIONS) {
    const names = words(properties[key])
    if (names.length > 0) out[key] = names
  }
  return out
}

/** Exposure from `systemd-analyze security`, 0 (locked down) to 10 (nothing applied). */
export function exposureTone(score: number): "running" | "notice" | "warning" | "danger" {
  if (score < 4) return "running"
  if (score < 6.5) return "notice"
  if (score < 8.5) return "warning"
  return "danger"
}

/** systemd-analyze's verdicts, as they are read. */
export const VERDICT_WORDS: Record<string, string> = {
  SAFE: "safe",
  OK: "ok",
  MEDIUM: "medium",
  EXPOSED: "exposed",
  UNSAFE: "unsafe",
}

/** A memory figure the limits form accepts: "512M", "2G", "1.5G", or bytes. */
export function parseBytes(text: string): number | undefined {
  const match = text
    .trim()
    .toUpperCase()
    .match(/^(\d+(?:\.\d+)?)\s*([KMGT]?)I?B?$/)
  if (!match) return undefined
  const scale = { "": 1, K: 1024, M: 1024 ** 2, G: 1024 ** 3, T: 1024 ** 4 }[match[2]] ?? 1
  const bytes = Math.round(Number(match[1]) * scale)
  return bytes > 0 ? bytes : undefined
}

export type ServiceWarning = {
  key: string
  tone: "warning" | "danger" | "default"
  title: string
  body: string
}

/**
 * What is wrong with a unit beyond its state, from its properties: a unit
 * file changed on disk that systemd has not re-read, a frozen cgroup, a
 * condition that kept it from starting, memory at its ceiling.
 */
export function serviceWarnings(
  unit: SystemdUnit,
  properties: Record<string, string>,
): ServiceWarning[] {
  const out: ServiceWarning[] = []
  if (properties.NeedDaemonReload === "yes") {
    out.push({
      key: "reload",
      tone: "warning",
      title: "Its unit file changed on disk",
      body: "systemd is still running it from the version it read before. Reload the unit files for the change to count, then restart it to apply it.",
    })
  }
  if (properties.FreezerState === "frozen" || properties.FreezerState === "freezing") {
    out.push({
      key: "frozen",
      tone: "warning",
      title: "Frozen",
      body: "Every process in it is paused by its cgroup and takes no processor time until it is thawed; whatever it serves waits.",
    })
  }
  if (properties.ConditionResult === "no" && unitBucket(unit) !== "active") {
    out.push({
      key: "condition",
      tone: "default",
      title: "Skipped: a condition was not met",
      body: "Its last start was skipped rather than failed, because one of its Condition…= settings did not hold on this host.",
    })
  }
  const memoryMax = limit(properties.MemoryMax)
  const memory = unit.memoryBytes ?? 0
  if (memoryMax && memory / memoryMax >= 0.9 && unitBucket(unit) === "active") {
    out.push({
      key: "memory",
      tone: "danger",
      title: "At its memory limit",
      body: "It is using nine-tenths or more of its MemoryMax. Past it the kernel ends one of its processes; raise the limit from Control, or find what it holds.",
    })
  }
  return out
}
