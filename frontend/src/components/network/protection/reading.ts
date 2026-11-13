import type {
  BlocklistCoverage,
  BlocklistDiff,
  Conntrack,
  KernelProfile,
  LimitProfile,
  ProtectionBlocklist,
  ProtectionLimit,
  ProtectionSetting,
  ProtectionTrusted,
  ProtectionView,
  SeriesPoint,
} from "@/lib/types"
import type { Tone } from "@/components/tone"
import { countryName } from "@/lib/countries"
import { portProduct } from "@/components/product-logo"

/**
 * What a limit, a blocklist, a trusted address and a kernel setting say about
 * themselves, and the request bodies that save them unchanged — the Protection
 * page describes each one in a row, an editor and a confirmation, and says it
 * from here so the three cannot drift into three sentences.
 */

/** The body `POST /protection/limits` and `PUT /protection/limits/{id}` take. */
export type LimitRequest = {
  name: string
  protocol: ProtectionLimit["protocol"]
  ports: string
  rate: number
  per: ProtectionLimit["per"]
  burst: number
  perSource: boolean
  maxConnections: number
  globalConnections: number
  profile: string
  action: ProtectionLimit["action"]
  enabled?: boolean
}

/** The body `POST /protection/blocklists` and `PUT /protection/blocklists/{id}` take. */
export type BlocklistRequest = {
  name: string
  kind: ProtectionBlocklist["kind"]
  entries: string[]
  /** ISO 3166-1 alpha-2, lower-case: the server refuses anything else. */
  countries: string[]
  url: string
  preset: string
  /** A fetched list's schedule; empty keeps the existing one. */
  refresh?: string
  signatureUrl?: string
  publicKey?: string
  enabled?: boolean
}

const PER_WORD: Record<ProtectionLimit["per"], string> = {
  second: "a second",
  minute: "a minute",
  hour: "an hour",
}

const count = (n: number, one: string, many: string) =>
  `${n.toLocaleString()} ${n === 1 ? one : many}`

/** `22/tcp`, `53/tcp+udp`: the port or range and the protocols it covers. */
export function portsWord(limit: Pick<ProtectionLimit, "ports" | "protocol">) {
  return `${limit.ports}/${limit.protocol === "both" ? "tcp+udp" : limit.protocol}`
}

/** The rate half of a limit: "10 new connections a minute per address, bursts of 20". */
export function rateWord(limit: ProtectionLimit) {
  if (limit.rate <= 0) return undefined
  const base = `${count(limit.rate, "new connection", "new connections")} ${PER_WORD[limit.per]}`
  const each = limit.perSource ? " per address" : ""
  const burst = limit.burst > 0 ? `, bursts of ${limit.burst.toLocaleString()}` : ""
  return `${base}${each}${burst}`
}

/** The ceiling half of a limit: "at most 50 open per address, 500 for everyone". */
export function ceilingWord(limit: ProtectionLimit) {
  const each =
    limit.maxConnections > 0
      ? `at most ${limit.maxConnections.toLocaleString()} open per address`
      : undefined
  const all =
    (limit.globalConnections ?? 0) > 0
      ? `${(limit.globalConnections ?? 0).toLocaleString()} open for everyone`
      : undefined
  if (each && all) return `${each}, ${all}`
  return each ?? (all ? `at most ${all}` : undefined)
}

/** A limit as the line its row carries: "22/tcp · 10 new connections a minute per address · drop". */
export function limitSentence(limit: ProtectionLimit) {
  return [portsWord(limit), rateWord(limit), ceilingWord(limit), limit.action]
    .filter(Boolean)
    .join(" · ")
}

/** The product that usually listens on a limit's first port, for its mark. */
export function limitProduct(limit: Pick<ProtectionLimit, "ports">) {
  const port = Number.parseInt(limit.ports, 10)
  return Number.isInteger(port) ? portProduct(port) : undefined
}

/** A limit as the request that would save it unchanged, optionally with its switch moved. */
export function limitRequest(limit: ProtectionLimit, enabled?: boolean): LimitRequest {
  return {
    name: limit.name,
    protocol: limit.protocol,
    ports: limit.ports,
    rate: limit.rate,
    per: limit.per || "second",
    burst: limit.burst,
    perSource: limit.perSource,
    maxConnections: limit.maxConnections,
    globalConnections: limit.globalConnections ?? 0,
    profile: limit.profile ?? "",
    action: limit.action,
    ...(enabled === undefined ? {} : { enabled }),
  }
}

/** A list as the request that would save it unchanged, optionally with its switch moved. */
export function blocklistRequest(
  list: ProtectionBlocklist,
  presets: ProtectionView["presets"],
  enabled?: boolean,
): BlocklistRequest {
  const preset = list.kind === "feed" ? presets.find((p) => p.url === list.url)?.id : undefined
  return {
    name: list.name,
    kind: list.kind,
    entries: list.kind === "manual" ? list.entries : [],
    countries: list.kind === "country" ? list.countries : [],
    url: list.kind === "feed" && !preset ? list.url : "",
    preset: preset ?? "",
    // The server keeps the pinned key when the same signature is named.
    ...(list.signatureUrl ? { signatureUrl: list.signatureUrl } : {}),
    ...(enabled === undefined ? {} : { enabled }),
  }
}

/** The name a feed list is called when the reader does not name it: its preset, else its host. */
export function feedName(presets: ProtectionView["presets"], preset: string, url: string) {
  const named = presets.find((p) => p.id === preset)
  if (named) return named.name
  try {
    return new URL(url).host
  } catch {
    return ""
  }
}

/** The names of a country list's countries, as many as a line holds and a count for the rest. */
export function countriesWord(codes: string[], shown = 4) {
  const names = codes.slice(0, shown).map(countryName)
  const more = codes.length - names.length
  return more > 0 ? `${names.join(" · ")} · +${more}` : names.join(" · ")
}

/** What a list is, as a line: its countries, its feed or how many addresses were typed. */
export function listWhat(list: ProtectionBlocklist, presets: ProtectionView["presets"]) {
  switch (list.kind) {
    case "country":
      return countriesWord(list.countries)
    case "feed":
      return presets.find((p) => p.url === list.url)?.name ?? feedName(presets, "", list.url)
    default:
      return count(list.entries.length, "address", "addresses") + " typed"
  }
}

/** Whether a list is fetched, and so has something to refresh. */
export const isFetched = (list: Pick<ProtectionBlocklist, "kind">) => list.kind !== "manual"

/** How the trusted set says where an address came from. */
export const ORIGIN_WORD: Record<ProtectionView["trusted"][number]["origin"], string> = {
  loopback: "this machine",
  allowlist: "your allowlist",
  you: "you",
  kept: "kept",
}

/** A connection table's level as the tone every reading in the product uses. */
export function levelTone(level: Conntrack["level"]): Tone {
  return level === "critical" ? "danger" : level === "warning" ? "warning" : "default"
}

/**
 * The words a choice setting's values go by. Zero and one are off and on for
 * all of them but the reverse-path filter, whose two on-values are loose and
 * strict — the page offers loose only, because strict breaks asymmetric routing
 * and tunnels.
 */
export function choiceWord(key: string, value: string) {
  if (key.endsWith("rp_filter")) {
    return value === "0" ? "Off" : value === "1" ? "Strict" : value === "2" ? "Loose" : value
  }
  return value === "0" ? "Off" : value === "1" ? "On" : value
}

/** The value as the reader sees it: a choice by its word, a number as itself. */
export const settingWord = (s: ProtectionSetting, value: string) =>
  s.kind === "choice" ? choiceWord(s.key, value) : Number(value).toLocaleString("en-US")

/** The groups the kernel's settings are read in, by what each one defends. */
export const SETTING_GROUPS: { title: string; keys: string[] }[] = [
  {
    title: "Connection floods",
    keys: [
      "net.ipv4.tcp_syncookies",
      "net.ipv4.tcp_max_syn_backlog",
      "net.ipv4.tcp_synack_retries",
      "net.ipv4.tcp_rfc1337",
      "net.netfilter.nf_conntrack_max",
    ],
  },
  {
    title: "Spoofed addresses",
    keys: [
      "net.ipv4.conf.all.rp_filter",
      "net.ipv4.conf.default.rp_filter",
      "net.ipv4.conf.all.accept_source_route",
      "net.ipv6.conf.all.accept_source_route",
      "net.ipv4.conf.all.log_martians",
    ],
  },
  {
    title: "Redirects and broadcasts",
    keys: [
      "net.ipv4.conf.all.accept_redirects",
      "net.ipv6.conf.all.accept_redirects",
      "net.ipv4.conf.all.send_redirects",
      "net.ipv4.icmp_echo_ignore_broadcasts",
      "net.ipv4.icmp_ignore_bogus_error_responses",
    ],
  },
]

/** Which group a setting is read in; one the page has not met yet goes last. */
export function settingGroup(key: string) {
  return SETTING_GROUPS.find((g) => g.keys.includes(key))?.title ?? "Other settings"
}

/**
 * The settings the reader has edited, as the values that would be sent: only
 * those whose staged value differs from what the kernel is running.
 */
export function stagedChanges(settings: ProtectionSetting[], pending: Record<string, string>) {
  return Object.fromEntries(
    Object.entries(pending).filter(([key, value]) =>
      settings.some((s) => s.key === key && s.available && s.current !== value),
    ),
  )
}

/** Whether a number setting's staged text is a whole number inside its range. */
export function inRange(s: ProtectionSetting, value: string) {
  if (s.kind !== "number") return true
  if (!/^\d+$/.test(value.trim())) return false
  const n = Number(value)
  return n >= s.min && n <= s.max
}

/** The schedules a fetched list may keep, as the editor offers them. */
export const REFRESH_CHOICES = [
  { value: "6h", label: "Every 6 hours" },
  { value: "12h", label: "Every 12 hours" },
  { value: "24h", label: "Daily" },
  { value: "72h", label: "Every 3 days" },
  { value: "168h", label: "Weekly" },
  { value: "manual", label: "Only when refreshed by hand" },
] as const

/** How a fetched list is kept fresh, and when it next is: "daily · next in 3h". */
export function refreshWord(
  list: Pick<ProtectionBlocklist, "kind" | "refresh" | "nextRefresh" | "failures" | "stale">,
  until: (iso: string) => string,
) {
  if (list.kind === "manual") return undefined
  const label = REFRESH_CHOICES.find((c) => c.value === (list.refresh || "24h"))?.label ?? "Daily"
  const parts = [label.toLowerCase()]
  if ((list.failures ?? 0) > 0) {
    parts.push(`failing (${list.failures} ${list.failures === 1 ? "try" : "tries"})`)
  }
  if (list.stale) parts.push("stale")
  if (list.nextRefresh) parts.push(`next fetch ${until(list.nextRefresh)}`)
  return parts.join(" · ")
}

/** What a list's last fetch or edit changed: "+12 −3 at the last fetch". */
export function diffWord(diff: BlocklistDiff | null | undefined) {
  if (!diff) return undefined
  if (!diff.baseline) return `${diff.added.toLocaleString()} networks, nothing to compare with`
  if (diff.added === 0 && diff.removed === 0) return "unchanged at the last fetch"
  return `+${diff.added.toLocaleString()} −${diff.removed.toLocaleString()} at the last change`
}

/** How much address space a list takes, rather than how many lines it is. */
export function coverageWord(c: BlocklistCoverage | undefined) {
  if (!c) return undefined
  const parts: string[] = []
  if (c.ipv4Networks > 0) {
    const share = c.ipv4Share * 100
    parts.push(
      `${compactCount(c.ipv4Addresses)} IPv4 addresses (${share < 0.01 ? "<0.01" : share.toFixed(2)}% of IPv4)`,
    )
  }
  if (c.ipv6Networks > 0) {
    parts.push(
      c.ipv6Slash48s >= 1
        ? `${compactCount(Math.round(c.ipv6Slash48s))} IPv6 /48s`
        : `${c.ipv6Networks.toLocaleString()} IPv6 networks smaller than a /48`,
    )
  }
  return parts.join(" · ") || undefined
}

function compactCount(n: number) {
  return Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n)
}

/** The expiries the exception and trusted editors offer. */
export const EXPIRY_CHOICES = [
  { value: "", label: "Until removed" },
  { value: "1h", label: "1 hour" },
  { value: "24h", label: "1 day" },
  { value: "168h", label: "1 week" },
  { value: "720h", label: "30 days" },
] as const

/** An expiry choice as the RFC 3339 instant the server takes, from now. */
export function expiryFrom(choice: string, now: Date = new Date()) {
  const hours = Number.parseInt(choice, 10)
  if (!choice || !Number.isFinite(hours)) return ""
  return new Date(now.getTime() + hours * 3_600_000).toISOString().replace(/\.\d{3}Z$/, "Z")
}

/** The kernel values a profile would change, among those this host has. */
export function profileChanges(settings: ProtectionSetting[], profile: KernelProfile) {
  return Object.fromEntries(
    Object.entries(profile.values).filter(([key, value]) =>
      settings.some((s) => s.key === key && s.available && s.current !== value),
    ),
  )
}

/** A limit profile as the editor's fields. */
export function limitFromProfile(p: LimitProfile) {
  return {
    name: p.name,
    protocol: p.protocol,
    ports: p.ports,
    rate: p.rate ? String(p.rate) : "",
    per: p.per || ("minute" as const),
    burst: p.burst ? String(p.burst) : "",
    perSource: p.perSource,
    max: p.maxConnections ? String(p.maxConnections) : "",
    global: p.globalConnections ? String(p.globalConnections) : "",
    action: p.action,
  }
}

/** Who kept a trusted address and why, as its row's second line. */
export function trustedNoteWord(e: ProtectionTrusted, when: (iso: string) => string) {
  const parts: string[] = []
  if (e.reason) parts.push(e.reason)
  if (e.addedBy) parts.push(`kept by ${e.addedBy}`)
  if (e.expiresAt) parts.push(e.expired ? "expired" : `until ${when(e.expiresAt)}`)
  if (e.lastSeen) parts.push(`last signed in ${when(e.lastSeen)}`)
  else if (e.origin === "kept" || e.origin === "you") parts.push("no sign-in seen in 90 days")
  return parts.join(" · ")
}

/** A recorded series as chart rows, by timestamp in milliseconds. */
export function seriesRows(series: Record<string, SeriesPoint[] | undefined>, keys: string[]) {
  const byTs = new Map<number, Record<string, number> & { ts: number }>()
  for (const key of keys) {
    for (const p of series[key] ?? []) {
      const ts = p.t * 1000
      const row = byTs.get(ts) ?? { ts }
      row[key] = p.value
      byTs.set(ts, row)
    }
  }
  return [...byTs.values()].sort((a, b) => a.ts - b.ts)
}

/**
 * What the page says of itself on its opening line and its section heads:
 * what the lists dropped and the limits refused since the table was loaded,
 * the same since the recorder first saw each rule, and what is in force.
 */
export function protectionTotals(
  view: Pick<ProtectionView, "blocklists" | "limits" | "exceptions">,
) {
  const sum = <T>(rows: T[], of: (row: T) => number) => rows.reduce((n, row) => n + of(row), 0)
  // The earliest a group's totals reach; a group with none yet says only what
  // the loaded table counted.
  const since = (rows: { total?: { since: string | null } }[]) =>
    rows
      .map((row) => row.total?.since)
      .filter((at): at is string => Boolean(at))
      .sort()[0]
  const lists = view.blocklists
  const on = lists.filter((l) => l.enabled)
  const limitsOn = view.limits.filter((l) => l.enabled)
  const exceptions = view.exceptions ?? []
  return {
    lists: lists.length,
    listsOn: on.length,
    networks: sum(on, (l) => l.count),
    failing: lists.filter((l) => l.error).length,
    dropped: sum(lists, (l) => l.packets),
    droppedBytes: sum(lists, (l) => l.bytes),
    droppedEver: sum(lists, (l) => l.total?.packets ?? 0),
    droppedSince: since(lists),
    limits: view.limits.length,
    limitsOn: limitsOn.length,
    refused: sum(view.limits, (l) => l.packets),
    refusedBytes: sum(view.limits, (l) => l.bytes),
    refusedEver: sum(view.limits, (l) => l.total?.packets ?? 0),
    refusedSince: since(view.limits),
    exceptions: exceptions.length,
    exceptionsLive: exceptions.filter((e) => !e.expired).length,
    letThrough: sum(exceptions, (e) => e.packets),
    inForce: on.length > 0 || limitsOn.length > 0,
  }
}

/** A state word and the dot it stands beside. */
export type StateWord = {
  label: string
  tone: "running" | "stopped" | "warning" | "danger"
  /** The switch beside it already says this much: a narrow row leaves it out. */
  plain?: boolean
}

/**
 * The one state a blocklist's row says: the worst thing true of it. A row is
 * one line, so a list that is signed, stale and failing says it is failing;
 * its editor lists the rest (`listFacts`).
 */
export function listState(
  list: Pick<
    ProtectionBlocklist,
    "enabled" | "error" | "containsYou" | "enforcement" | "stale" | "integrity"
  >,
): StateWord {
  if (list.error) return { label: "Fetch failed", tone: "danger" }
  if (list.containsYou) return { label: "Holds your address", tone: "warning" }
  if (list.enabled && list.enforcement === "degraded")
    return { label: "Degraded enforcement", tone: "warning" }
  if (list.enabled && list.stale) return { label: "Stale", tone: "warning" }
  if (list.enabled && list.enforcement === "unknown")
    return { label: "Enforcement unknown", tone: "warning" }
  if (!list.enabled) return { label: "Off", tone: "stopped", plain: true }
  if (list.enforcement === "verified") return { label: "Verified set", tone: "running" }
  if (list.integrity === "signed") return { label: "Signed", tone: "running" }
  return { label: "In force", tone: "running", plain: true }
}

/** Everything a list's row has no room for, as the short facts its editor opens on. */
export function listFacts(
  list: ProtectionBlocklist,
  presets: ProtectionView["presets"],
  when: (iso: string) => string,
) {
  const fetched = isFetched(list)
  return [
    listWhat(list, presets),
    `${list.count.toLocaleString()} ${list.count === 1 ? "network" : "networks"}`,
    coverageWord(list.coverage),
    fetched ? (list.refreshed ? `fetched ${when(list.refreshed)}` : "not fetched yet") : undefined,
    fetched ? refreshWord(list, when) : undefined,
    diffWord(list.lastDiff),
    list.integrity === "signed" ? "signed" : undefined,
    list.containsYou ? "holds your address" : undefined,
    list.enabled && list.enforcement === "verified" ? "verified set" : undefined,
    list.enabled && list.enforcement === "degraded" ? "degraded enforcement" : undefined,
    list.enabled && list.enforcement === "unknown" ? "enforcement unknown" : undefined,
  ].filter((fact): fact is string => Boolean(fact))
}

/** A list's coverage in the width a row has: the share without "of IPv4". */
export function coverageShort(c: BlocklistCoverage | undefined) {
  return coverageWord(c)?.replace("% of IPv4)", "%)")
}

/** When a fetched list was last read and when it next is, as a row's two short lines. */
export function fetchWords(
  list: Pick<
    ProtectionBlocklist,
    "kind" | "refreshed" | "refresh" | "nextRefresh" | "failures" | "enabled"
  >,
  when: (iso: string) => string,
) {
  if (list.kind === "manual") return undefined
  const label = REFRESH_CHOICES.find((c) => c.value === (list.refresh || "24h"))?.label ?? "Daily"
  const failures = list.failures ?? 0
  return {
    fetched: list.refreshed ? `fetched ${when(list.refreshed)}` : "not fetched yet",
    next: [
      label.toLowerCase(),
      failures > 0 ? `${failures} failed ${failures === 1 ? "try" : "tries"}` : undefined,
      list.enabled && list.nextRefresh ? `next ${when(list.nextRefresh)}` : undefined,
    ]
      .filter(Boolean)
      .join(" · "),
  }
}

/** A limit in the few words a picture's node has: its rate, else its ceiling. */
export function limitShort(limit: ProtectionLimit) {
  if (limit.rate > 0)
    return `${limit.rate.toLocaleString()} ${PER_WORD[limit.per]}${limit.perSource ? " per address" : ""}`
  if (limit.maxConnections > 0) return `${limit.maxConnections.toLocaleString()} open per address`
  if ((limit.globalConnections ?? 0) > 0)
    return `${(limit.globalConnections ?? 0).toLocaleString()} open for everyone`
  return undefined
}

/** How many sources a limit's per-source sets track now, and how full they are. */
export function metersWord(limit: Pick<ProtectionLimit, "meters">) {
  const m = limit.meters
  if (!m || m.error) return undefined
  if (m.rateSources === null && m.connSources === null) return undefined
  const tracked = Math.max(m.rateSources ?? 0, m.connSources ?? 0)
  const full = tracked / m.capacity
  return `tracking ${tracked.toLocaleString()} source${tracked === 1 ? "" : "s"}${full >= 0.8 ? ` (${Math.round(full * 100)}% of its meter)` : ""}`
}

/** How an exception stands: it ends, it ended, or it holds until it is removed. */
export function exceptionState(
  e: { expiresAt: string | null; expired: boolean },
  when: (iso: string) => string,
): StateWord {
  if (!e.expiresAt) return { label: "Until removed", tone: "running" }
  return e.expired
    ? { label: `Ended ${when(e.expiresAt)}`, tone: "warning" }
    : { label: `Ends ${when(e.expiresAt)}`, tone: "running" }
}

/** What a trusted address's row says of its standing, when there is anything to say. */
export function trustedState(
  e: Pick<ProtectionTrusted, "stale" | "expired">,
): StateWord | undefined {
  if (e.expired) return { label: "Expired", tone: "warning" }
  if (e.stale) return { label: "Stale", tone: "warning" }
  return undefined
}

/**
 * The choice the trusted editor opens on, and what each choice sends. An
 * address that already ends keeps its end unless the reader picks another:
 * the server takes an empty expiry as "never", so a note saved from a form
 * that opened on "Until removed" quietly made a week's trust permanent.
 */
export const KEEP_EXPIRY = "keep"

export function trustedExpiryChoice(e: Pick<ProtectionTrusted, "expiresAt" | "expired">) {
  return e.expiresAt && !e.expired ? KEEP_EXPIRY : "never"
}

export function trustedExpiry(
  e: Pick<ProtectionTrusted, "expiresAt" | "expired">,
  choice: string,
  now: Date = new Date(),
) {
  if (choice === KEEP_EXPIRY) return e.expiresAt && !e.expired ? e.expiresAt : ""
  return expiryFrom(choice === "never" ? "" : choice, now)
}

/**
 * The control a kernel setting takes in the settings column: a switch for one
 * the kernel holds as off or on, segments for another small choice, a field
 * for a number. A switch is only honest while the value is one of its two, so
 * an on/off setting found at a third value keeps its segments.
 */
export function settingControl(s: ProtectionSetting, value: string) {
  if (s.kind !== "choice" || s.allowed.length > 3) return "number" as const
  const onOff = s.allowed.length === 2 && s.allowed.includes("0") && s.allowed.includes("1")
  return onOff && (value === "0" || value === "1") ? ("switch" as const) : ("segments" as const)
}

/** How many of each kind the picture draws; the lists under it hold the rest. */
export const LISTS_DRAWN = 3
export const LIMITS_DRAWN = 1

/** Every counter the picture's lines are alive by, under the key `grown` compares. */
export function protectionCounters(
  view: Pick<ProtectionView, "blocklists" | "limits" | "exceptions">,
) {
  return Object.fromEntries([
    ...view.blocklists.map((l) => [`list:${l.id}`, l.packets] as const),
    ...view.limits.map((l) => [`limit:${l.id}`, l.packets] as const),
    ["exceptions", (view.exceptions ?? []).reduce((n, e) => n + e.packets, 0)] as const,
  ])
}
