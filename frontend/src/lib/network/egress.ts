import type { Tone } from "@/components/tone"

/** A policy selecting the traffic an egress group carries. */
export type EgressPolicy = {
  kind: "all" | "selector" | "rule"
  from?: string
  to?: string
  iif?: string
  fwmark?: string
}

export type EgressMember = {
  id: number
  name: string
  kind: "gateway" | "tunnel"
  gateway?: string
  device: string
  source?: string
  priority: number
  weight: number
}

export type EgressProbe = {
  kind: "icmp" | "tcp" | "dns"
  target: string
  port?: number
  name?: string
}

export type EgressThresholds = {
  intervalSeconds: number
  timeoutMillis: number
  lossPercent: number
  latencyMillis: number
  window: number
  failAfter: number
  recoverAfter: number
  holdSeconds: number
  stableSeconds: number
}

export type EgressProbeResult = {
  kind: string
  target: string
  ok: boolean
  rttMillis?: number
  error?: string
}

export type EgressSample = {
  at: string
  probes: EgressProbeResult[]
  ok: number
  total: number
  loss: number
  latencyMillis?: number
  good: boolean
  why?: string
}

export type EgressPoint = { at: string; latencyMillis?: number; loss: number; good: boolean }

export type EgressMemberView = EgressMember & {
  table: number
  mark: string
  probeSource?: string
  state: "unknown" | "up" | "down"
  since?: string
  proven: boolean
  active: boolean
  good: number
  bad: number
  last?: EgressSample
  history: EgressPoint[]
}

export type EgressDecision = {
  action: "none" | "failover" | "failback" | "rebalance" | "hold"
  target?: number[]
  reason: string
}

export type EgressConnections = {
  read: number
  truncated?: boolean
  error?: string
  byMember: Record<string, number>
  moved: number
  pinned: number
  flushed: number
  flushFailed?: number
  spared?: number
  basis: string
}

export type EgressMemberEvidence = {
  id: number
  state: string
  since?: string
  latencyMillis?: number
  loss: number
  ok: number
  total: number
  why?: string
  at?: string
}

export type EgressEvent = {
  id: number
  groupId: number
  at: string
  kind:
    | "state"
    | "switch"
    | "hold"
    | "refused"
    | "failed"
    | "verify"
    | "connections"
    | "simulation"
    | "automation"
    | "config"
  action?: string
  memberId?: number
  outcome?: string
  reason: string
  actor?: string
  before?: number[]
  after?: number[]
  evidence?: {
    members?: EgressMemberEvidence[]
    routeBefore?: string
    routeAfter?: string
    connections?: EgressConnections
    change?: string
    fingerprint?: string
  }
}

export type EgressExpectation = { name: string; passed: boolean; detail: string }

export type EgressSimMember = {
  id: number
  state: string
  good: boolean
  latencyMillis?: number
  loss: number
  ok: number
  total: number
  fault?: string
}

export type EgressSimStep = {
  index: number
  phase: string
  seconds: number
  members: EgressSimMember[]
  active: number[]
  decision: EgressDecision
  switched?: boolean
  dataPath: string
}

export type EgressSimPhase = {
  name: string
  description: string
  start: number
  end: number
  faults: Record<string, string>
}

export type EgressSimulation = {
  id: string
  groupId: number
  fingerprint: string
  status: "running" | "passed" | "failed" | "error" | "interrupted"
  actor?: string
  startedAt: string
  finishedAt?: string
  error?: string
  result?: {
    phases: EgressSimPhase[]
    steps: EgressSimStep[]
    expectations: EgressExpectation[]
    topology: string[]
    limits: string[]
    cleanup: string
    intervalSeconds: number
  }
}

export type EgressFinding = { level: "ok" | "warning" | "error"; member?: number; text: string }

export type EgressRuntime = {
  status: "verified" | "drift" | "unreadable"
  missing?: string[]
  route: string
  checkedAt: string
  error?: string
}

export type EgressGroup = {
  id: number
  slot: number
  name: string
  family: "inet" | "inet6"
  policy: EgressPolicy
  probes: EgressProbe[]
  thresholds: EgressThresholds
  sticky: boolean
  connections: "flush" | "keep"
  failback: "automatic" | "manual"
  protected?: string[]
  enabled: boolean
  automation: boolean
  active: number[]
  decidedAt?: string
  decidedBy?: string
  simulation?: { id: string; fingerprint: string; passedAt: string }
  createdAt?: string
  createdBy?: string
  fingerprint: string
  table: number
  members: EgressMemberView[]
  advice?: EgressDecision
  runtime?: EgressRuntime
  readiness: EgressFinding[]
  events: EgressEvent[]
  lastSimulation?: EgressSimulation
  automationBlock?: string
}

export type EgressView = {
  groups: EgressGroup[]
  capacity: number
  persistent: boolean
  monitoring?: string
  running?: {
    id: string
    groupId: number
    step: number
    steps: number
    phase: string
    startedAt: string
  }
}

export const DEFAULT_THRESHOLDS: EgressThresholds = {
  intervalSeconds: 10,
  timeoutMillis: 1000,
  lossPercent: 20,
  latencyMillis: 300,
  window: 5,
  failAfter: 3,
  recoverAfter: 5,
  holdSeconds: 60,
  stableSeconds: 120,
}

/** What the policy carries, in a sentence. */
export function egressPolicySummary(policy: EgressPolicy): string {
  switch (policy.kind) {
    case "all":
      return "All of this server's traffic that the main table would send to its default route"
    case "selector": {
      const parts = []
      if (policy.from) parts.push(`from ${policy.from}`)
      if (policy.to) parts.push(`to ${policy.to}`)
      return `Traffic ${parts.join(" ")}`
    }
    case "rule": {
      const parts = []
      if (policy.fwmark) parts.push(`marked ${policy.fwmark}`)
      if (policy.iif) parts.push(`arriving on ${policy.iif}`)
      return `Packets ${parts.join(" and ")}`
    }
  }
}

/** The path a member stands for. */
export function egressHop(member: Pick<EgressMember, "kind" | "gateway" | "device">): string {
  return member.kind === "tunnel"
    ? `through ${member.device}`
    : `via ${member.gateway} on ${member.device}`
}

export function egressMemberReading(member: EgressMemberView): { label: string; tone: Tone } {
  if (member.state === "down") return { label: "Down", tone: "danger" }
  if (member.state === "up" && member.proven) return { label: "Up", tone: "success" }
  if (member.state === "up") return { label: "Up, evidence stale", tone: "warning" }
  return { label: "Measuring", tone: "default" }
}

export function egressNames(group: Pick<EgressGroup, "members">, ids: number[] = []): string {
  const names = ids.map((id) => group.members.find((m) => m.id === id)?.name ?? `member ${id}`)
  return names.join(" and ") || "no member"
}

/** The group's state in one reading. */
export function egressGroupReading(group: EgressGroup): { label: string; tone: Tone } {
  const active = group.members.filter((m) => m.active)
  if (!group.enabled) return { label: "Not carrying traffic", tone: "default" }
  if (active.some((m) => m.state === "down"))
    return {
      label: `Carrying traffic through ${egressNames(group, group.active)}, which is down`,
      tone: "danger",
    }
  if (group.members.every((m) => m.state === "down"))
    return { label: "No member is up", tone: "danger" }
  return { label: `Carrying traffic through ${egressNames(group, group.active)}`, tone: "success" }
}

/** Which manual switches the members allow now. */
export function egressManualActions(group: EgressGroup): {
  failover: boolean
  failback: boolean
} {
  const tier = Math.min(...group.members.filter((m) => m.active).map((m) => m.priority))
  const usable = group.members.filter((m) => m.state !== "down")
  return {
    failover: usable.some((m) => !m.active),
    failback: usable.some((m) => !m.active && m.priority < tier),
  }
}

const EVENT_WORD: Record<EgressEvent["kind"], string> = {
  state: "Measured",
  switch: "Switched",
  hold: "Held",
  refused: "Refused",
  failed: "Failed",
  verify: "Verified",
  connections: "Connections",
  simulation: "Simulation",
  automation: "Automation",
  config: "Changed",
}

export function egressEventReading(event: EgressEvent): { label: string; tone: Tone } {
  let label = EVENT_WORD[event.kind] ?? event.kind
  if (event.kind === "switch" && event.action) {
    label = event.action === "manual" ? "Switched by hand" : capitalize(event.action)
  }
  if (event.outcome === "applying") return { label: `${label}, in progress`, tone: "warning" }
  if (event.outcome === "interrupted") return { label: `${label}, interrupted`, tone: "warning" }
  if (event.outcome === "pending")
    return { label: `${label}, awaiting confirmation`, tone: "warning" }
  if (event.outcome === "recovered") return { label: `${label}, restored`, tone: "warning" }
  if (event.outcome === "unknown") return { label: `${label}, outcome unknown`, tone: "warning" }
  switch (event.kind) {
    case "refused":
    case "failed":
      return { label, tone: "danger" }
    case "switch":
      return { label, tone: event.outcome === "applied" ? "success" : "danger" }
    case "state":
      return {
        label: event.reason.includes(" is down ") ? "Member down" : "Member up",
        tone: event.reason.includes(" is down ") ? "danger" : "success",
      }
    case "hold":
      return { label, tone: "warning" }
    case "verify":
      return { label, tone: event.outcome === "failed" ? "danger" : "default" }
    case "simulation":
      return {
        label,
        tone:
          event.outcome === "applied"
            ? "success"
            : event.outcome === "failed"
              ? "danger"
              : "default",
      }
  }
  return { label, tone: "default" }
}

function capitalize(word: string) {
  return word.slice(0, 1).toUpperCase() + word.slice(1)
}

/** Whether a simulation authorises automation for the configuration shown. */
export function egressSimulationReading(
  sim: EgressSimulation | undefined,
  fingerprint: string,
): { label: string; tone: Tone; current: boolean } {
  if (!sim) return { label: "Not simulated", tone: "warning", current: false }
  const current = sim.fingerprint === fingerprint
  switch (sim.status) {
    case "running":
      return { label: "Running", tone: "default", current }
    case "passed":
      return current
        ? { label: "Passed", tone: "success", current }
        : { label: "Passed for an earlier configuration", tone: "warning", current }
    case "failed":
      return { label: "Failed", tone: "danger", current }
    case "interrupted":
      return { label: "Interrupted", tone: "warning", current }
    default:
      return { label: "Could not complete", tone: "danger", current }
  }
}

/** Each phase with the switches and holds the rules made in it. */
export function egressPhaseSummary(sim: EgressSimulation): {
  name: string
  description: string
  samples: number
  switches: { sample: number; action: string; active: number[]; reason: string }[]
  holds: number
}[] {
  const result = sim.result
  if (!result) return []
  return result.phases.map((phase) => {
    const steps = result.steps.filter((s) => s.index >= phase.start && s.index <= phase.end)
    return {
      name: phase.name,
      description: phase.description,
      samples: steps.length,
      switches: steps
        .filter((s) => s.switched)
        .map((s) => ({
          sample: s.index - phase.start + 1,
          action: s.decision.action,
          active: s.active,
          reason: s.decision.reason,
        })),
      holds: steps.filter((s) => s.decision.action === "hold").length,
    }
  })
}

/** A form's editable state: every number as typed. */
export type EgressDraft = {
  name: string
  family: "inet" | "inet6"
  policy: { kind: EgressPolicy["kind"]; from: string; to: string; iif: string; fwmark: string }
  members: {
    id?: number
    name: string
    kind: EgressMember["kind"]
    gateway: string
    device: string
    source: string
    priority: string
    weight: string
  }[]
  probes: { kind: EgressProbe["kind"]; target: string; port: string; name: string }[]
  thresholds: Record<keyof EgressThresholds, string>
  sticky: boolean
  connections: "flush" | "keep"
  failback: "automatic" | "manual"
  protected: string
}

function thresholdDraft(t: EgressThresholds): Record<keyof EgressThresholds, string> {
  return Object.fromEntries(Object.entries(t).map(([k, v]) => [k, String(v)])) as Record<
    keyof EgressThresholds,
    string
  >
}

export function emptyEgressDraft(): EgressDraft {
  return {
    name: "",
    family: "inet",
    policy: { kind: "all", from: "", to: "", iif: "", fwmark: "" },
    members: [
      {
        name: "",
        kind: "gateway",
        gateway: "",
        device: "",
        source: "",
        priority: "1",
        weight: "1",
      },
      {
        name: "",
        kind: "gateway",
        gateway: "",
        device: "",
        source: "",
        priority: "2",
        weight: "1",
      },
    ],
    probes: [{ kind: "icmp", target: "", port: "", name: "" }],
    thresholds: thresholdDraft(DEFAULT_THRESHOLDS),
    sticky: false,
    connections: "flush",
    failback: "automatic",
    protected: "",
  }
}

export function egressDraftFromGroup(group: EgressGroup): EgressDraft {
  return {
    name: group.name,
    family: group.family,
    policy: {
      kind: group.policy.kind,
      from: group.policy.from ?? "",
      to: group.policy.to ?? "",
      iif: group.policy.iif ?? "",
      fwmark: group.policy.fwmark ?? "",
    },
    members: group.members.map((m) => ({
      id: m.id,
      name: m.name,
      kind: m.kind,
      gateway: m.gateway ?? "",
      device: m.device,
      source: m.source ?? "",
      priority: String(m.priority),
      weight: String(m.weight),
    })),
    probes: group.probes.map((p) => ({
      kind: p.kind,
      target: p.target,
      port: p.port ? String(p.port) : "",
      name: p.name && p.name !== "." ? p.name : "",
    })),
    thresholds: thresholdDraft(group.thresholds),
    sticky: group.sticky,
    connections: group.connections,
    failback: group.failback,
    protected: (group.protected ?? []).join(", "),
  }
}

const LITERAL = /^[0-9a-fA-F:.]+$/

function looksLikeAddress(value: string, family: "inet" | "inet6") {
  const v = value.trim()
  if (!LITERAL.test(v)) return false
  return family === "inet6" ? v.includes(":") : /^\d{1,3}(\.\d{1,3}){3}$/.test(v)
}

/** The problems the server would refuse, said before a request is sent. */
export function egressDraftProblems(draft: EgressDraft): string[] {
  const out: string[] = []
  if (!draft.name.trim()) out.push("Name the group.")
  if (draft.members.length < 2) out.push("A group needs at least two members to fail over between.")
  if (draft.members.length > 8) out.push("A group has at most eight members.")
  draft.members.forEach((m, i) => {
    const label = m.name.trim() || `Member ${i + 1}`
    if (!m.device.trim()) out.push(`${label} needs a device.`)
    if (m.kind === "gateway" && !looksLikeAddress(m.gateway, draft.family))
      out.push(`${label} needs a gateway address of the group's family.`)
    if (m.source.trim() && !looksLikeAddress(m.source, draft.family))
      out.push(`${label}'s source is not an address of the group's family.`)
  })
  const hops = draft.members.map(
    (m) => `${m.device.trim()}|${m.kind === "gateway" ? m.gateway.trim() : ""}`,
  )
  if (new Set(hops).size !== hops.length) out.push("Two members are the same next hop.")
  if (draft.probes.length < 1 || draft.probes.length > 4) out.push("Measure 1 to 4 probe targets.")
  draft.probes.forEach((p, i) => {
    if (!looksLikeAddress(p.target, draft.family))
      out.push(`Probe ${i + 1} needs a literal target address of the group's family.`)
  })
  if (draft.policy.kind === "selector" && !draft.policy.from.trim() && !draft.policy.to.trim())
    out.push("An address policy needs a source or a destination network.")
  if (draft.policy.kind === "rule" && !draft.policy.iif.trim() && !draft.policy.fwmark.trim())
    out.push("A rule policy needs a packet mark or an incoming interface.")
  const t = Object.fromEntries(
    Object.entries(draft.thresholds).map(([k, v]) => [k, Number(v)]),
  ) as unknown as EgressThresholds
  if (Object.values(t).some((v) => !Number.isFinite(v) || !Number.isInteger(v)))
    out.push("Every threshold is a whole number.")
  else {
    if (t.timeoutMillis >= t.intervalSeconds * 1000)
      out.push("The probe timeout must be shorter than the interval.")
    if (t.latencyMillis >= t.timeoutMillis)
      out.push("The latency threshold must be below the probe timeout.")
  }
  if (draft.sticky && draft.connections !== "flush")
    out.push("A sticky group flushes the connections of a member that goes down.")
  return out
}

/** The request body a draft becomes. */
export function egressRequestFromDraft(draft: EgressDraft) {
  const policy: EgressPolicy = { kind: draft.policy.kind }
  if (draft.policy.kind === "selector") {
    if (draft.policy.from.trim()) policy.from = draft.policy.from.trim()
    if (draft.policy.to.trim()) policy.to = draft.policy.to.trim()
  }
  if (draft.policy.kind === "rule") {
    if (draft.policy.iif.trim()) policy.iif = draft.policy.iif.trim()
    if (draft.policy.fwmark.trim()) policy.fwmark = draft.policy.fwmark.trim()
  }
  return {
    name: draft.name.trim(),
    family: draft.family,
    policy,
    members: draft.members.map((m) => ({
      ...(m.id ? { id: m.id } : {}),
      name: m.name.trim(),
      kind: m.kind,
      ...(m.kind === "gateway" ? { gateway: m.gateway.trim() } : {}),
      device: m.device.trim(),
      ...(m.source.trim() ? { source: m.source.trim() } : {}),
      priority: Number(m.priority) || 1,
      weight: Number(m.weight) || 1,
    })),
    probes: draft.probes.map((p) => ({
      kind: p.kind,
      target: p.target.trim(),
      ...(p.kind !== "icmp" && p.port.trim() ? { port: Number(p.port) } : {}),
      ...(p.kind === "dns" && p.name.trim() ? { name: p.name.trim() } : {}),
    })),
    thresholds: Object.fromEntries(
      Object.entries(draft.thresholds).map(([k, v]) => [k, Number(v)]),
    ) as EgressThresholds,
    sticky: draft.sticky,
    connections: draft.connections,
    failback: draft.failback,
    protected: draft.protected
      .split(/[\s,]+/)
      .map((p) => p.trim())
      .filter(Boolean),
  }
}

/** Seconds as a short duration. */
export function egressDuration(seconds: number): string {
  if (seconds < 60) return `${seconds} s`
  if (seconds % 60 === 0) return `${seconds / 60} min`
  return `${Math.floor(seconds / 60)} min ${seconds % 60} s`
}
