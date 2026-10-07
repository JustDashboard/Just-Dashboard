import type {
  Conntrack,
  ProtectionBlocklist,
  ProtectionLimit,
  ProtectionSetting,
  ProtectionView,
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

/** The ceiling half of a limit: "at most 50 open per address". */
export function ceilingWord(limit: ProtectionLimit) {
  if (limit.maxConnections <= 0) return undefined
  return `at most ${limit.maxConnections.toLocaleString()} open${limit.perSource ? " per address" : ""}`
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

/** A setting that is running and is not where it is recommended to be. */
export const below = (s: ProtectionSetting) => s.available && !s.atRecommended

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
