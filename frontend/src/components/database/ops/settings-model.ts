import type { DbAccess, DbConnection } from "@/lib/types"

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
    word: "One private address",
    sentence: "It listens on one address of this machine — a private network or a VPN.",
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
  if (!firewall.backend && !firewall.active)
    return "No firewall is active on this machine: the binding alone decides."
  if (!firewall.active)
    return `${firewall.backend ?? "The firewall"} is installed and switched off: the binding alone decides.`
  return `${firewall.backend ?? "The firewall"} is on, and port ${port ?? "?"} is ${firewall.open ? "open to anywhere" : "closed"}.`
}

/** What changing the reach did to the firewall, from the route's one word. */
export function firewallOutcome(word: string, port: number | undefined): string {
  switch (word) {
    case "opened":
      return `A firewall rule now allows port ${port ?? ""} from anywhere.`.replace("  ", " ")
    case "closed":
      return "The firewall rule this dashboard wrote for it was removed."
    case "already":
      return "The firewall already had the rule."
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
