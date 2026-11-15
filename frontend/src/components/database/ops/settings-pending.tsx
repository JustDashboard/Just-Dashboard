"use client"

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react"
import { RotateClockwise } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { PendingStrip, type PendingItem } from "@/components/deploy/settings/pending-strip"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { POWER } from "@/components/database/home/power"
import { powerConfirmation } from "@/components/database/home/power-subject"
import { read } from "@/components/database/home/read"
import { usePower } from "@/components/database/home/verbs"
import { isDown } from "@/components/database/ops/performance-parts"
import {
  pendingHeadline,
  pendingItems,
  rememberWaiting,
  rememberedWaiting,
  type PendingReport,
  type PendingWord,
} from "@/components/database/ops/settings-pending-model"
import type { DbSettings } from "@/components/database/ops/settings-parameters-model"
import { useDatabase } from "@/components/database/shell/database-context"
import { settingsPageHref, type SettingsPageKey } from "@/components/database/shell/settings-pages"

type Reports = Record<string, PendingReport>

const PendingContext = createContext<{
  report: (key: string, report: PendingReport | undefined) => void
} | null>(null)

/** What a report says, without the function it carries: two that say the same are one. */
const said = (report: PendingReport) =>
  JSON.stringify([
    report.waiting ?? [],
    report.edits ?? [],
    report.covers ?? [],
    report.ready ?? true,
  ])

/**
 * Puts what a section holds on the strip at the head of its page: what it has
 * stored on the server that waits for a restart, and what its form holds
 * unsaved. `refresh` is called once a restart has ended, since what was
 * waiting is then in force.
 */
export function usePendingReport(key: string, report: PendingReport) {
  const context = useContext(PendingContext)
  const send = context?.report
  const latest = useRef(report)
  useEffect(() => {
    latest.current = report
  })
  const signature = said(report)
  useEffect(() => {
    if (!send) return
    send(key, { ...latest.current, refresh: () => latest.current.refresh?.() })
    // The signature is the report's whole content: a report that says the
    // same thing again is not sent again.
  }, [send, key, signature])
  useEffect(() => {
    if (!send) return
    return () => send(key, undefined)
  }, [send, key])
}

/**
 * What a database's settings hold that is not in force yet, as the strip at
 * the head of every settings page (`deploy/settings/pending-strip.tsx`, which
 * a project's settings draw for what is saved and not live).
 *
 * A database has one such state: a value the server has stored and reads only
 * when it starts: a parameter, or a library it loads at start. The Parameters
 * page said so in its head and again in a notice, the Extensions page in a
 * notice of its own, and no other page said it at all, so a reader on General
 * had no way to learn that the server was waiting on them. The strip says it
 * on every page, names each value as a way to where it is changed, and
 * carries the one act that puts them in force: Restart, confirmed with the
 * server named, for the role that may interrupt it.
 *
 * It arrives for what is stored, never for what is typed. Unsaved edits are
 * named on it while it is drawn, beside what they will join; on a page with
 * nothing waiting they are the save bar's and the section heads' alone,
 * because a strip that arrived with the first keystroke would push the field
 * being typed in down a line (the reason `settingStatus` sits at a head's far
 * end).
 *
 * The page a parameter is changed on reads the whole list anyway, so it
 * reports what waits (`usePendingReport`) and this asks for nothing there.
 *
 * What a page learns is remembered for the next one, so the strip is in its
 * place when the next settings page is first painted and that page's read
 * landing moves nothing.
 */
export function SettingsPending({
  page,
  confirm,
  children,
}: {
  page: SettingsPageKey
  confirm: (request: ConfirmRequest) => void
  children: React.ReactNode
}) {
  const { id, conn, engine, summary, status, href } = useDatabase()
  const { can } = useAuth()
  const power = usePower()
  const [reports, setReports] = useState<Reports>({})
  const report = useCallback((key: string, next: PendingReport | undefined) => {
    setReports((held) => {
      if (!next) {
        if (!(key in held)) return held
        const rest = { ...held }
        delete rest[key]
        return rest
      }
      return { ...held, [key]: next }
    })
  }, [])
  const value = useMemo(() => ({ report }), [report])

  // A SQL engine marks the parameters it has stored and not yet read. A
  // key–value server sets its configuration at once, a document database
  // lists counters and a file has no server to restart, so none of them is asked.
  const asks =
    page !== "parameters" &&
    engine.kind === "sql" &&
    engine.can("settings") &&
    !engine.can("fileBased") &&
    !isDown(status.state)
  const list = usePoll(
    (signal) =>
      read<DbSettings>(
        `/databases/${id}/settings`,
        (answer) => Array.isArray(answer.settings),
        { all: 1 },
        signal,
      ),
    0,
    [id],
    { enabled: asks },
  )
  const parameters = settingsPageHref(href("settings"), { path: "parameters" })
  const stored: PendingWord[] = useMemo(
    () =>
      page === "parameters"
        ? []
        : (list.data?.settings ?? [])
            .filter((setting) => setting.pendingRestart)
            .map((setting) => ({
              name: setting.name,
              mono: true,
              state: "restart",
              href: `${parameters}?q=${encodeURIComponent(setting.name)}`,
            })),
    [page, list.data, parameters],
  )

  // Once a restart has ended, what waited is in force: every reading of it is old.
  const flight = power.change
  const wasFlying = useRef(false)
  const refreshList = list.refresh
  const refreshes = useRef<(() => void)[]>([])
  useEffect(() => {
    refreshes.current = Object.values(reports).flatMap((one) => (one.refresh ? [one.refresh] : []))
  })
  const reread = useCallback(() => {
    refreshList()
    for (const refresh of refreshes.current) refresh()
  }, [refreshList])
  // A restart begun elsewhere (the database's menu, another tab) ends without
  // this page having asked for it: what the server holds is read again then.
  useEffect(() => {
    if (flight) {
      wasFlying.current = true
      return
    }
    if (!wasFlying.current) return
    wasFlying.current = false
    // The engine accepts connections a moment after its container is up.
    const timer = window.setTimeout(reread, 1500)
    return () => window.clearTimeout(timer)
  }, [flight, reread])

  const found = pendingItems(stored, Object.values(reports))
  // Until this page's own reads have answered, what the last page knew stands.
  const settled =
    (!asks || list.data !== undefined || list.error !== undefined) &&
    Object.values(reports).every((one) => one.ready !== false)
  const waiting = settled ? found.waiting : (rememberedWaiting(id) ?? found.waiting)
  const { edits } = found
  const names = waiting.map((item) => `${item.name}:${item.state}`).join(",")
  useEffect(() => {
    if (settled) rememberWaiting(id, waiting)
    // `names` is what `waiting` says: the list itself is new on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, settled, names])
  const drawn = waiting.length > 0
  const mayRestart = power.can.restart && Boolean(summary)
  const restart = () => {
    if (!summary) return
    confirm({
      ...powerConfirmation("restart", engine, summary),
      action: async () => {
        // The restart can take minutes: the dialog begins it and the strip says it is under way.
        void power.run("restart").then(reread)
        return "reported"
      },
    })
  }
  const items: PendingItem[] = [...edits.map((item) => ({ ...item, current: true })), ...waiting]

  return (
    <PendingContext.Provider value={value}>
      {drawn && (
        <div
          data-slot="settings-pending"
          className="flex min-w-0 animate-rise flex-wrap items-center gap-x-4 gap-y-2 border-b border-hairline pb-4"
        >
          <PendingStrip
            label="Waiting for a restart"
            fold="What is waiting"
            className="min-w-0 flex-1 border-b-0 pb-0"
            headline={
              flight ? (
                <Status
                  tone="running"
                  label={<TextShimmer>{POWER[flight.action].progressive}</TextShimmer>}
                />
              ) : (
                <Status tone="warning" label={pendingHeadline(waiting.length, edits.length)} />
              )
            }
            detail={
              !mayRestart && !flight
                ? can("service.control") && can("destructive")
                  ? (summary?.power.reason ?? `Nothing here can restart ${conn.name}.`)
                  : "Restarting it is for a role that may interrupt it."
                : undefined
            }
            items={items}
          />
          {mayRestart && (
            <Button size="sm" variant="outline" disabled={Boolean(flight)} onClick={restart}>
              <RotateClockwise />
              Restart
            </Button>
          )}
        </div>
      )}
      {children}
    </PendingContext.Provider>
  )
}
