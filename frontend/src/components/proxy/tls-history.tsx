"use client"

import { useState } from "react"
import { ClockRewind, Eye, Trash } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { targetLabel, type ScanTarget } from "@/lib/scan-target"
import type { TLSScan } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Sparkline } from "@/components/metrics/sparkline"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { VerbMenu } from "@/components/verbs"
import { Button } from "@/components/ui/button"

/** One stored report, as the history lists it. */
export type TLSScanSummary = {
  id: number
  grade: string
  /** Absent where the scan read no certificate. */
  daysLeft?: number
  fingerprint?: string
  reachable: boolean
  checkedAt: string
}

/** How a report differs from the one before it, as each said it. */
export type ScanChange = {
  area: "reachability" | "grade" | "certificate" | "protocols" | "headers"
  title: string
  before: string
  after: string
}

export type StoredTLSScan = {
  id: number
  report: TLSScan
  /** Absent for a target's first report. */
  previousId?: number
  changes: ScanChange[]
}

export type ScanHistory = ReturnType<typeof useScanHistory>

/**
 * The target's stored reports. Every finished scan is kept on the server, so
 * the report opens on the last one at once — for an account that may not scan
 * too — and a new scan is read against the one before it.
 *
 * `live` is the checked time of the scan on screen, so the list is read again
 * once a new scan has been stored.
 */
export function useScanHistory(target: ScanTarget | undefined, live: string | undefined) {
  const key = target ? targetLabel(target) : ""
  const summaries = usePoll(
    (signal) =>
      get<TLSScanSummary[]>(
        "/certificates/reports",
        { domain: target?.host, port: target?.port },
        signal,
      ),
    0,
    [key, live],
    { enabled: target !== undefined },
  )
  // A past report is picked for this target only; another target starts on
  // its own latest.
  const [picked, setPicked] = useState<{ key: string; id: number }>()
  const viewingId = picked?.key === key ? picked.id : undefined
  const latestId = summaries.data?.[0]?.id
  const shownId = viewingId ?? latestId
  const shown = usePoll(
    (signal) => get<StoredTLSScan>(`/certificates/reports/${shownId}`, undefined, signal),
    0,
    [shownId],
    { enabled: shownId !== undefined },
  )
  const stored = shown.data?.id === shownId ? shown.data : undefined
  return {
    key,
    summaries: summaries.data,
    refresh: summaries.refresh,
    /** The newest stored report, when it is the one fetched. */
    latest: viewingId === undefined ? stored : undefined,
    /** A past report the reader picked, once it has arrived. */
    viewing: viewingId !== undefined ? stored : undefined,
    viewingId,
    /** The report whose changes the panel lists: the picked one or the newest. */
    shown: stored,
    view: (id: number | undefined) =>
      setPicked(id === undefined || id === latestId ? undefined : { key, id }),
  }
}

/**
 * The history beside a report: how the grade and the certificate's days left
 * moved, what changed since the scan before, and every stored scan to open.
 */
export function ScanHistoryPanel({
  history,
  target,
  admin,
}: {
  history: ScanHistory
  target: ScanTarget
  admin: boolean
}) {
  const { confirm, dialog } = useConfirm()
  const summaries = history.summaries ?? []
  // Oldest first, as a line reads left to right.
  const oldestFirst = summaries.toReversed()
  const days = oldestFirst.flatMap((scan) => (scan.daysLeft === undefined ? [] : [scan.daysLeft]))
  const grades = oldestFirst.map((scan) => gradeRank(scan.grade))
  const label = targetLabel(target)

  const forget = () =>
    confirm({
      title: "Forget scan history",
      description: `Every stored report for ${label} is deleted. The next scan starts a new history.`,
      confirmLabel: "Forget history",
      action: async () => {
        await del("/certificates/reports", { query: { domain: target.host, port: target.port } })
        history.view(undefined)
        history.refresh()
        notify.success(`Forgot the scan history of ${label}`)
      },
    })

  return (
    <Panel plain>
      <PanelHeader
        title="History"
        actions={
          <div className="flex items-center gap-2">
            {admin && <WatchButton target={target} />}
            {admin && summaries.length > 0 && (
              <VerbMenu
                label={`More actions for the history of ${label}`}
                verbs={[
                  {
                    key: "forget",
                    label: "Forget history",
                    icon: Trash,
                    danger: true,
                    run: forget,
                  },
                ]}
              />
            )}
          </div>
        }
      />
      <PanelBody className="space-y-5">
        {history.viewingId !== undefined && (
          <Notice icon={ClockRewind} title="A past scan">
            <p>
              The report above is the scan from{" "}
              {history.viewing ? timestamp(history.viewing.report.checkedAt) : "…"}, not the latest.
            </p>
            <div className="pt-2">
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => history.view(undefined)}
              >
                Back to the latest
              </Button>
            </div>
          </Notice>
        )}
        {summaries.length === 0 ? (
          <EmptyNote>
            {admin
              ? "Each scan of this target is kept here, so the next one can say what changed."
              : "No scan of this target is stored yet."}
          </EmptyNote>
        ) : (
          <>
            {summaries.length > 1 && (
              <div className="grid grid-cols-2 gap-4">
                <Trend label="Grade" values={grades} max={GRADES.length - 1} />
                <Trend label="Days left" values={days} />
              </div>
            )}
            <Changes stored={history.shown} />
            <RowList aria-label="Stored scans">
              {summaries.map((scan) => {
                const open = scan.id === (history.viewingId ?? summaries[0]?.id)
                return (
                  <Row
                    key={scan.id}
                    onClick={() => history.view(scan.id)}
                    leading={
                      open ? (
                        <Eye aria-label="On screen" className="size-3.5 text-foreground" />
                      ) : (
                        <span aria-hidden className="size-3.5" />
                      )
                    }
                    title={timestamp(scan.checkedAt)}
                    subtitle={
                      scan.reachable
                        ? scan.daysLeft === undefined
                          ? "no certificate"
                          : `${scan.daysLeft} days left`
                        : "no handshake"
                    }
                    trailing={<Tag mono>{scan.grade || "—"}</Tag>}
                  />
                )
              })}
            </RowList>
          </>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

function Trend({ label, values, max }: { label: string; values: number[]; max?: number }) {
  return (
    <div className="min-w-0">
      <p className="eyebrow text-muted-foreground">{label}</p>
      <Sparkline values={values} max={max} width={160} height={28} label={`${label} over time`} />
    </div>
  )
}

function Changes({ stored }: { stored: StoredTLSScan | undefined }) {
  if (!stored) return null
  if (stored.previousId === undefined) {
    return (
      <EmptyNote>The first stored scan of this target; the next one is compared with it.</EmptyNote>
    )
  }
  return (
    <div className="min-w-0">
      <p className="eyebrow pb-1 text-muted-foreground">
        Changes since the scan before ({relativeTime(stored.report.checkedAt)})
      </p>
      {stored.changes.length === 0 ? (
        <EmptyNote>Nothing changed.</EmptyNote>
      ) : (
        <RowList aria-label="Changes since the scan before">
          {stored.changes.map((change) => (
            <Row
              key={`${change.area}:${change.title}`}
              title={change.title}
              subtitle={`${change.before || "—"} → ${change.after || "—"}`}
              trailing={<Tag>{change.area}</Tag>}
            />
          ))}
        </RowList>
      )}
    </div>
  )
}

/**
 * Adds the report's target to the watch list, which the server then checks on
 * its own schedule. Watching a target already watched changes nothing, so the
 * list is not read first: the page would fetch it on every visit for a button
 * most visits never press.
 */
function WatchButton({ target }: { target: ScanTarget }) {
  const key = targetLabel(target)
  const [watched, setWatched] = useState<string>()
  const [adding, setAdding] = useState(false)
  const watch = async () => {
    setAdding(true)
    try {
      await post("/certificates/watched", { domain: target.host, port: target.port })
      setWatched(key)
      notify.success(`${key} is on the watch list`)
    } catch (err) {
      notify.error(`Could not watch ${key}`, err)
    } finally {
      setAdding(false)
    }
  }
  return (
    <Button
      type="button"
      variant="outline"
      size="xs"
      onClick={() => void watch()}
      disabled={watched === key || adding}
      pending={adding}
    >
      {watched === key ? "Watched" : "Watch"}
    </Button>
  )
}

// Worst to best, so a higher point on the line is a better grade.
const GRADES = ["F", "E", "D", "C", "B", "A-", "A", "A+"]

function gradeRank(grade: string) {
  return Math.max(0, GRADES.indexOf(grade))
}
