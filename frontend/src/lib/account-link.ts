import { bytes, duration, percent } from "@/lib/format"
import type { AccountLinkScope, AccountLinkStatus } from "@/lib/types"

/**
 * The pure half of the Connect page: what each scope shares and lets the site do, the heartbeat
 * the dashboard last sent read as rows a person can check, the picture's four kinds of data, and
 * the countdown on a link request. The wording of the scopes is the contract's scope table
 * (docs/server-link-protocol.md); the CLI help names the same things, so a change goes to both.
 */

export const SCOPES: readonly AccountLinkScope[] = ["observe", "operate", "maintain"]

export function scopeName(scope: AccountLinkScope): string {
  return scope[0].toUpperCase() + scope.slice(1)
}

/** One thing the heartbeat carries; `key` is what the page draws it as. */
export type SharedItem = { key: string; label: string }

/** What the heartbeat carries at `observe`, and so at every scope. */
const OBSERVED: SharedItem[] = [
  { key: "version", label: "version and update state" },
  { key: "os", label: "OS, kernel and architecture" },
  { key: "uptime", label: "uptime" },
  { key: "access", label: "access path (tailnet address or SSH tunnel port)" },
  { key: "metrics", label: "CPU, memory, disk, load and network" },
  { key: "counts", label: "container and backup counts" },
  { key: "alerts", label: "alerts firing" },
]

/** The names `operate` adds. Names only: never an image, a variable or a path. */
const NAMED: SharedItem = {
  key: "names",
  label: "container, stack, backup-job and last-deployed project names",
}

/**
 * Never in the heartbeat at any scope (plan §3.6 and §4.4). Said next to what is shared because
 * the list of what leaves a root-equivalent machine is only half of a trust decision.
 */
export const NEVER_SENT: readonly string[] = [
  ".env contents",
  "API keys",
  "Accounts",
  "The network allowlist",
  "Logs",
  "File contents",
  "Connection strings",
  "Deploy variables",
  "Backup credentials",
]

const upper = (text: string) => text[0].toUpperCase() + text.slice(1)
const capital = (item: SharedItem) => ({ ...item, label: upper(item.label) })

/** Everything the heartbeat carries at this scope, one thing to an entry. */
export function sharedItems(scope: AccountLinkScope): SharedItem[] {
  return (scope === "observe" ? OBSERVED : [...OBSERVED, NAMED]).map(capital)
}

/** The scope card's sentence for what it shares. */
export function scopeShares(scope: AccountLinkScope): string {
  switch (scope) {
    case "observe":
      return OBSERVED.map((item) => upper(item.label)).join(" · ")
    case "operate":
      return `Everything in Observe, plus ${NAMED.label}.`
    case "maintain":
      return `The same as Operate: everything in Observe, plus ${NAMED.label}.`
  }
}

/** The scope card's sentence for what the site may do with it. */
export function scopeAllows(scope: AccountLinkScope): string {
  switch (scope) {
    case "observe":
      return "Refresh the snapshot."
    case "operate":
      return "Also start, stop or restart a container, restart a stack and run a backup job."
    case "maintain":
      return "Also install a dashboard update, set the automatic-update policy and restart the dashboard."
  }
}

const COMMAND_WORDS: Record<string, string> = {
  "snapshot.now": "Refresh the snapshot",
  "container.start": "Start a container",
  "container.stop": "Stop a container",
  "container.restart": "Restart a container",
  "stack.restart": "Restart a stack",
  "backup.run": "Run a backup job",
  "dashboard.update.install": "Install a dashboard update",
  "dashboard.update.policy": "Set the automatic-update policy",
  "dashboard.restart": "Restart the dashboard",
}

/** A command's kind in words; a kind this page does not know is shown as it was sent. */
export function commandWords(kind: string): string {
  return COMMAND_WORDS[kind] ?? kind
}

/** "just-dashboard.com", or "localhost:3000" in development; the raw text if it is not a URL. */
export function siteHost(siteUrl: string): string {
  try {
    return new URL(siteUrl).host || siteUrl
  } catch {
    return siteUrl
  }
}

/**
 * A request's remaining time as the clock a person reads: "14:32". Past the deadline it is
 * "Expired", which the dashboard's own poll confirms within seconds by dropping the request.
 */
export function countdown(expiresAt: string, now: number): { label: string; expired: boolean } {
  const deadline = Date.parse(expiresAt)
  // An unreadable deadline is the backend's bug, not a reason to disable approving.
  if (!Number.isFinite(deadline)) return { label: "—", expired: false }
  const left = Math.ceil((deadline - now) / 1000)
  if (left <= 0) return { label: "Expired", expired: true }
  const pad = (n: number) => String(n).padStart(2, "0")
  const hours = Math.floor(left / 3600)
  const clock = `${pad(Math.floor((left % 3600) / 60))}:${pad(left % 60)}`
  return { label: hours > 0 ? `${hours}:${clock}` : clock, expired: false }
}

/** How long a link request stands before the site drops it. */
export const REQUEST_LIFETIME = 15 * 60_000

/** Ten missed heartbeats (they go out every 30 s) is a link that is not working. */
const STALE_AFTER = 300_000

export function heartbeatStale(lastHeartbeatAt: string | undefined, now: number): boolean {
  if (!lastHeartbeatAt) return false
  const at = Date.parse(lastHeartbeatAt)
  return Number.isFinite(at) && now - at > STALE_AFTER
}

type Policy = NonNullable<NonNullable<AccountLinkStatus["link"]>["updatePolicy"]>

const AUTO_WORDS: Record<Policy["auto"], string> = {
  off: "Off",
  patch: "Patch releases",
  all: "All releases",
}

/** The update policy in the words the site sets it in. */
export function policyWords(policy: Policy | undefined): { auto: string; window?: string } {
  if (!policy || policy.auto === "off") return { auto: AUTO_WORDS.off }
  const { start, end } = policy.window
  return {
    auto: AUTO_WORDS[policy.auto],
    window: start && end ? `between ${start} and ${end}, server time` : undefined,
  }
}

export type SharedRow = {
  label: string
  value: string
  /** The row is a moment: an ISO time the page can draw in the reader's locale. */
  time?: string
  /** A list of records, one line each, under the row's count. */
  items?: string[]
}

export type SharedGroup = { key: string; label: string; rows: SharedRow[] }

/** Words whose plain reading is wrong or shouted: the heartbeat's keys are camelCase. */
const WORDS: Record<string, string> = {
  cpu: "CPU",
  os: "OS",
  ssh: "SSH",
  tls: "TLS",
  ip: "IP",
  ip4: "IPv4",
  ip6: "IPv6",
  psi: "PSI",
  id: "ID",
  url: "URL",
  mem: "memory",
  net: "network",
  rx: "received",
  tx: "sent",
}

const words = (key: string) =>
  key
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .toLowerCase()
    .split(" ")
    .filter(Boolean)
    .map((word) => WORDS[word] ?? word)

/** "cpuPercent" under "metrics" is "CPU"; the unit moves into the value, so it leaves the name. */
export function fieldLabel(path: string[]): string {
  const last = path.length - 1
  const parts = path.flatMap((key, i) => {
    const w = words(key)
    return i === last && w.length > 1 && /(Percent|Seconds)$/.test(key) ? w.slice(0, -1) : w
  })
  return parts.length ? upper(parts.join(" ")) : ""
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value)

const isMoment = (key: string, value: number) =>
  value > 0 && (key === "at" || key === "since" || /At$/.test(key))

const isSize = (key: string) => /^(mem|swap|disk)(Used|Total|Free)$/.test(key) || /Bytes$/.test(key)

/** The protocol's times are unix seconds; anything past the year 5138 in seconds is milliseconds. */
const momentISO = (value: number) => new Date(value < 1e11 ? value * 1000 : value).toISOString()

const utc = (iso: string) => `${iso.slice(0, 16).replace("T", " ")} UTC`

function scalar(key: string, value: unknown): { value: string; time?: string } {
  if (value === null || value === undefined || value === "") return { value: "—" }
  if (typeof value === "boolean") return { value: value ? "yes" : "no" }
  if (typeof value === "number") {
    if (isMoment(key, value)) {
      const iso = momentISO(value)
      return { value: utc(iso), time: iso }
    }
    if (/Percent$/.test(key)) return { value: percent(value) }
    if (/Seconds$/.test(key)) return { value: duration(value) }
    if (isSize(key)) return { value: bytes(value) }
    return { value: String(Math.round(value * 100) / 100) }
  }
  return { value: String(value) }
}

/** One record of a list on one line: `shop-web · ghcr.io/… · running · healthy`. */
function summary(record: unknown): string {
  if (!isRecord(record)) return scalar("", record).value
  return Object.entries(record)
    .filter(([, value]) => !isRecord(value) && !Array.isArray(value))
    .map(([key, value]) => scalar(key, value).value)
    .join(" · ")
}

function row(path: string[], value: unknown): SharedRow {
  const key = path[path.length - 1]
  const label = fieldLabel(path)
  if (!Array.isArray(value)) return { label, ...scalar(key, value) }
  if (value.length === 0) return { label, value: "none" }
  if (!value.some((entry) => isRecord(entry) || Array.isArray(entry))) {
    return { label, value: value.map((entry) => scalar(key, entry).value).join(", ") }
  }
  return {
    label,
    value: `${value.length} ${value.length === 1 ? "entry" : "entries"}`,
    items: value.map(summary),
  }
}

function flatten(record: Record<string, unknown>, path: string[]): SharedRow[] {
  return Object.entries(record).flatMap(([key, value]) =>
    isRecord(value) && Object.keys(value).length > 0
      ? flatten(value, [...path, key])
      : [row([...path, key], isRecord(value) ? null : value)],
  )
}

/**
 * The last heartbeat as the groups a person reads it in: each top-level object is a group, and
 * the fields that sit beside them (the protocol, when it was sent, the alerts) are the first,
 * "Heartbeat". Built from what was actually sent rather than from a list of what should be, so
 * a field the dashboard adds shows up here the day it starts leaving the machine.
 */
export function sharedGroups(snapshot: Record<string, unknown> | undefined): SharedGroup[] {
  if (!snapshot) return []
  const groups: SharedGroup[] = []
  const loose: SharedRow[] = []
  for (const [key, value] of Object.entries(snapshot)) {
    if (isRecord(value) && Object.keys(value).length > 0) {
      groups.push({ key, label: fieldLabel([key]), rows: flatten(value, []) })
    } else {
      loose.push(row([key], isRecord(value) ? null : value))
    }
  }
  return loose.length ? [{ key: "heartbeat", label: "Heartbeat", rows: loose }, ...groups] : groups
}
