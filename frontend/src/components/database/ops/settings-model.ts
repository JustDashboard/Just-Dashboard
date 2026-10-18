import type { DbAccess, DbConnection, DbHostAddress } from "@/lib/types"

/**
 * What the Settings page says and sends, worked out from what it holds: which
 * of a connection's labels changed, the phrase a database's deletion is typed
 * with, what deleting does on each kind of engine, and why a server's reach
 * cannot be changed from here. No React.
 */

export type ConnectionDraft = {
  name: string
  environment: string
  notes: string
  readOnly: boolean
}

export function draftOf(conn: DbConnection): ConnectionDraft {
  return {
    name: conn.name,
    environment: conn.environment ?? "",
    notes: conn.notes ?? "",
    readOnly: Boolean(conn.readOnly),
  }
}

/**
 * The fields of a draft that differ from what is saved — and only those: the
 * route leaves an absent field as it is, so a save of the notes cannot undo a
 * protection somebody else switched on meanwhile.
 */
export function connectionChanges(
  saved: ConnectionDraft,
  draft: ConnectionDraft,
): Partial<ConnectionDraft> {
  const changes: Partial<ConnectionDraft> = {}
  if (draft.name.trim() !== saved.name) changes.name = draft.name.trim()
  if (draft.environment.trim() !== saved.environment) changes.environment = draft.environment.trim()
  if (draft.notes !== saved.notes) changes.notes = draft.notes
  if (draft.readOnly !== saved.readOnly) changes.readOnly = draft.readOnly
  return changes
}

/** The server keeps four thousand bytes of notes. */
export const NOTES_BYTES = 4000

export function notesProblem(notes: string): string | undefined {
  const size = new TextEncoder().encode(notes).length
  return size > NOTES_BYTES ? `At most ${NOTES_BYTES} bytes; this is ${size}.` : undefined
}

/** What an engine is, as far as deleting its database goes. */
export type DropKind = {
  /** The database is a file. */
  fileBased: boolean
  /** The server numbers its databases, and one is emptied rather than removed. */
  numbered: boolean
  /** The engine keeps documents. */
  documents: boolean
}

/**
 * The phrase the server wants typed to delete the database a connection
 * names: its name; a file's name rather than its path, which nobody types
 * without copying; a numbered database the way the engine itself writes it
 * (`db0`). Empty when the connection names nothing — then there is nothing to
 * delete, and no dialog is opened to ask.
 */
export function dropPhrase(conn: Pick<DbConnection, "database">, kind: DropKind): string {
  const name = (conn.database ?? "").trim()
  if (kind.fileBased) return name.split("/").pop() ?? ""
  if (kind.numbered) return `db${(name || "0").replace(/^db/, "")}`
  return name
}

/** What deleting the database does on this kind of engine, in a sentence. */
export function dropEffect(kind: DropKind, row: string): string {
  if (kind.fileBased) {
    return "The database file is deleted from this server's disk, with its write-ahead log. The connection goes with it."
  }
  if (kind.numbered) {
    return `Every ${row} in this numbered database is deleted. The numbered database itself stays, empty, and the connection keeps working.`
  }
  if (kind.documents) {
    return "The database is dropped with every collection, index and document in it. The server keeps running, and the connection goes with the database."
  }
  return "The database is dropped with every table, view, index and row in it. The server keeps running, and the connection goes with the database."
}

/** The words for where a server can be reached from. */
export const EXPOSURE: Record<string, { word: string; sentence: string }> = {
  local: {
    word: "This server only",
    sentence: "It listens on loopback: nothing outside this machine can reach it.",
  },
  private: {
    word: "One address of this machine",
    sentence:
      "It listens on one address of this machine — a tailnet, a VPN or a local network — and only that network reaches it.",
  },
  allowlist: {
    word: "Allowed addresses",
    sentence:
      "It listens on every interface, and the gateway refuses every address that is not on its list.",
  },
  public: {
    word: "Anywhere",
    sentence: "It listens on every interface: anything that can reach this machine can try it.",
  },
  remote: {
    word: "Its own machine decides",
    sentence:
      "The server is not on this machine, so where it listens is not this dashboard's to read.",
  },
  unknown: { word: "Not known", sentence: "The connection cannot be opened to find out." },
}

/**
 * Why where the server listens cannot be changed from this page, or nothing
 * when it can. The server refuses each of these; the page says so first, in
 * the words of what to do instead.
 */
export function reachRefusal(
  access: Pick<DbAccess, "managed" | "exposure" | "container" | "composeProject">,
): string | undefined {
  if (access.exposure === "remote") {
    return "The server runs on another machine. Where it listens is changed there."
  }
  if (access.composeProject) {
    return `${access.container ?? "Its container"} belongs to the compose project ${access.composeProject}: the port binding is changed in the compose file, and the stack redeployed.`
  }
  if (access.managed) return undefined
  if (access.exposure === "private") {
    return "It is published on one chosen address. That binding is changed on the container's own page."
  }
  if (access.container) {
    return `${access.container} publishes no port of its own to change. Publish one on the container's page first.`
  }
  return "No container of this dashboard publishes it: the server listens by its own configuration (its bind address), which is changed there."
}

/** What the firewall says about the server's port, in one line. */
export function firewallWords(access: Pick<DbAccess, "firewall" | "port">): string {
  const { firewall, port } = access
  const name = firewall.backend ?? "The firewall"
  if (!firewall.backend && !firewall.active)
    return "No firewall is active on this machine: the binding alone decides."
  if (!firewall.active) return `${name} is installed and switched off: the binding alone decides.`
  if (firewall.open) return `${name} is on, and port ${port ?? "?"} is open to anywhere.`
  if (firewall.allowed.length > 0)
    return `${name} is on, and lets port ${port ?? "?"} in from ${joinWords(firewall.allowed)}.`
  return `${name} is on, and port ${port ?? "?"} is closed.`
}

/** What changing the reach did to the firewall, from the route's one word. */
export function firewallOutcome(
  word: string,
  port: number | undefined,
  exposure?: DbAccess["exposure"],
): string {
  const listed = exposure === "allowlist"
  switch (word) {
    case "opened":
      return listed
        ? `Firewall rules now let port ${port ?? "?"} in from the listed addresses.`
        : `A firewall rule now allows port ${port ?? "?"} from anywhere.`
    case "updated":
      return listed
        ? `The firewall's rules for port ${port ?? "?"} now name the listed addresses.`
        : `The firewall's rules for port ${port ?? "?"} were replaced.`
    case "closed":
      return "The firewall rules this dashboard wrote for it were removed."
    case "already":
      return "The firewall already had the rules."
    case "inactive":
      return "A firewall is installed and switched off, so no rule was written."
    case "read-only":
      return "The firewall cannot be edited from here."
    case "none":
      return "This machine has no firewall, so the binding alone decides."
    default:
      return ""
  }
}

/** What changing the reach did at the gateway, from the route's one word. */
export function gatewayOutcome(word: string, port: number | undefined): string {
  switch (word) {
    case "kept":
      return `The gateway refuses new connections to port ${port ?? "?"} from every address not on the list.`
    case "released":
      return `The gateway no longer keeps port ${port ?? "?"} to a list.`
    case "restored":
      return "The gateway's list was put back as it was."
    default:
      return ""
  }
}

/** "a", "a and b", "a, b and c". */
export function joinWords(words: string[]): string {
  if (words.length < 2) return words.join("")
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`
}

/** The most addresses a list keeps a port to, as the server counts them. */
export const ALLOW_LIMIT = 16

/** An address with an optional prefix length, v4 or v6; the server has the last word. */
const SOURCE = /^(?:\d{1,3}(?:\.\d{1,3}){3}|[0-9a-f:]*:[0-9a-f:.]*)(?:\/\d{1,3})?$/i

/**
 * Why an address cannot go on the list as typed, or nothing. The server
 * normalises and decides; this only says the obvious before a request is
 * sent for it.
 */
export function sourceProblem(entry: string, list: string[]): string | undefined {
  const value = entry.trim()
  if (!value) return undefined
  if (!SOURCE.test(value))
    return "Not an address or a network, such as 203.0.113.7 or 198.51.100.0/24."
  if (/\/0$/.test(value)) return "That is every address there is: choose Anywhere instead."
  if (/^127\.|^::1$/.test(value)) return "That is this server, which always reaches it."
  if (list.includes(value)) return "Already on the list."
  if (list.length >= ALLOW_LIMIT)
    return `A list keeps at most ${ALLOW_LIMIT} addresses: join neighbours into one network.`
  return undefined
}

/** Whether two lists name the same addresses, in any order. */
export function sameList(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((entry) => b.includes(entry))
}

/** Who one of this machine's addresses faces, as a picker says it. */
export function addressWords(address: DbHostAddress): { label: string; hint: string } {
  const network = address.interface ? `${address.interface}'s network` : "that network"
  switch (address.network) {
    case "tailnet":
      return { label: "Your tailnet", hint: "Every device on your tailnet reaches it." }
    case "vpn":
      return {
        label: "A VPN",
        hint: `The peers on ${address.interface ?? "that VPN"} reach it.`,
      }
    case "uplink":
      return {
        label: "The provider's network",
        hint: `The machines on ${network} reach it — and the internet, if your provider maps a public address onto this one.`,
      }
    default:
      return { label: "A local network", hint: `The machines on ${network} reach it.` }
  }
}
