import { duration } from "@/lib/format"
import type {
  HubLinkMachine,
  HubLinkNotice,
  HubLinkServer,
  HubLinkSession,
  HubLinkStatus,
} from "@/lib/types"

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

/**
 * Whether a hub URL is the test origin (§2.3), where no machine is asked about or pinned: the
 * Settings page says so in place of the machine.
 */
export function atTestOrigin(url: string | undefined, testOrigin: string | undefined): boolean {
  if (!url || !testOrigin) return false
  try {
    return new URL(url).origin === new URL(testOrigin).origin
  } catch {
    return false
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
 * The test origin is the one `http` hub (§2.3): a host that matches it, or that exact origin
 * written out, fills it in as it is, which is what a development hub's Connect button sends.
 */
export function connectTarget(param: string | null | undefined, testOrigin?: string): string {
  const exact = (param ?? "").trim().toLowerCase().replace(/\/$/, "")
  if (testOrigin && exact === testOrigin.toLowerCase()) return testOrigin
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

/*
 * Where a browser the shell sent to sign in comes back to afterwards lived here while the hub's
 * Connect door (`/dashboard/hub?connect=<hub host>`, §8.1) was the only address kept. Every page
 * behind the sign-in is kept now, by the rules in `lib/sign-in-return.ts`, which still gives the
 * door its `connect` and still treats a `next=` as somebody else's text.
 */

/**
 * A reason from the backend as a sentence of its own. The API words its reasons in lower case with
 * no full stop, to sit inside other text; standing alone under a title or a field they start with
 * a capital and end with a stop, as the page's other reasons do. Anything the hub wrote that the
 * backend quotes inside one is left as it is.
 */
export function sentence(text: string): string {
  const trimmed = text.trim()
  if (!trimmed) return ""
  const capital = trimmed.charAt(0).toUpperCase() + trimmed.slice(1)
  return /[.!?…]["'”’)]?$/.test(capital) ? capital : `${capital}.`
}

/** A request that got no answer at all, in Go's words: `Post "<url>": <why>`. */
const NO_ANSWER = /^(?:Get|Head|Post|Put|Patch|Delete) "[^"]*": ([\s\S]+)$/

/**
 * Connect's `502 hub_unreachable` (§8.2) names the hub it tried before what happened: `the hub at
 * <origin>: <why>`, where the why is Go's words for a request that got no answer, or the
 * backend's own sentence for a hub that answered with something else.
 */
const AT_HUB = /^the hub at (\S+): ([\s\S]+)$/

/** What Go's reason for no answer means to a person, checked in order. */
const NO_ANSWER_WHY: [RegExp, string][] = [
  [/connection refused/i, "the connection was refused"],
  [/no such host|server misbehaving/i, "its name did not resolve"],
  [/i\/o timeout|deadline exceeded|Client\.Timeout|timeout awaiting/i, "it did not answer in time"],
  [
    /no route to host|network is unreachable|host is unreachable/i,
    "there is no network route to it",
  ],
  [/x509|certificate|tls:/i, "its certificate was not accepted"],
  [/connection reset|broken pipe|EOF|server closed/i, "the connection closed before it answered"],
]

/**
 * The last exchange's failure (`admin.link.lastError`), or Connect's refusal when the hub gave it
 * nothing (`hub_unreachable`), as a person reads it. A request that got no answer is in Go's own
 * words — `Post "http://…/heartbeat": dial tcp …: connect: connection refused`, after "the hub at
 * <origin>: " in Connect's — so it is said as what happened, "Could not reach hub.…: the
 * connection was refused.", and Go's words are kept as `raw` for a disclosure. Connect's names
 * the host it tried, which is the one said. Anything else is already the backend's sentence, or
 * the hub's answer quoted and attributed by it (§8.10), and is said as it is.
 */
export function exchangeFailure(error: string, host: string): { message: string; raw?: string } {
  const raw = error.trim()
  const atHub = AT_HUB.exec(raw)
  const noAnswer = NO_ANSWER.exec(atHub ? atHub[2] : raw)
  if (!noAnswer) return { message: sentence(raw) }
  const where = atHub ? hubHost(atHub[1]) : host
  const why = NO_ANSWER_WHY.find(([pattern]) => pattern.test(noAnswer[1]))?.[1]
  return { message: why ? `Could not reach ${where}: ${why}.` : `Could not reach ${where}.`, raw }
}

/**
 * Whose machine an address is, the way the protocol words it (§8.2): "hub.tailed39ba.ts.net, a
 * machine of wayy01@github", or "…, tagged tag:server" for a machine with no person of its own.
 */
export function describeMachine(machine: HubLinkMachine): string {
  if (machine.tags.length > 0) return `${machine.name}, tagged ${machine.tags.join(", ")}`
  if (machine.owner) return `${machine.name}, a machine of ${machine.owner}`
  return machine.name
}

/**
 * The hub by the name it gave itself, "Just Hub (hub)". That name is `owner.label`, which the hub
 * chose and so is the hub's text too (§8.10): it is said inside the product's name, never alone
 * where the dashboard's own words go.
 */
export function hubLabel(name: string | undefined): string {
  return name ? `Just Hub (${name})` : "Just Hub"
}

/**
 * Who is speaking, for text the hub wrote (§8.10): "Just Hub (hub) says". Every notice, and every
 * message of the hub's, is drawn under this, never as the dashboard's own words.
 */
export function hubSays(name: string | undefined): string {
  return `${hubLabel(name)} says`
}

/**
 * The words an idle link's last error is given in by the backend (`hublink.refused`, around
 * `ErrURLRefused`). The status has no field of its own for it: an idle link is a stored link whose
 * `admin.link.lastError` is its reason, whatever the last exchange left behind, so the reason is
 * read back out of these words, which `hublink/status_test.go` holds the backend to.
 */
const IDLE =
  /^this server no longer accepts the hub's address: ([\s\S]+); disconnect, then connect again$/

/**
 * Why this server stopped using its link, when it has (§2.2 "When the rule runs", §2.4): the
 * hub's address fails the URL rule now — the server moved to another tailnet, the test origin was
 * removed — or another machine answers at the hub's name. An idle link stays stored and sends
 * nothing, commands and reports alike, until a person disconnects; a sync is refused. Only an
 * administrator is sent the reason; everyone is sent `idle` (`isIdle`).
 */
export function idleReason(status: HubLinkStatus): string | undefined {
  if (status.state !== "linked") return undefined
  const reason = IDLE.exec(status.admin?.link?.lastError ?? "")?.[1]?.trim()
  return reason ? reason.charAt(0).toUpperCase() + reason.slice(1) : undefined
}

/**
 * Whether this server has stopped using its link, for every reader. While it has, the stored hub
 * address may answer on a machine this server refused — after "the hub's machine changed" it is
 * exactly that — so nothing offers it as a link: not the page's title, not a notice, not the
 * switcher or the palette.
 */
export function isIdle(status: HubLinkStatus): boolean {
  return status.state === "linked" && (status.idle === true || Boolean(idleReason(status)))
}

/**
 * Whether the link is made and its first report is still on its way (§4.8). The backend calls a
 * link reachable only once a report has gone through, so from the approval until the first one it
 * answers `reachable: false` with no `lastHeartbeatAt` — a report due, not a hub that cannot be
 * reached. Only an administrator's status can tell the two apart, by the failure an attempt that
 * failed leaves in `admin.link.lastError`; without it, a link with no report reads as not reaching
 * the hub, as it did before, rather than as one about to.
 */
export function firstReportDue(status: HubLinkStatus): boolean {
  const link = status.admin?.link
  return (
    status.enabled &&
    status.state === "linked" &&
    !isIdle(status) &&
    !status.lastHeartbeatAt &&
    link !== undefined &&
    !link.lastError
  )
}

/**
 * How often the link reports (§4.8): the hub's interval, "every 15s", or before the status names
 * one, the hub's schedule. The hub sets it anywhere from 10 seconds to 10 minutes.
 */
export function reportCadence(seconds: number | undefined): string {
  return seconds && seconds > 0 ? `every ${duration(seconds)}` : "on the hub's schedule"
}

/** What the shell knows about its hub, or nothing: the gate every hub feature is drawn behind. */
export type ShellHub = {
  name: string
  /** The hub's address to open, or "" while the link is idle: then nothing links to it. */
  url: string
  reachable: boolean
  /**
   * Linked, and the first report not sent yet (`firstReportDue`): not reachable, and not trouble
   * either, so nothing says the hub cannot be reached.
   */
  due: boolean
  /** This server has stopped using the link (`isIdle`). */
  idle: boolean
  fleet: HubLinkServer[]
  /** The hub's notices; while the link is idle, without their links. */
  notices: HubLinkNotice[]
}

/**
 * Whether the shell is hub-aware, and with what. The session's `hub` field decides (§8.3); once
 * `/dashboard/hub` has answered, it can only take that away — a disconnect from the terminal or a
 * removal in the hub — and it brings the freshest reachability, since the session was read once
 * when the page loaded.
 *
 * An idle link keeps its name, its fleet and what it last said, but not one way to the hub: the
 * address is dropped here, once, so the switcher's "Open Just Hub", the palette's hub and every
 * notice's "Open in Just Hub" have nothing to link to.
 */
export function shellHub(
  session: HubLinkSession | undefined,
  status: HubLinkStatus | undefined,
): ShellHub | undefined {
  if (!session?.linked) return undefined
  if (status && (!status.enabled || status.state !== "linked")) return undefined
  const idle = status ? isIdle(status) : session.idle === true
  const notices = status?.notices ?? []
  return {
    name: status?.hubName ?? session.name,
    url: idle ? "" : (status?.hubUrl ?? session.url),
    reachable: !idle && (status?.reachable ?? session.reachable),
    due: status ? firstReportDue(status) : false,
    idle,
    fleet: status?.fleet ?? [],
    notices: idle ? notices.map((notice) => ({ ...notice, href: undefined })) : notices,
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
 * it is not reaching the hub, or, before its first report (`due`), that the report is due.
 */
export function serverReading(server: HubLinkServer, reachable: boolean, due = false): Reading {
  if (!reachable) {
    if (!server.self) return { tone: "unknown", label: "Unknown" }
    return due
      ? { tone: "notice", label: "First report due" }
      : { tone: "warning", label: "Not reaching the hub" }
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
 *
 * The names are the hub's words, so each says who listed it (§8.10); and the hub sends another
 * server's version as "" (§6.1), so a row says where a server opens rather than what it runs.
 */
export function serverEntries(hub: ShellHub): ServerEntry[] {
  const listedBy = { label: "Listed by", value: hubLabel(hub.name) }
  const servers = orderFleet(hub.fleet).map((server): ServerEntry => {
    const address = serverAddress(server)
    const reading = serverReading(server, hub.reachable, hub.due)
    return {
      id: `server:${server.serverId}`,
      kind: "server",
      title: server.name,
      // The state is the row's `Status` at its far end; the line beside the name says the rest.
      detail: server.self
        ? "This server"
        : [address ? hubHost(address) : "no address", server.lastSeenAt ? "" : "no report yet"]
            .filter(Boolean)
            .join(" · "),
      keywords: [hubHost(address), server.version].filter(Boolean),
      href: server.self ? "/" : address,
      self: server.self || undefined,
      reading,
      facts: [
        ...(address ? [{ label: "Address", value: hubHost(address), mono: true }] : []),
        ...(server.version ? [{ label: "Version", value: server.version }] : []),
        ...(server.self
          ? []
          : [{ label: "Opens", value: address ? "In a new tab" : "No address reported" }]),
        listedBy,
      ],
    }
  })
  const url = openable(hub.url)
  const host = hubHost(hub.url)
  return [
    ...servers,
    {
      id: "server:hub",
      kind: "server",
      title: hubLabel(hub.name),
      // An idle link has no address to open, and says why rather than a blank.
      detail: hub.idle ? "link idle" : host,
      keywords: ["hub", hub.name, host].filter(Boolean),
      href: url,
      hub: true,
      reading: hub.idle
        ? { tone: "danger", label: "Idle" }
        : hub.reachable
          ? { tone: "running", label: "Reachable" }
          : hub.due
            ? { tone: "notice", label: "First report due" }
            : { tone: "warning", label: "Unreachable" },
      facts: [
        ...(host ? [{ label: "Address", value: host, mono: true }] : []),
        { label: "Servers", value: String(hub.fleet.length) },
        ...(url
          ? [{ label: "Opens", value: "In a new tab" }]
          : hub.idle
            ? [{ label: "Opens", value: "Not while the link is idle" }]
            : []),
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

/**
 * The link's state in a word and a tone, for the identity line's far end: the one place the header
 * says it. A link whose first report is still due says so, not that the hub is unreachable, which
 * the backend's `reachable: false` would otherwise read as (`firstReportDue`).
 */
export function linkReading(status: HubLinkStatus): Reading {
  if (!status.enabled) return { tone: "stopped", label: "Turned off" }
  if (status.state === "pending") return { tone: "warning", label: "Waiting for approval" }
  if (status.state === "linked") {
    if (isIdle(status)) return { tone: "danger", label: "Idle" }
    if (firstReportDue(status)) return { tone: "notice", label: "Connected, first report due" }
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
