import { buildDsn } from "@/lib/db-dsn"
import type { DbAccess, DbConnection } from "@/lib/types"
import type { ConnectParts, Engine } from "@/components/database/engine"

/**
 * The connection string, in every shape it is asked for.
 *
 * Two things decide a string: where it is read from, and what is going to
 * read it. A database on this machine has up to three addresses — the one the
 * dashboard itself uses, the name a linked deployment's containers reach it
 * by, and the machine's public address when the port is published — and each
 * is a target of the audited `GET /databases/{id}/url`. What reads it wants a
 * URL, a line of an `.env` file, the command that opens a shell, or a client
 * library's own few lines; those are written here, from the engine registry's
 * words.
 *
 * Everything in this file is built from facts the page already holds, with
 * the password hidden. Drawing a string therefore reveals nothing and audits
 * nothing; the real one is fetched only when somebody asks to see or copy it.
 */

/** What stands where the password is until it is revealed. */
export const MASK = "••••••"

export type ConnectTargetId = "host" | "container" | "public"

export type ConnectTarget = {
  /** The `target` the URL route takes. */
  id: ConnectTargetId
  label: string
  /** The address the string names. Absent with `unavailable`. */
  host?: string
  port?: string
  /** Who can use this string, in a line. */
  note: string
  /** Why this target has no string, in place of one. */
  unavailable?: string
}

/** The name a database answers to on the network a linked deployment shares with it. */
function linkedName(id: number) {
  return `db-${id}.jd.internal`
}

/** The address the operator's own network can be relied on to route to: IPv4 where there is one. */
function publicAddress(addresses: string[]): string | undefined {
  return addresses.find((address) => !address.includes(":")) ?? addresses[0]
}

/**
 * Where the database can be connected from.
 *
 * `access` is an administrator's reading (`GET /databases/{id}/access`).
 * Without it — a role that may not have it, or before it lands — there is one
 * target: the address as saved. The same goes for a file, which has no
 * network, and for a server on another machine, whose saved address is the
 * one everybody uses.
 */
export function connectTargets(
  conn: DbConnection,
  engine: Engine,
  access?: DbAccess,
): ConnectTarget[] {
  const saved: ConnectTarget = {
    id: "host",
    label: "As saved",
    host: conn.host,
    port: conn.port,
    note: "The address this dashboard connects to.",
  }
  if (!engine.can("server")) {
    return [{ ...saved, note: "A file on this server; a program on it opens the path directly." }]
  }
  if (!access) return [saved]
  if (access.exposure === "remote") {
    return [{ ...saved, note: "Anything that can reach that address connects with it." }]
  }

  const address = publicAddress(access.publicAddresses)
  const published = access.exposure === "public" && Boolean(address)
  return [
    {
      id: "host",
      label: "This server",
      host: conn.host,
      port: conn.port,
      note: "Reachable from programs on this server.",
    },
    access.container
      ? {
          id: "container",
          label: "Inside Docker",
          host: linkedName(conn.id),
          port: engine.defaultPort,
          note: "The name a deployment linked to this database reaches it by, on the private network the dashboard joins them on.",
        }
      : {
          id: "container",
          label: "Inside Docker",
          note: "",
          unavailable:
            "This server runs on the host itself, not in a container, so it has no name on a Docker network. A container reaches it through the host's own address.",
        },
    published
      ? {
          id: "public",
          label: "Anywhere",
          host: address,
          port: conn.port,
          note: "Reachable from anywhere with the password. Treat the string as a secret.",
        }
      : { id: "public", label: "Anywhere", note: "", unavailable: unpublished(access) },
  ]
}

/** Why there is no string for a laptop, in one sentence. */
function unpublished(access: DbAccess): string {
  if (access.exposure === "public") {
    return `This machine has no public address of its own — the provider maps one in front of it. Use that address with port ${access.port}.`
  }
  if (access.exposure === "private") {
    return "Published on one address only. Whoever can reach that address connects on the same port."
  }
  return access.managed
    ? "Not reachable from outside this server. Open it up under Settings to connect from your own machine."
    : "Not reachable from outside this server."
}

/** The parts a snippet is written from, with the password hidden. */
export function maskedParts(
  conn: DbConnection,
  engine: Engine,
  target: ConnectTarget,
): ConnectParts {
  const host = target.host ?? conn.host
  const port = target.port ?? conn.port
  const dsn = buildDsn(conn.driver, {
    host,
    port,
    user: conn.user,
    password: MASK,
    database: conn.database,
    option: "",
  }).replace(encodeURIComponent(MASK), MASK)
  return { url: engine.url(dsn), host, port, user: conn.user, database: conn.database }
}

/** The same parts around the real string, once the server has handed it over. */
export function revealedParts(parts: ConnectParts, engine: Engine, dsn: string): ConnectParts {
  return { ...parts, url: engine.url(dsn) }
}

export type ConnectFormat = { id: string; label: string }

/** The shapes a string is offered in: the URL, an `.env` line, the engine's shell, its clients. */
export function connectFormats(engine: Engine): ConnectFormat[] {
  return [
    { id: "url", label: "URL" },
    { id: "env", label: ".env" },
    ...(engine.cli ? [{ id: "cli", label: engine.cli }] : []),
    ...engine.clients.map((client) => ({ id: client.id, label: client.label })),
  ]
}

/** The string in the shape asked for. An unknown shape is the URL. */
export function connectSnippet(engine: Engine, parts: ConnectParts, format: string): string {
  if (format === "env") return `${engine.env}=${parts.url}`
  if (format === "cli") return engine.command(parts)
  return engine.clients.find((client) => client.id === format)?.code(parts) ?? parts.url
}

/**
 * Whether a snippet holds the password. A shell that takes fields asks for it
 * itself and a file has none, so those are copied as drawn — with no reveal
 * to audit and nothing an administrator has to do.
 */
export function holdsSecret(snippet: string): boolean {
  return snippet.includes(MASK)
}
