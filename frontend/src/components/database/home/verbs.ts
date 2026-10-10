"use client"

import { useCallback, useEffect, useMemo, useRef, useSyncExternalStore } from "react"
import { useRouter } from "next/navigation"
import { LockClosed, LockOpen, Play, RotateClockwise, StopCircle } from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { Verb } from "@/components/verbs"
import {
  POWER,
  changePower,
  endPower,
  powerCarried,
  powerChange,
  powerEnded,
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
 * home's Start, the menu on another of the database's pages, this tab before
 * it was reloaded, or another tab of the dashboard (`power.ts` keeps it in
 * the tab's session and tells the other tabs). A surface that draws the
 * database reads it to say "Stopping…" and to run its beam.
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
  const { id, conn, summary, engine, status, href } = useDatabase()
  const { can } = useAuth()
  const router = useRouter()
  const change = usePowerChange(id)
  const power = summary?.power
  const logs = engine.has("logs") ? href("logs") : undefined
  const mayStart = can("service.control")
  const mayInterrupt = mayStart && can("destructive")
  const check = status.refresh
  const name = conn.name

  const run = useCallback(
    async (action: PowerAction) => {
      try {
        const { answer, settled } = await changePower(id, action, {
          request: () => post<PowerResponse>(`/databases/${id}/power`, { action }),
          read: async () => (await get<DbConnectionSummary>(`/databases/${id}`)).state,
        })
        const target = `${answer.via === "docker" ? "Container" : "Unit"} ${answer.target}`
        if (settled) {
          notify.success(`${name} ${POWER[action].done}`, {
            description: `${target}${answer.state ? ` is ${answer.state}` : ""}.`,
          })
        } else if (action === "stop") {
          notify.warning(`${name} has not stopped yet`, {
            description: `${target} was asked to stop and the server still answers.`,
          })
        } else {
          // The request succeeded and the engine is still not there: a
          // container that starts and an engine that crashes inside it. Its
          // log is where the reason is.
          notify.warning(`${name} is not answering yet`, {
            description: `${target} was ${POWER[action].done}, but the engine is not accepting connections.`,
            duration: 12_000,
            action: logs ? { label: "Logs", onClick: () => router.push(logs) } : undefined,
          })
        }
      } catch (err) {
        notify.error(`Could not ${action} ${name}`, err)
      } finally {
        // Whatever happened, the page's own reading of the server is now old.
        check()
      }
    },
    [id, name, check, logs, router],
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

/** How often the server is read while it is being changed. Its own poll is every thirty seconds. */
const WATCH_EVERY_MS = 3_000

/**
 * Keeps the page's reading of the server fresh for as long as a change is in
 * flight, and ends a change nobody here is waiting on.
 *
 * The summary is polled twice a minute, which is right for a server at rest
 * and wrong for one being stopped: "Runs as" went on saying what Docker said
 * before the change, under a notice saying "Starting…". And a change this tab
 * did not send — the page was reloaded mid-stop, or another tab began it —
 * has no request to end it, so it ends here, on what the server reads.
 *
 * Mounted once per page, by the database's menu.
 */
function usePowerWatch() {
  const { id, status } = useDatabase()
  const change = usePowerChange(id)
  const check = status.refresh
  const state = status.state
  const seenDown = useRef(false)
  const held = useRef(false)

  useEffect(() => {
    if (!change) {
      seenDown.current = false
      // A change that has just ended, wherever it was ended from — another
      // tab's request among them: what the server is now is the next thing
      // to read, not the last thing read while it was changing.
      if (held.current) check()
      held.current = false
      return
    }
    held.current = true
    if (state !== "running" && state !== "checking" && state !== "unknown") seenDown.current = true
    const settle = () => {
      if (!powerCarried(id) && powerEnded(change, state, seenDown.current, Date.now())) endPower(id)
    }
    settle()
    const timer = setInterval(() => {
      check()
      settle()
    }, WATCH_EVERY_MS)
    return () => clearInterval(timer)
  }, [id, change, check, state])
}

/**
 * Hands the keyboard back to the menu a verb was chosen from once the
 * confirmation it opened has closed.
 *
 * The dialog is opened by a menu item that is gone by the time it closes, so
 * the dialog has nowhere to return focus to and leaves it on the page's body:
 * cancel a Stop and the next Tab starts again from the top of the window.
 * The menu's trigger is still there, and is where the reader was.
 */
function returnFocusToMenu() {
  const menu = document.activeElement?.closest('[role="menu"]')
  const trigger = document.getElementById(menu?.getAttribute("aria-labelledby") ?? "")
  if (!trigger) return
  let opened = false
  const observer = new MutationObserver(() => {
    if (document.querySelector('[role="dialog"]')) {
      opened = true
      return
    }
    if (!opened) return
    observer.disconnect()
    // After the dialog's own teardown, which leaves focus where it fell.
    setTimeout(() => {
      if (document.activeElement === document.body && trigger.isConnected) trigger.focus()
    })
  })
  observer.observe(document.body, { childList: true })
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
  usePowerWatch()
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
        run: () => {
          returnFocusToMenu()
          confirm({
            ...powerConfirmation(action, engine, summary),
            action: async () => {
              void power.run(action)
              return "reported"
            },
          })
        },
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
