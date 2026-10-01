"use client"

import { useCallback, useMemo, useSyncExternalStore } from "react"
import { LockClosed, LockOpen, Play, RotateClockwise, StopCircle } from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { Verb } from "@/components/verbs"
import {
  POWER,
  changePower,
  powerChange,
  subscribePower,
  type PowerAction,
  type PowerChange,
  type PowerResponse,
} from "@/components/database/home/power"
import { powerConfirmation } from "@/components/database/home/power-subject"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import type {
  DatabaseVerbSource,
  DatabaseVerbTools,
  DbConnectionSummary,
} from "@/components/database/shell/types"

const noChange = () => undefined

/**
 * The change of power in flight for a connection, wherever it was begun: the
 * home's Start, the menu on another of the database's pages, another tab of
 * this one. A surface that draws the database reads it to say "Stopping…" and
 * to run its beam.
 */
export function usePowerChange(id: number): PowerChange | undefined {
  return useSyncExternalStore(subscribePower, () => powerChange(id), noChange)
}

/**
 * Starting, stopping and restarting the server behind the database this page
 * is about.
 *
 * `can` is what may be drawn: the summary says the action is possible for the
 * state the server is in (`power.start` is false on one already running), and
 * the role holds what the route asks — `service.control` to start, and
 * `destructive` as well to stop or restart, since both disconnect everything
 * on it. `run` carries one through and reports how it ended; it is the
 * caller's to confirm first where the action interrupts.
 */
export function usePower() {
  const { id, conn, summary, status } = useDatabase()
  const { can } = useAuth()
  const change = usePowerChange(id)
  const power = summary?.power
  const mayStart = can("service.control")
  const mayInterrupt = mayStart && can("destructive")
  const check = status.refresh
  const name = conn.name

  const run = useCallback(
    async (action: PowerAction) => {
      try {
        const answer = await changePower(id, action, {
          request: () => post<PowerResponse>(`/databases/${id}/power`, { action }),
          read: async () => (await get<DbConnectionSummary>(`/databases/${id}`)).state,
        })
        notify.success(`${name} ${POWER[action].done}`, {
          description: `${answer.via === "docker" ? "Container" : "Unit"} ${answer.target}${answer.state ? ` is ${answer.state}` : ""}.`,
        })
      } catch (err) {
        notify.error(`Could not ${action} ${name}`, err)
      } finally {
        // Whatever happened, the page's own reading of the server is now old.
        check()
      }
    },
    [id, name, check],
  )

  return useMemo(
    () => ({
      change,
      can: {
        start: Boolean(power?.start) && mayStart,
        stop: Boolean(power?.stop) && mayInterrupt,
        restart: Boolean(power?.restart) && mayInterrupt,
      },
      run,
    }),
    [change, power?.start, power?.stop, power?.restart, mayStart, mayInterrupt, run],
  )
}

/**
 * What the server behind the connection can be asked to do — start, stop,
 * restart — and the one thing the connection itself can be made: protected.
 * They are the verbs the database's menu draws on every page
 * (`shell/database-verbs.tsx`), declared here because the home is where the
 * server's state is read.
 *
 * A verb is in the list only when it can be used: the summary offers the
 * action for the state the server is in, and the role may ask for it. While
 * a change is in flight the verb says so in its own place — "Stopping…" —
 * and none of the three can be pressed.
 *
 * Stop and Restart interrupt whatever is connected, so they are confirmed
 * with the server named. The confirmation only begins the change: it can
 * take minutes, and a dialog held open that long is a page the reader cannot
 * use, so the dialog closes and the database's own surfaces show the change.
 */
export const useLifecycleVerbs: DatabaseVerbSource = ({ confirm }: DatabaseVerbTools) => {
  const { id, conn, summary, engine, readOnly, status } = useDatabase()
  const { refresh } = useDatabases()
  const { can } = useAuth()
  const power = usePower()
  const admin = can("system.admin")
  const check = status.refresh
  const name = conn.name

  const protect = useCallback(
    async (on: boolean) => {
      try {
        await put<DbConnection>(`/databases/${id}`, { readOnly: on })
        notify.success(on ? `${name} is protected` : `${name} is no longer protected`, {
          description: on
            ? "The dashboard now refuses every change to its data and its schema, for every role."
            : "Its data and its schema can be changed from the dashboard again.",
        })
        refresh()
        check()
      } catch (err) {
        notify.error(on ? `Could not protect ${name}` : `Could not stop protecting ${name}`, err)
      }
    },
    [id, name, refresh, check],
  )

  return useMemo(() => {
    const verbs: Verb[] = []
    const busy = Boolean(power.change)
    const word = (action: PowerAction) =>
      power.change?.action === action ? POWER[action].progressive : POWER[action].label

    if (power.can.start) {
      verbs.push({
        key: "start",
        label: word("start"),
        icon: Play,
        group: "Server",
        progressive: POWER.start.progressive,
        disabled: busy,
        run: () => void power.run("start"),
      })
    }
    for (const action of ["restart", "stop"] as const) {
      if (!power.can[action] || !summary) continue
      verbs.push({
        key: action,
        label: word(action),
        icon: action === "stop" ? StopCircle : RotateClockwise,
        group: "Server",
        progressive: POWER[action].progressive,
        disabled: busy,
        run: () =>
          confirm({
            ...powerConfirmation(action, engine, summary),
            action: async () => {
              void power.run(action)
              return "reported"
            },
          }),
      })
    }
    if (admin) {
      verbs.push(
        readOnly
          ? {
              key: "unprotect",
              label: "Stop protecting",
              icon: LockOpen,
              run: () => void protect(false),
            }
          : { key: "protect", label: "Protect", icon: LockClosed, run: () => void protect(true) },
      )
    }
    return verbs
  }, [power, summary, engine, readOnly, admin, confirm, protect])
}
