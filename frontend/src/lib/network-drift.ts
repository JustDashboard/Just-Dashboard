import type { NetworkChangeStatus } from "./types"

export type DriftStatus =
  "matching" | "missing" | "drift" | "conflict" | "unreadable" | "unknown" | "not_required"

export type DriftObservation = {
  id: string
  domain: string
  resource: string
  status: DriftStatus
  coverage: string
  reason?: string
  expected?: Record<string, string>
  observed?: Record<string, string>
  owned: boolean
  repairable: boolean
  dependencies?: string[]
}

export type OwnedRepair = {
  id: string
  observationId: string
  domain: string
  resource: string
  action: string
  reason: string
  preconditions: string[]
  dependencies?: string[]
  executable?: boolean
  blocker?: string
  reviewToken?: string
  before?: string
  after?: string
  effect?: "boot_files" | "runtime"
}

export type DriftReport = {
  checkedAt: string
  finishedAt: string
  status: "matching" | "drift" | "unknown"
  consistent: boolean
  savedGeneration?: string
  canonicalGeneration?: string
  spec: DriftObservation
  journal: DriftObservation
  change?: NetworkChangeStatus
  files: DriftObservation[]
  runtime: DriftObservation[]
  boot: {
    status: DriftStatus
    reason?: string
    unit: string
    loadState?: string
    unitFileState?: string
    activeState?: string
    fragmentPath?: string
    dropInPaths?: string
    needDaemonReload?: string
    owned: boolean
    repairable: boolean
    execution: {
      status: "unknown" | "unrecorded" | "running" | "failed" | "succeeded"
      source: string
      scope: string
      bootTrigger: string
      bootId?: string
      invocationId?: string
      startedAt?: string
      finishedAt?: string
      startedMonotonicUs?: number
      finishedMonotonicUs?: number
      result?: string
      exitStatus?: number
      reason?: string
      commands: {
        path: string
        ignoreErrors: boolean
        startedAt?: string
        finishedAt?: string
        code?: string
        exitStatus?: number
      }[]
    }
  }
  blocklists: {
    id: number
    name: string
    enabled: boolean
    enforcement: string
    renderedGeneration: string
    cache: { status: string; count: number; generation: string; error?: string }
    runtime: { status: string; count: number | null; generation: string; error?: string }
  }[]
  repairPlan: {
    generation?: string
    createdAt: string
    status: string
    executable: boolean
    blockers: string[]
    items: OwnedRepair[]
    excluded: { observationId: string; reason: string }[]
    preconditions: string[]
  }
}

export function driftReading(status: string) {
  switch (status) {
    case "matching":
      return { label: "Matches", tone: "running" as const }
    case "missing":
      return { label: "Missing", tone: "warning" as const }
    case "drift":
      return { label: "Changed", tone: "warning" as const }
    case "conflict":
      return { label: "Ownership conflict", tone: "danger" as const }
    case "unreadable":
      return { label: "Could not read", tone: "unknown" as const }
    case "not_required":
      return { label: "Not required", tone: "stopped" as const }
    default:
      return { label: "Incomplete comparison", tone: "unknown" as const }
  }
}

export function driftCounts(observations: Pick<DriftObservation, "status">[]) {
  return {
    differences: observations.filter((o) => ["missing", "drift", "conflict"].includes(o.status))
      .length,
    conflicts: observations.filter((o) => o.status === "conflict").length,
    unknown: observations.filter((o) => ["unreadable", "unknown"].includes(o.status)).length,
    matching: observations.filter((o) => o.status === "matching").length,
  }
}

/** Where the saved configuration is written, in the order it is restored at boot. */
export type DriftDomain = "saved" | "files" | "kernel" | "boot" | "blocklists"

export const DRIFT_DOMAINS: { domain: DriftDomain; label: string }[] = [
  { domain: "saved", label: "Saved configuration" },
  { domain: "files", label: "Rendered files" },
  { domain: "kernel", label: "Kernel objects" },
  { domain: "boot", label: "Boot unit" },
  { domain: "blocklists", label: "Blocklists" },
]

/**
 * One comparison as the page draws it: which domain it is in, the product
 * that owns the thing compared, and a name a reader recognises — a route is
 * its destination, not the id the saved configuration gave it.
 */
export type DriftRow = {
  id: string
  domain: DriftDomain
  kind: string
  /** A `product-logo` id; nothing for the dashboard's own files, which draw its mark. */
  product?: string
  label: string
  detail?: string
  status: DriftStatus
  reason?: string
  owned?: boolean
  expected?: Record<string, string>
  observed?: Record<string, string>
}

const KIND_LABEL: Record<string, string> = {
  spec: "configuration",
  journal: "journal",
  render: "file",
  "recovery-helper": "helper",
  namespace: "namespace",
  link: "device",
  address: "address",
  route: "route",
  rule: "policy rule",
  sysctl: "kernel setting",
  shaping: "shaping",
  gateway: "nft table",
  admission: "admission",
  "boot-unit": "unit",
  activation: "activation",
  blocklist: "blocklist",
}

export function driftKindLabel(kind: string) {
  return KIND_LABEL[kind] ?? kind.replaceAll("-", " ")
}

function split(path: string) {
  const cut = path.lastIndexOf("/")
  return cut > 0 ? { name: path.slice(cut + 1), dir: path.slice(0, cut) } : { name: path }
}

/**
 * The program a rendered file is read by at boot: `nft -f` for the nftables
 * files, systemd for the units, `ip`, `tc` and `sysctl` — the kernel's own
 * tools — for the batches and the settings drop-in.
 */
export function driftFileProduct(path: string) {
  if (path.endsWith(".nft")) return "netfilter"
  if (path.endsWith(".service")) return "systemd"
  if (path.endsWith(".batch") || path.includes("/sysctl.d/")) return "linux"
  return undefined
}

/** Which blocklist publisher a list is, by the name the preset gave it. */
export function blocklistProduct(name: string) {
  if (/spamhaus/i.test(name)) return "spamhaus"
  if (/firehol/i.test(name)) return "firehol"
  return undefined
}

function observationRow(o: DriftObservation, domain: DriftDomain): DriftRow {
  const base = {
    id: o.id,
    domain,
    kind: o.domain,
    status: o.status,
    reason: o.reason,
    owned: o.owned,
    expected: o.expected,
    observed: o.observed,
  }
  const { name, dir } = split(o.resource)
  switch (o.domain) {
    case "spec":
    case "journal":
    case "recovery-helper":
      return { ...base, label: name, detail: dir }
    case "render":
      return { ...base, product: driftFileProduct(o.resource), label: name, detail: dir }
    case "link": {
      // A peer in another namespace is named `namespace/device`.
      const [space, device] = o.resource.includes("/")
        ? o.resource.split("/", 2)
        : [undefined, o.resource]
      const kind = o.expected?.kind ?? o.observed?.kind
      return {
        ...base,
        product: kind === "wireguard" ? "wireguard" : "linux",
        label: device,
        detail: [kind, space && `in ${space}`].filter(Boolean).join(" · ") || undefined,
      }
    }
    case "address": {
      // `device/cidr`, or `namespace/device/cidr` inside a namespace.
      const parts = o.resource.split("/")
      const cidr = parts.slice(-2).join("/")
      const [space, device] = parts.length > 3 ? parts : [undefined, parts[0]]
      return {
        ...base,
        product: "linux",
        label: cidr,
        detail: [`on ${device}`, space && `in ${space}`].filter(Boolean).join(" · "),
      }
    }
    case "route": {
      const destination = o.expected?.destination
      const table = o.expected?.table
      return {
        ...base,
        product: "linux",
        label: destination ?? `Route ${o.resource}`,
        detail: [table && `table ${table}`, `route ${o.resource}`].filter(Boolean).join(" · "),
      }
    }
    case "rule": {
      const priority = o.expected?.priority
      return {
        ...base,
        product: "linux",
        label: priority ? `priority ${priority}` : `Rule ${o.resource}`,
        detail: [o.expected?.family, `rule ${o.resource}`].filter(Boolean).join(" · "),
      }
    }
    case "sysctl":
      return { ...base, product: "linux", label: o.resource }
    case "namespace":
    case "shaping":
      return { ...base, product: "linux", label: o.resource }
    case "gateway":
      return { ...base, product: "netfilter", label: o.resource }
    case "admission": {
      const [family, chain] = o.resource.split("/", 2)
      return {
        ...base,
        product: "netfilter",
        label: chain ?? o.resource,
        detail: [family, o.observed?.tool].filter(Boolean).join(" · "),
      }
    }
    default:
      return { ...base, label: o.resource }
  }
}

/**
 * Every comparison the report makes, as one list the counts, the picture and
 * the table all read, so they cannot disagree about how many there are. The
 * boot unit's activation and each enabled blocklist are comparisons too: a
 * failed activation is a known difference, and a list whose kernel set has
 * fallen behind its cache is one.
 */
export function driftRows(report: DriftReport): DriftRow[] {
  const execution = report.boot.execution
  return [
    observationRow(report.spec, "saved"),
    observationRow(report.journal, "saved"),
    ...report.files.map((o) => observationRow(o, "files")),
    ...report.runtime.map((o) => observationRow(o, "kernel")),
    {
      id: "boot:unit",
      domain: "boot",
      kind: "boot-unit",
      product: "systemd",
      label: report.boot.unit,
      detail: [report.boot.loadState, report.boot.unitFileState].filter(Boolean).join(" · "),
      status: report.boot.status,
      reason: report.boot.reason,
      owned: report.boot.owned,
    },
    {
      id: "boot:activation",
      domain: "boot",
      kind: "activation",
      product: "systemd",
      label: "Last activation",
      detail: execution?.status,
      status:
        execution?.status === "failed"
          ? "drift"
          : execution?.status === "succeeded"
            ? "matching"
            : "unknown",
      reason: execution?.reason,
    },
    ...report.blocklists.map((list): DriftRow => ({
      id: `blocklist:${list.id}`,
      domain: "blocklists",
      kind: "blocklist",
      product: blocklistProduct(list.name),
      label: list.name,
      detail: list.enforcement,
      status: !list.enabled
        ? "not_required"
        : list.enforcement === "verified"
          ? "matching"
          : list.enforcement === "degraded"
            ? "drift"
            : "unknown",
      reason:
        list.enabled && list.enforcement === "degraded"
          ? "The kernel set does not hold what the cache and the render say it should."
          : undefined,
    })),
  ]
}

// Worst first: what is someone else's, then what changed, then what is gone,
// then what could not be compared, then what matches.
const SEVERITY: Record<string, number> = {
  conflict: 0,
  drift: 1,
  missing: 2,
  unreadable: 3,
  unknown: 4,
  matching: 5,
  not_required: 6,
}

export function driftSeverity(status: string) {
  return SEVERITY[status] ?? 4
}

/** The rows worst first, each domain in restore order within a status. */
export function driftSorted(rows: DriftRow[]) {
  const order = DRIFT_DOMAINS.map((d) => d.domain)
  return rows
    .map((row, index) => ({ row, index }))
    .sort(
      (a, b) =>
        driftSeverity(a.row.status) - driftSeverity(b.row.status) ||
        order.indexOf(a.row.domain) - order.indexOf(b.row.domain) ||
        a.index - b.index,
    )
    .map(({ row }) => row)
}

/** The worst status in a set of rows: what a domain's wire says. */
export function driftWorst(rows: Pick<DriftRow, "status">[]): DriftStatus | undefined {
  return rows.reduce<DriftStatus | undefined>(
    (worst, row) =>
      worst === undefined || driftSeverity(row.status) < driftSeverity(worst) ? row.status : worst,
    undefined,
  )
}

/**
 * The one sentence at the end of the identity line. A failed refresh is
 * said before anything the stale evidence says, and a configuration that
 * changed under the inspection before any difference it found, because
 * neither reading can be trusted for what follows it.
 */
export function driftVerdict(
  report: Pick<DriftReport, "consistent">,
  rows: Pick<DriftRow, "status">[],
  readFailed: boolean,
): { tone: "running" | "warning" | "danger" | "unknown"; label: string; narrows?: "differ" } {
  if (readFailed) return { tone: "warning", label: "Last known evidence" }
  if (!report.consistent) return { tone: "warning", label: "Changed during inspection" }
  const counts = driftCounts(rows)
  if (counts.differences)
    return {
      tone: counts.conflicts ? "danger" : "warning",
      label: `${counts.differences} ${counts.differences === 1 ? "difference" : "differences"}`,
      narrows: "differ",
    }
  if (counts.unknown) return { tone: "unknown", label: `${counts.unknown} incomplete` }
  return { tone: "running", label: "Everything matches" }
}

export type DriftMove = {
  id: string
  label: string
  product?: string
  domain: DriftDomain
  from?: DriftStatus
  to?: DriftStatus
  at: string
}

/**
 * What changed between two inspections: each comparison whose status is not
 * what it was, one that appeared, and one that is no longer made. A poll that
 * found the same facts moves nothing.
 */
export function driftMoves(previous: DriftRow[], next: DriftRow[], at: string): DriftMove[] {
  const before = new Map(previous.map((row) => [row.id, row]))
  const after = new Map(next.map((row) => [row.id, row]))
  const moves: DriftMove[] = []
  for (const row of next) {
    const was = before.get(row.id)
    if (was?.status === row.status) continue
    moves.push({ ...pick(row), from: was?.status, to: row.status, at })
  }
  for (const row of previous) {
    if (!after.has(row.id)) moves.push({ ...pick(row), from: row.status, at })
  }
  return moves
}

function pick({ id, label, product, domain }: DriftRow) {
  return { id, label, product, domain }
}

/**
 * A systemd timestamp (`Sat 2026-10-10 14:58:02 UTC`) as an instant, where
 * the zone it states can be read without guessing: UTC, GMT or a numeric
 * offset. A zone abbreviation such as `EEST` names different offsets in
 * different places, so it is left unread rather than read wrong.
 */
export function systemdInstant(raw: string | undefined): number | undefined {
  const match = raw?.match(
    /^(?:\w{3} )?(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})(?:\.\d+)? (UTC|GMT|[+-]\d{2}:?\d{2})$/,
  )
  if (!match) return undefined
  const [, y, mo, d, h, mi, s, zone] = match
  const offset = /^[+-]/.test(zone) ? `${zone.slice(0, 3)}:${zone.slice(-2)}` : "Z"
  const at = Date.parse(`${y}-${mo}-${d}T${h}:${mi}:${s}${offset}`)
  return Number.isNaN(at) ? undefined : at
}

export function selectedDriftRepairRequest(report: DriftReport, selected: string[]) {
  if (
    !report.consistent ||
    report.repairPlan.status === "blocked" ||
    !report.repairPlan.executable ||
    !report.savedGeneration ||
    report.savedGeneration !== report.repairPlan.generation ||
    !selected.length ||
    new Set(selected).size !== selected.length
  )
    return undefined
  const items = report.repairPlan.items.filter((item) => selected.includes(item.id))
  if (
    items.length !== selected.length ||
    items.some((item) => !item.executable || !item.reviewToken)
  )
    return undefined
  return {
    generation: report.savedGeneration,
    selections: items.map(({ id, reviewToken }) => ({ id, reviewToken })),
  }
}

export function driftRepairOutcome(change: NetworkChangeStatus) {
  const pending = change.phase === "awaiting_confirmation" && change.watchdog === "armed"
  const completed = ["saved", "confirmed"].includes(change.phase) && change.watchdog === "completed"
  const runtimeApplied = change.runtime === "applied"
  const filesSaved = change.persistence === "written" && change.boot === "not_verified"
  const runtimeOnly =
    runtimeApplied && change.persistence === "not_applicable" && change.boot === "not_applicable"
  if (
    (!pending && !completed) ||
    (change.recoveryErrors?.length ?? 0) > 0 ||
    (!filesSaved && !runtimeOnly) ||
    !["applied", "not_applied"].includes(change.runtime)
  )
    return {
      tone: "warning" as const,
      title: "Inspect the repair outcome",
      description:
        "The returned change does not establish a completed repair. Inspect its current status before retrying.",
    }
  return {
    tone: "success" as const,
    title: pending ? "Repairs await reconnection confirmation" : "Selected repairs applied",
    description: runtimeOnly
      ? "Runtime applied; not saved for boot."
      : runtimeApplied
        ? "Selected runtime rules applied and boot inputs saved; boot execution remains unverified."
        : "Selected boot inputs saved; boot execution remains unverified.",
  }
}

// Keep a selection only while its reviewed evidence is unchanged. Poll time is
// excluded: a fresh read of the same facts should not interrupt an operator.
export function driftReviewKey(report: DriftReport) {
  return JSON.stringify({
    generation: report.savedGeneration,
    consistent: report.consistent,
    phase: report.change?.phase,
    journalId: report.change?.id,
    journalGeneration: report.change?.generation,
    boot: {
      status: report.boot.status,
      owned: report.boot.owned,
      activeState: report.boot.activeState,
      bootId: report.boot.execution?.bootId,
      fragmentPath: report.boot.fragmentPath,
      dropInPaths: report.boot.dropInPaths,
      needDaemonReload: report.boot.needDaemonReload,
      unitFileState: report.boot.unitFileState,
    },
    plan: {
      generation: report.repairPlan.generation,
      status: report.repairPlan.status,
      executable: report.repairPlan.executable,
      blockers: report.repairPlan.blockers,
      items: report.repairPlan.items,
      excluded: report.repairPlan.excluded,
      preconditions: report.repairPlan.preconditions,
    },
    observations: [report.spec, report.journal, ...report.files, ...report.runtime].map(
      ({ id, status, expected, observed }) => ({
        id,
        status,
        expected,
        observed,
      }),
    ),
  })
}
