"use client"

import { useEffect, useSyncExternalStore } from "react"
import { api } from "@/lib/api"
import { notify } from "@/lib/toast"
import { readableDuration } from "@/components/database/data/view"
import type { DbMaintenanceAction, DbMaintenanceResult } from "@/components/database/schema/types"

/**
 * The maintenance commands in flight, held outside any one reading of a
 * table.
 *
 * A command runs on a request held open until the engine is done — a reindex
 * of a large table is minutes — and the server stops the command when that
 * request is dropped. The run used to live in the Statistics panel, so
 * looking at the table's columns while it worked took the panel away, and
 * with it the request: the command died to a press on a tab. Here a run
 * belongs to its table, not to what is on screen: it goes on while the reader
 * reads something else, the table's head says it is running wherever they
 * are in the schema browser, and only Stop ends it.
 *
 * What a run answered is kept for its table too, so the output is there when
 * the reader comes back to Statistics; and where nobody is looking at it when
 * it lands, a toast says how it went. A tab that is closed mid-run would take
 * the request with it, so the browser is asked to confirm leaving while one
 * is going.
 */
export type MaintenanceRun = {
  action: DbMaintenanceAction
  /** The action as it was asked for: "Reindex concurrently". */
  label: string
  /** The table, as a sentence says it. */
  table: string
  startedAt: number
}

export type MaintenanceOutcome = {
  action: DbMaintenanceAction
  label: string
  result?: DbMaintenanceResult
  error?: Error
  /** When it landed: a new outcome is a new time. */
  at: number
}

type Flight = MaintenanceRun & { controller: AbortController }

const flights = new Map<string, Flight>()
const outcomes = new Map<string, MaintenanceOutcome>()
/** How many panels are showing each table's outcome: what lands unseen is said in a toast. */
const readers = new Map<string, number>()
const listeners = new Set<() => void>()
let version = 0

/** What a table's runs are kept under. */
export function maintenanceKey(id: number, schema: string, table: string): string {
  return `${id}\u0000${schema}\u0000${table}`
}

/** Leaving the tab drops the request, and with it the command: the browser asks first. */
const warn = (event: BeforeUnloadEvent) => event.preventDefault()

function changed() {
  version += 1
  if (flights.size > 0) window.addEventListener("beforeunload", warn)
  else window.removeEventListener("beforeunload", warn)
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** Starts one command for a table. One runs at a time per table: a second ask while one is going is dropped. */
export function startMaintenance({
  id,
  schema,
  table,
  action,
  label,
  options,
}: {
  id: number
  schema: string
  table: string
  action: DbMaintenanceAction
  label: string
  options?: Record<string, boolean | string>
}) {
  const key = maintenanceKey(id, schema, table)
  if (flights.has(key)) return
  const controller = new AbortController()
  flights.set(key, { action, label, table, startedAt: Date.now(), controller })
  outcomes.delete(key)
  changed()
  const settle = (outcome: Omit<MaintenanceOutcome, "action" | "label" | "at">) => {
    flights.delete(key)
    const unseen = (readers.get(key) ?? 0) === 0
    if (controller.signal.aborted) {
      notify.info(`${label} on ${table} was stopped`)
    } else {
      outcomes.set(key, { action, label, at: Date.now(), ...outcome })
      if (unseen && outcome.error) notify.error(`${label} did not run on ${table}`, outcome.error)
      else if (unseen && outcome.result) {
        const said = `${label} on ${table} ${outcome.result.ok ? "finished" : "ran and reported a problem"}`
        const detail = { description: `In ${readableDuration(outcome.result.duration)}.` }
        if (outcome.result.ok) notify.success(said, detail)
        else notify.warning(said, detail)
      }
    }
    changed()
  }
  api<DbMaintenanceResult>(`/databases/${id}/maintenance`, {
    method: "POST",
    body: { action: action.id, schema: schema || undefined, table, options },
    signal: controller.signal,
  }).then(
    (result) => settle({ result }),
    (err: unknown) => settle({ error: err instanceof Error ? err : new Error(String(err)) }),
  )
}

/** Ends a table's run: the request is dropped and the server stops the command. */
export function stopMaintenance(key: string) {
  flights.get(key)?.controller.abort()
}

/** A table's run in flight and what its last one answered, kept in step with the store. */
export function useMaintenance(key: string): {
  run: MaintenanceRun | undefined
  outcome: MaintenanceOutcome | undefined
} {
  useSyncExternalStore(
    subscribe,
    () => version,
    () => 0,
  )
  return { run: flights.get(key), outcome: outcomes.get(key) }
}

/** Says a panel is showing this table's outcome, for as long as it is mounted. */
export function useMaintenanceReader(key: string) {
  useEffect(() => {
    readers.set(key, (readers.get(key) ?? 0) + 1)
    return () => {
      const left = (readers.get(key) ?? 1) - 1
      if (left > 0) readers.set(key, left)
      else readers.delete(key)
    }
  }, [key])
}
