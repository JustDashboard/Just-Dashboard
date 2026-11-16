"use client"

import { useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Clock, Stopwatch } from "@/components/icons"
import { countdown, type ScheduleLane } from "@/lib/schedule"
import { plural } from "@/lib/format"
import { useNow } from "@/hooks/use-now"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Rail, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
import { laneProduct } from "@/components/procs/schedule-band"
import { laneHref } from "@/components/procs/schedule-links"

/**
 * Everything that runs on a clock, folded into a column beside the schedule
 * it opened (D9): the account's cron jobs, the timers and the package files'
 * lines, soonest first, as the Scheduled page's band orders them. Each row is
 * the schedule as the program it runs, its name, what it is and when it fires
 * next; the one on screen is filled, and its view goes with the next one.
 */
export function ScheduleRail({
  lanes,
  user,
  current,
  loading,
  error,
  onRetry,
}: {
  lanes: ScheduleLane[]
  /** Whose crontab the cron rows are. */
  user: string
  /** The lane key of the schedule on screen. */
  current: string | null
  loading: boolean
  error?: Error
  onRetry?: () => void
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  const [query, setQuery] = useState("")
  const now = useNow(1000)
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return lanes
    return lanes.filter((lane) =>
      [lane.name, lane.runs, lane.when].some((text) => text.toLowerCase().includes(q)),
    )
  }, [lanes, query])
  const caption =
    loading && lanes.length === 0
      ? "Reading the schedules…"
      : query
        ? `${rows.length} of ${lanes.length}`
        : `${plural(lanes.length, "schedule")} · soonest first`
  return (
    <Rail
      slot="schedule-rail"
      label="Schedules"
      search={{ placeholder: "Name, command or schedule", query, onQuery: setQuery }}
      rows={rows}
      keyOf={(lane) => lane.key}
      current={(lane) => lane.key === current}
      row={(lane, on) => (
        <ScheduleRow lane={lane} current={on} now={now} href={laneHref(lane, user, tab)} />
      )}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="No schedule matches."
      caption={caption}
    />
  )
}

function ScheduleRow({
  lane,
  current,
  now,
  href,
}: {
  lane: ScheduleLane
  current: boolean
  now: number
  href: string
}) {
  const quiet = lane.next === undefined
  const line = lane.skipped
    ? "left to systemd"
    : lane.job?.disabled
      ? "switched off"
      : lane.timer && lane.timer.activeState !== "active"
        ? "stopped"
        : lane.when
  return (
    <RailRow
      item={lane.key}
      name={lane.name}
      title={lane.runs}
      current={current}
      href={href}
      quiet={quiet}
      mark={
        <ProductLogo
          id={laneProduct(lane)}
          size="sm"
          fallback={lane.kind === "timer" ? Stopwatch : Clock}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={<StatusDot className="size-1.5" tone={quiet ? "stopped" : "running"} />}
      end={
        lane.next !== undefined && (
          <RailFigure>in {countdown(Math.max(0, lane.next - now))}</RailFigure>
        )
      }
    >
      <RailName current={current}>{lane.name}</RailName>
      <RailLine>{line}</RailLine>
    </RailRow>
  )
}
