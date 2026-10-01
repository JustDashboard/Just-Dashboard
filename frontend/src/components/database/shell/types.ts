import type { DbAccess, DbCapabilities, DbConnection, DbFlavor } from "@/lib/types"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"

/**
 * What the server behind a connection is doing. A stopped or paused one was
 * not dialled: its container or unit says it is down. `broken` is the saved
 * connection itself: its row can no longer be opened.
 */
export type DbState = "running" | "stopped" | "paused" | "unreachable" | "broken"

export type DbContainerRef = {
  id: string
  name: string
  image: string
  /** Docker's word: running, exited, paused, restarting, created, dead. */
  state: string
  status?: string
  health?: string
  composeProject?: string
  composeService?: string
}

export type DbUnitRef = { name: string; activeState: string; subState?: string }

export type DbFileRef = { path: string; exists: boolean; size: number; modified?: string }

/** What can start, stop or restart the server from here, and through what. */
export type DbPower = {
  via: "docker" | "systemd" | ""
  start: boolean
  stop: boolean
  restart: boolean
  /** Why nothing here can, when `via` is empty. */
  reason?: string
}

/**
 * One connection as `GET /databases/{id}` answers it: the saved row, what the
 * server said it is, where it runs and whether it is up — read without
 * dialling every other connection the way the fleet does. It answers 200
 * whatever the server is doing; only an id that names no connection is a 404.
 */
export type DbConnectionSummary = DbConnection & {
  /** The product behind the driver; the driver's own until the server has answered once. */
  flavor: DbFlavor
  flavorLabel: string
  /** The server's own description of itself ("PostgreSQL 16.4"), and the bare number in it. */
  version?: string
  versionNumber?: string
  state: DbState
  ok: boolean
  /** The engine's or the driver's own words when it did not answer, without the password. */
  error?: string
  latencyMs: number
  /** Where the server is; `unknown` beside a row that cannot be opened. */
  source: "docker" | "host" | "file" | "remote" | "unknown"
  container?: DbContainerRef
  unit?: DbUnitRef
  file?: DbFileRef
  power: DbPower
  /** How far the server's port reaches, and whether `PUT /access` can change that. */
  exposure: DbAccess["exposure"] | "unknown"
  managed: boolean
  /** How many deployment environments are bound to it. */
  consumers: number
  lastBackup?: string
  /** Resolved for this driver and the flavour that answered. Every key is present. */
  capabilities: DbCapabilities
  checkedAt: string
}

/** What an area's verbs are given to work with: the one confirmation the database's menu draws. */
export type DatabaseVerbTools = {
  confirm: (request: ConfirmRequest) => void
}

/**
 * An area's contribution to the database's menu: a hook returning its verbs
 * as data, already filtered by what the role may do and what this server can.
 */
export type DatabaseVerbSource = (tools: DatabaseVerbTools) => Verb[]
