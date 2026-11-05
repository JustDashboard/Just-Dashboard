import type { HubLinkNotice, HubLinkServer, HubLinkSession, HubLinkStatus } from "@/lib/types"

/**
 * The pure half of everything a linked dashboard draws about its hub: the address the Settings
 * page offers to connect to, whether the shell is hub-aware at all, the servers the switcher and
 * the palette list and how each reads, which notices the rail still owes, and the words for the
 * link's state. The protocol is docs/internal/backend/hub-link-protocol.md; the section numbers
 * below are its.
 *
 * Every address here ends in a link a person presses or in `location.assign`, and every one of
 * them came from another machine by way of this server's cache. The backend already checks each
 * against §6's rules; `openable` checks again here, so a value that slipped past is a row without
 * a link rather than a `javascript:` URL.
 */

/** "hub.tailed39ba.ts.net", or "127.0.0.1:43180" for the test origin; the raw text if no URL. */
export function hubHost(url: string | undefined): string {
  if (!url) return ""
  try {
    return new URL(url).host || url
  } catch {
    return url
  }
}

const LOOPBACK = /^(localhost|127(\.\d{1,3}){3}|\[::1\])$/

/**
 * An address this page may send the browser to: an `https` origin, or a loopback `http` one,
 * which only the test origin produces (§2.3). Anything else is not a link.
 */
export function openable(url: string | undefined): string | undefined {
  if (!url) return undefined
  try {
    const parsed = new URL(url)
    if (parsed.username || parsed.password) return undefined
    if (parsed.protocol === "https:") return parsed.href
    if (parsed.protocol === "http:" && LOOPBACK.test(parsed.hostname)) return parsed.href
    return undefined
  } catch {
    return undefined
  }
}

/** A notice's link, kept only when it is on the hub's own origin (§6.2). */
export function noticeHref(href: string | undefined, hubUrl: string | undefined) {
  const target = openable(href)
  const hub = openable(hubUrl)
  if (!target || !hub) return undefined
  return new URL(target).origin === new URL(hub).origin ? target : undefined
}

const HOST = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:\d{1,5})?$/

/**
 * What `?connect=<hub host>` fills the address field with (§8.1): `https://` and the host. The
 * hub's Connect button writes a bare host; a value that is not one — a path, a user name, another
 * scheme — fills nothing, because a link that someone else wrote is not allowed to put an address
 * in front of the person deciding. The backend applies the URL rule to whatever is submitted.
 *
 * The test origin is the one `http` hub, so a host that matches it fills it in as it is.
 */
export function connectTarget(param: string | null | undefined, testOrigin?: string): string {
  const raw = (param ?? "")
    .trim()
    .toLowerCase()
    .replace(/^https:\/\//, "")
    .replace(/\/$/, "")
    // A fully qualified name's trailing dot, before any port: the spelling §2.2 step 3 drops.
    .replace(/\.(?=(:\d+)?$)/, "")
  if (!raw || !HOST.test(raw)) return ""
  if (testOrigin && hubHost(testOrigin) === raw) return testOrigin
  return `https://${raw.replace(/:443$/, "")}`
}

/** What the shell knows about its hub, or nothing: the gate every hub feature is drawn behind. */
export type ShellHub = {
  name: string
  url: string
  reachable: boolean
  fleet: HubLinkServer[]
  notices: HubLinkNotice[]
}

/**
 * Whether the shell is hub-aware, and with what. The session's `hub` field decides (§8.3); once
 * `/dashboard/hub` has answered, it can only take that away — a disconnect from the terminal or a
 * removal in the hub — and it brings the freshest reachability, since the session was read once
 * when the page loaded.
 */
export function shellHub(
  session: HubLinkSession | undefined,
  status: HubLinkStatus | undefined,
): ShellHub | undefined {
  if (!session?.linked) return undefined
  if (status && (!status.enabled || status.state !== "linked")) return undefined
  return {
    name: status?.hubName ?? session.name,
    url: status?.hubUrl ?? session.url,
    reachable: status?.reachable ?? session.reachable,
    fleet: status?.fleet ?? [],
    notices: status?.notices ?? [],
  }
}

/** A tone a `Status` dot takes; the subset of `DotTone` a server or a link can be in. */
export type ReadingTone = "running" | "stopped" | "warning" | "danger" | "notice" | "unknown"

export type Reading = { tone: ReadingTone; label: string }

const SERVER_READING: Record<HubLinkServer["state"], Reading> = {
  online: { tone: "running", label: "Online" },
  late: { tone: "warning", label: "Late" },
  offline: { tone: "danger", label: "Offline" },
  updating: { tone: "notice", label: "Updating" },
}

/**
 * How a server in the fleet reads. The states are the hub's (§6.1). While the hub cannot be
 * reached they are only what it said last, so the others read as unknown rather than as a green
 * that may be minutes old, and this server — which is plainly up, since it drew the page — says
 * it is not reaching the hub.
 */
export function serverReading(server: HubLinkServer, reachable: boolean): Reading {
  if (!reachable) {
    return server.self
      ? { tone: "warning", label: "Not reaching the hub" }
      : { tone: "unknown", label: "Unknown" }
  }
  return SERVER_READING[server.state] ?? { tone: "unknown", label: "Unknown" }
}

const byName = new Intl.Collator("en", { numeric: true, sensitivity: "base" })

/** This server first, then the rest by name, so a long list is found by reading down it. */
export function orderFleet(fleet: readonly HubLinkServer[]): HubLinkServer[] {
  return [...fleet].sort(
    (a, b) => Number(b.self) - Number(a.self) || byName.compare(a.name, b.name),
  )
}

/** The fleet's entry for this server: its name is the one the hub shows, and the tab's. */
export function selfServer(fleet: readonly HubLinkServer[]): HubLinkServer | undefined {
  return fleet.find((server) => server.self)
}

/** The address another server opens at: its own origin, never a path from the hub. */
export function serverAddress(server: HubLinkServer): string | undefined {
  const url = openable(server.endpoint)
  return url && new URL(url).origin
}

/** What a palette entry for a server carries; the palette adds its mark and its action. */
export type ServerEntry = {
  id: string
  kind: "server"
  title: string
  detail: string
  keywords: string[]
  /** Where choosing it goes: "/" for this server, another server's origin, the hub. */
  href?: string
  /** True for this server, whose entry is a page here rather than a place elsewhere. */
  self?: boolean
  hub?: boolean
  reading?: Reading
  facts: { label: string; value: string; mono?: boolean }[]
}

/**
 * The palette's Servers: every server the hub lists, this one first, then the hub itself (the
 * design report's "Open vps-07749119"). A server with no address is still listed, so a search
 * for it says it is there and cannot be opened from here, rather than that it does not exist.
 */
export function serverEntries(hub: ShellHub): ServerEntry[] {
  const servers = orderFleet(hub.fleet).map((server): ServerEntry => {
    const address = serverAddress(server)
    const reading = serverReading(server, hub.reachable)
    return {
      id: `server:${server.serverId}`,
      kind: "server",
      title: server.name,
      detail: server.self
        ? "This server"
        : [reading.label, server.version].filter(Boolean).join(" · "),
      keywords: [hubHost(address), server.version].filter(Boolean),
      href: server.self ? "/" : address,
      self: server.self || undefined,
      reading,
      facts: [
        ...(address ? [{ label: "Address", value: hubHost(address), mono: true }] : []),
        ...(server.version ? [{ label: "Version", value: server.version }] : []),
        ...(!address && !server.self ? [{ label: "Opens", value: "No address reported" }] : []),
      ],
    }
  })
  const url = openable(hub.url)
  return [
    ...servers,
    {
      id: "server:hub",
      kind: "server",
      title: "Just Hub",
      detail: hubHost(hub.url),
      keywords: ["hub", hub.name, hubHost(hub.url)],
      href: url,
      hub: true,
      reading: hub.reachable
        ? { tone: "running", label: "Reachable" }
        : { tone: "warning", label: "Unreachable" },
      facts: [
        { label: "Address", value: hubHost(hub.url), mono: true },
        { label: "Servers", value: String(hub.fleet.length) },
      ],
    },
  ]
}

/** The tone a notice takes: the shared severity union of `components/tone.ts`. */
export function noticeTone(level: HubLinkNotice["level"]): "default" | "warning" | "danger" {
  return level === "critical" ? "danger" : level === "warning" ? "warning" : "default"
}

/** The notices still owed to this browser, newest first as the hub sent them. */
export function owedNotices(
  notices: readonly HubLinkNotice[],
  dismissed: readonly string[],
): HubLinkNotice[] {
  const gone = new Set(dismissed)
  return notices.filter((notice) => !gone.has(notice.id))
}

/**
 * The dismissals worth remembering: a notice the hub no longer carries is over (§6.2), and if it
 * comes back under the same id it is news again — so its dismissal is forgotten with it, and the
 * list never grows past the twenty notices a reply can hold.
 */
export function keptDismissals(
  dismissed: readonly string[],
  notices: readonly HubLinkNotice[],
): string[] {
  const live = new Set(notices.map((notice) => notice.id))
  return dismissed.filter((id) => live.has(id))
}

/** The link's state in a word and a tone, for the identity line's far end. */
export function linkReading(status: HubLinkStatus): Reading {
  if (!status.enabled) return { tone: "stopped", label: "Turned off" }
  if (status.state === "pending") return { tone: "warning", label: "Waiting for approval" }
  if (status.state === "linked") {
    if (status.reachable === false) return { tone: "warning", label: "Connected, hub unreachable" }
    if (!status.lastHeartbeatAt) return { tone: "notice", label: "Connected, first report due" }
    return { tone: "running", label: "Connected" }
  }
  return { tone: "stopped", label: "Not connected" }
}

/** Who linked it, in words: an account's name, or "the terminal" for `manage.sh hub connect`. */
export function linkedByWords(linkedBy: string): string {
  return linkedBy === "cli" ? "the terminal" : linkedBy
}

/** "1 server", "3 servers". */
export function serverCount(count: number): string {
  return `${count} ${count === 1 ? "server" : "servers"}`
}

/** The terminal's way to connect, as the Settings page offers it beside its button (§8.4). */
export function terminalCommand(url: string): string {
  return `sudo ./scripts/manage.sh hub connect ${url || "URL"}`
}
