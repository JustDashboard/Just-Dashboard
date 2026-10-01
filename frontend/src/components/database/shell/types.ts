import type { DbAccess, DbCapabilities, DbConnection, DbFlavor } from "@/lib/types"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"

/**
 * One connection as `GET /databases/{id}` answers it: the saved row, what the
 * server said it is, where it runs and whether it is up — read without
 * dialling every other connection the way the fleet does.
 *
 * A backend that predates the route has none; the shell then draws from the
 * saved row and a ping, and every field here is simply not known.
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

export type DbConnectionSummary = DbConnection & {
  flavor: DbFlavor
  flavorLabel: string
  versionNumber?: string
  state: DbState
  ok: boolean
  error?: string
  latencyMs: number
  source: "docker" | "host" | "file" | "remote" | "unknown"
  container?: DbContainerRef
  unit?: DbUnitRef
  file?: DbFileRef
  power: DbPower
  exposure: DbAccess["exposure"] | "unknown"
  managed: boolean
  consumers: number
  lastBackup?: string
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
