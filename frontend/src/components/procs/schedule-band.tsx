"use client"

import { useMemo, useState } from "react"
import { Clock, Stopwatch } from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural } from "@/lib/format"
import {
  clockLabel,
  countdown,
  dayHours,
  longClock,
  type ScheduleKind,
  type ScheduleLane,
} from "@/lib/schedule"
import { useSessionState } from "@/lib/view-state"
import { useArrivals } from "@/hooks/use-arrivals"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, programProduct, unitProduct } from "@/components/product-logo"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"

const HOUR = 3600_000

/** The window the band draws, from now. */
export const BAND_WINDOW = 24 * HOUR

/** How many schedules "Up next" lists before "Show all". */
const SHOWN = 6

/** How many schedules an hour's column stacks before the rest are a count. */
const STACK = 6

/**
 * Each place a schedule is kept, in the hue that names it (§15: colour that
 * names a kind is `--tag-*`). None is red or amber, so a run never reads as a
 * run that failed. `label` heads a chip that counts them; `one` names a
 * single schedule of the kind in a row.
 */
export const KIND: Record<ScheduleKind, { label: string; one: string; color: string }> = {
  cron: { label: "Cron jobs", one: "Cron job", color: "var(--tag-blue)" },
  timer: { label: "Timers", one: "Timer", color: "var(--tag-violet)" },
  system: { label: "System cron", one: "System cron", color: "var(--tag-cyan)" },
}

export function laneProduct(lane: ScheduleLane) {
  return lane.kind === "timer" ? unitProduct(lane.product) : programProduct(lane.product)
}

/** The lane's first run still ahead of `now`. */
function upcoming(lane: ScheduleLane, now: number) {
  return lane.times.find((t) => t >= now) ?? (lane.next && lane.next >= now ? lane.next : undefined)
}

type Column = {
  at: number
  hour: number
  day: string
  entries: { lane: ScheduleLane; runs: number[] }[]
}

/**
 * The next day of everything on the host's clock, said two ways that agree:
 * a chart of the next 24 hours, an hour to a column, and under it the
 * schedules in the order they run.
 *
 * It replaced a lane per schedule on one long axis, which the operator could
 * not read — "is it a calendar? a timeline?" — because nothing on it said
 * what a dot, a tick or a band was, most lanes were one dot near the left
 * edge of an empty track (systemd only reports a timer's next run), and the
 * times were on an axis three lanes up. Now each hour is a column with its
 * time under it, each schedule that runs in that hour is one block in its
 * kind's hue, stacked — so a busy night is a tall column, and a schedule that
 * runs every ten minutes is one block in every hour rather than 144 ticks —
 * and the list under it prints each schedule's clock time, its schedule in
 * words and a countdown. Pointing at a block, an hour or a row says what it
 * is on the line under the chart, and lights that schedule's blocks across
 * the day.
 *
 * Times are the host's, since that is the clock its schedules are written
 * in (the page says which when it is not the reader's). A timer's later runs
 * are read off its calendar only where it has no random delay
 * (`timerRuns`); a package's cron line that steps aside for systemd draws
 * nothing and is counted under the list.
 */
export function ScheduleBand({
  lanes,
  zone,
  settled,
  onOpen,
}: {
  lanes: ScheduleLane[]
  /** The host's zone; undefined reads this browser's. */
  zone?: string
  /** Whether the schedules have been read; the band holds its shape until they are. */
  settled: boolean
  onOpen: (lane: ScheduleLane) => void
}) {
  const now = useNow(1000)
  const minute = Math.floor(now / 60_000) * 60_000
  const [kind, setKind] = useSessionState<ScheduleKind | "">("processes.scheduled.kind", "")
  const [all, setAll] = useState(false)
  const [pointed, setPointed] = useState<string | null>(null)
  const [hour, setHour] = useState<number | null>(null)

  const inWindow = useMemo(() => lanes.filter((lane) => lane.times.length > 0), [lanes])
  const counts = useMemo(() => {
    const out: Record<ScheduleKind, number> = { cron: 0, timer: 0, system: 0 }
    for (const lane of inWindow) out[lane.kind]++
    return out
  }, [inWindow])
  const listed = useMemo(
    () => inWindow.filter((lane) => !kind || lane.kind === kind),
    [inWindow, kind],
  )
  const ordered = useMemo(
    () =>
      [...listed].sort(
        (a, b) => (upcoming(a, minute) ?? Infinity) - (upcoming(b, minute) ?? Infinity),
      ),
    [listed, minute],
  )
  const shown = all ? ordered : ordered.slice(0, SHOWN)
  // A schedule that turned up since the last read rises into its place; a
  // narrowed list is a new question, not arrivals, so this keys on all of them.
  const arrived = useArrivals(inWindow.map((lane) => lane.key))
  const soonest = ordered[0]?.key

  const hours = useMemo(() => dayHours(minute, zone), [minute, zone])
  const columns = useMemo<Column[]>(
    () =>
      hours.map((slot) => {
        const end = slot.at + HOUR
        const entries = listed
          .map((lane) => ({
            lane,
            runs: lane.times.filter((t) => t >= minute && t >= slot.at && t < end),
          }))
          .filter((entry) => entry.runs.length > 0)
          // The schedules that run every hour stack at the foot of every
          // column, in the same order, so the day's floor is level and what
          // runs once stands on top of it.
          .sort(
            (a, b) =>
              b.lane.times.length - a.lane.times.length || a.lane.key.localeCompare(b.lane.key),
          )
        return { ...slot, entries }
      }),
    [hours, listed, minute],
  )

  // What the chart does not draw, said under the list so a quiet day is not
  // read as a quiet host: weekly and monthly work past the window, what runs
  // only at boot, what is switched off, and the package lines systemd runs.
  const rest = lanes.filter((lane) => (!kind || lane.kind === kind) && lane.times.length === 0)
  const later = rest.filter((lane) => lane.next !== undefined).length
  const atBoot = rest.filter((lane) => lane.job?.schedule.toLowerCase() === "@reboot").length
  const off = rest.filter(
    (lane) => lane.job?.disabled || (lane.timer && lane.timer.activeState !== "active"),
  ).length
  const skipped = rest.filter((lane) => lane.skipped).length

  return (
    <Panel plain aria-label="Next 24 hours">
      <PanelHeader title="Next 24 hours">
        <ChipStrip aria-label="Kind" className="mr-auto">
          {(Object.keys(KIND) as ScheduleKind[]).map((key) =>
            counts[key] === 0 && kind !== key ? null : (
              <FilterChip
                key={key}
                selected={kind === key}
                onClick={() => setKind(kind === key ? "" : key)}
              >
                <span
                  aria-hidden
                  className="size-2 rounded-[3px]"
                  style={{ background: KIND[key].color }}
                />
                {KIND[key].label}
                <ChipCount>{counts[key]}</ChipCount>
              </FilterChip>
            ),
          )}
        </ChipStrip>
      </PanelHeader>
      <PanelBody className="space-y-6 pt-4">
        <DayChart
          columns={columns}
          now={now}
          zone={zone}
          settled={settled}
          pointed={pointed}
          hour={hour}
          onPoint={setPointed}
          onHour={setHour}
          onOpen={onOpen}
          lanes={listed}
          narrowed={kind}
        />

        <section aria-label="Up next" className="min-w-0">
          <div className="mb-1 flex items-baseline justify-between gap-3">
            <h3 className="eyebrow">Up next</h3>
            {settled && listed.length > 0 && (
              <span className="text-hint text-muted-foreground">in the order they run</span>
            )}
          </div>
          {!settled ? (
            <div aria-hidden className="divide-y divide-hairline border-t border-hairline">
              {Array.from({ length: SHOWN }, (_, i) => (
                <div key={i} className="flex h-12 items-center gap-3">
                  <span className="h-3 w-10 animate-pulse rounded-sm bg-muted/60" />
                  <span className="size-7 animate-pulse rounded-lg bg-muted/60" />
                  <span className="h-3 w-48 animate-pulse rounded-sm bg-muted/60" />
                </div>
              ))}
            </div>
          ) : listed.length === 0 ? (
            <p className="border-t border-hairline py-6 text-body text-muted-foreground">
              Nothing {kind ? `in ${KIND[kind].label.toLowerCase()} ` : ""}runs in the next 24
              hours.
            </p>
          ) : (
            <ol
              aria-label="Schedules"
              className="border-t border-hairline"
              onMouseLeave={() => setPointed(null)}
            >
              {shown.map((lane) => (
                <UpNextRow
                  key={lane.key}
                  lane={lane}
                  now={now}
                  zone={zone}
                  arrived={arrived.has(lane.key)}
                  soonest={soonest === lane.key}
                  pointed={pointed === lane.key}
                  onPoint={() => setPointed(lane.key)}
                  onOpen={() => onOpen(lane)}
                />
              ))}
            </ol>
          )}
          {settled && (listed.length > SHOWN || later + atBoot + off + skipped > 0) && (
            <div className="flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 border-t border-hairline pt-2 text-hint text-muted-foreground">
              {listed.length > SHOWN && (
                <>
                  <Button
                    variant="ghost"
                    size="xs"
                    className="-ml-2 text-foreground"
                    onClick={() => setAll(!all)}
                  >
                    {all ? `Show the next ${SHOWN}` : `Show all ${listed.length}`}
                  </Button>
                  {later + atBoot + off + skipped > 0 && <FactDot />}
                </>
              )}
              {[
                later > 0 && { text: `${later} run later in the week or month` },
                atBoot > 0 && { text: `${atBoot} only at boot` },
                off > 0 && { text: `${off} switched off` },
                skipped > 0 && {
                  text: `${plural(skipped, "cron line")} left to systemd`,
                  title:
                    "Package cron lines that step aside on a host running systemd, because a timer does the same work",
                },
              ]
                .filter((item): item is { text: string; title?: string } => Boolean(item))
                .map((item, i) => (
                  <span key={i} className="inline-flex items-center gap-2" title={item.title}>
                    {i > 0 && <FactDot />}
                    {item.text}
                  </span>
                ))}
            </div>
          )}
        </section>
      </PanelBody>
    </Panel>
  )
}

/** "03:00", or the day's name at midnight, under an hour's column. */
function hourLabel(column: Column, index: number) {
  if (index === 0) return "Now"
  if (column.hour === 0) return column.day
  return `${String(column.hour).padStart(2, "0")}:00`
}

/**
 * The next 24 hours, an hour to a column, a block per schedule that runs in
 * it. One fixed-height line under the columns reads out what is pointed at —
 * a block, an hour — and at rest says the day in a sentence.
 */
function DayChart({
  columns,
  now,
  zone,
  settled,
  pointed,
  hour,
  onPoint,
  onHour,
  onOpen,
  lanes,
  narrowed,
}: {
  columns: Column[]
  now: number
  zone?: string
  settled: boolean
  pointed: string | null
  hour: number | null
  onPoint: (key: string | null) => void
  onHour: (index: number | null) => void
  onOpen: (lane: ScheduleLane) => void
  lanes: ScheduleLane[]
  narrowed: ScheduleKind | ""
}) {
  const runs = lanes.reduce(
    (n, lane) => n + lane.times.filter((t) => t >= Math.floor(now / 60_000) * 60_000).length,
    0,
  )
  const busiest = columns.reduce<Column | undefined>(
    (best, column) => (column.entries.length > (best?.entries.length ?? 0) ? column : best),
    undefined,
  )
  // A schedule in nearly every column is the day's floor — a collector
  // every ten minutes, the hourly scripts — and drawn faint, so what runs
  // once or twice a day is what the eye lands on.
  const routine = useMemo(() => {
    const seen = new Map<string, number>()
    for (const column of columns)
      for (const { lane } of column.entries) seen.set(lane.key, (seen.get(lane.key) ?? 0) + 1)
    return new Set([...seen].filter(([, n]) => n >= 20).map(([key]) => key))
  }, [columns])
  const lane = pointed ? lanes.find((l) => l.key === pointed) : undefined
  const column = hour !== null ? columns[hour] : undefined

  let readout: React.ReactNode
  if (!settled) readout = "Reading the schedules…"
  else if (lane) {
    const next = upcoming(lane, now)
    readout = (
      <>
        <span className="font-medium text-foreground">{lane.name}</span>
        <FactDot />
        <span className="first-letter:uppercase">{lane.when}</span>
        {next !== undefined && (
          <>
            <FactDot />
            <span className="numeric">
              next {longClock(next, zone)}, in {countdown(next - now)}
            </span>
          </>
        )}
      </>
    )
  } else if (column) {
    const from = `${String(column.hour).padStart(2, "0")}:00`
    const to = `${String((column.hour + 1) % 24).padStart(2, "0")}:00`
    readout =
      column.entries.length === 0 ? (
        <span className="numeric">
          {from}–{to}: nothing runs
        </span>
      ) : (
        <>
          <span className="numeric font-medium text-foreground">
            {from}–{to}
          </span>
          <span className="min-w-0 truncate">
            {column.entries
              .map(
                ({ lane, runs }) =>
                  `${lane.name} ${
                    runs.length > 1 ? `×${runs.length}` : clockLabel(runs[0], now, zone).time
                  }`,
              )
              .join(" · ")}
          </span>
        </>
      )
  } else if (lanes.length === 0) {
    readout = `Nothing${narrowed ? ` in ${KIND[narrowed].label.toLowerCase()}` : ""} runs in the next 24 hours.`
  } else {
    readout = (
      <span>
        <span className="numeric font-medium text-foreground">{plural(runs, "run")}</span> from{" "}
        {plural(lanes.length, "schedule")} in the next 24 hours
        {routine.size > 0 && (
          <>
            {" "}
            · {routine.size === lanes.length ? "all" : routine.size} run every hour (the faint
            blocks)
          </>
        )}
        {busiest && busiest.entries.length > routine.size && (
          <>
            {" "}
            · busiest at{" "}
            <span className="numeric text-foreground">
              {String(busiest.hour).padStart(2, "0")}:00
            </span>
          </>
        )}
      </span>
    )
  }

  return (
    <div
      className="min-w-0 space-y-2"
      onMouseLeave={() => {
        onHour(null)
        onPoint(null)
      }}
    >
      <div
        role="img"
        aria-label={
          settled
            ? `${plural(runs, "run")} from ${plural(lanes.length, "schedule")} over the next 24 hours, an hour to a column`
            : "The next 24 hours"
        }
        className="grid h-20 grid-cols-24 gap-[3px]"
      >
        {columns.map((col, i) => {
          const over = col.entries.length > STACK
          const drawn = over ? col.entries.slice(0, STACK - 1) : col.entries
          return (
            <div
              key={col.at}
              data-hour={col.hour}
              onMouseEnter={() => onHour(i)}
              className={cn(
                "relative flex flex-col-reverse gap-[2px] rounded-[4px] p-[2px] transition-colors",
                i === 0 ? "bg-row-hover" : "bg-surface-sunken",
                hour === i && "bg-row-hover ring-1 ring-border",
                col.hour === 0 &&
                  i > 0 &&
                  "before:absolute before:-top-1 before:-bottom-6 before:-left-[2px] before:w-px before:bg-border",
              )}
            >
              {drawn.map(({ lane }) => (
                // A block is a pointer's shortcut to the row below it, which
                // is the control a keyboard and a screen reader reach.
                <span
                  key={lane.key}
                  aria-hidden
                  onMouseEnter={() => onPoint(lane.key)}
                  onMouseLeave={() => onPoint(null)}
                  onClick={() => onOpen(lane)}
                  className={cn(
                    "h-[10px] w-full shrink-0 cursor-pointer rounded-[2px] transition-opacity",
                    pointed
                      ? pointed === lane.key
                        ? "ring-1 ring-foreground/70"
                        : "opacity-20"
                      : routine.has(lane.key) && "opacity-35",
                  )}
                  style={{ background: KIND[lane.kind].color }}
                />
              ))}
              {over && (
                <span className="flex h-[10px] shrink-0 items-center justify-center text-micro leading-none font-medium text-muted-foreground">
                  +{col.entries.length - drawn.length}
                </span>
              )}
            </div>
          )
        })}
      </div>
      <div aria-hidden className="grid grid-cols-24 gap-[3px] text-micro text-muted-foreground">
        {columns.map((col, i) => {
          const label = hourLabel(col, i)
          const marked = i === 0 || col.hour === 0 || col.hour % 3 === 0
          return (
            <span
              key={col.at}
              className={cn(
                "numeric overflow-visible text-center whitespace-nowrap",
                !marked && "invisible",
                (i === 0 || col.hour === 0) && "font-medium text-foreground",
                // A phone's columns are a third as wide: every six hours.
                i !== 0 && col.hour % 6 !== 0 && "max-sm:invisible",
              )}
            >
              {label}
            </span>
          )
        })}
      </div>
      <p
        aria-live="polite"
        // Two lines on a phone, where a sentence does not fit beside the
        // columns; one everywhere else. Either way one height in every state.
        className="flex h-9 min-w-0 flex-wrap content-start items-center gap-x-2 overflow-hidden text-hint text-muted-foreground sm:h-5 sm:flex-nowrap sm:truncate"
      >
        {readout}
      </p>
    </div>
  )
}

function UpNextRow({
  lane,
  now,
  zone,
  arrived,
  soonest,
  pointed,
  onPoint,
  onOpen,
}: {
  lane: ScheduleLane
  now: number
  zone?: string
  arrived: boolean
  soonest: boolean
  pointed: boolean
  onPoint: () => void
  onOpen: () => void
}) {
  const next = upcoming(lane, now)
  const fired = lane.times.find((t) => t < now && now - t < 60_000)
  const at = next !== undefined ? clockLabel(next, now, zone) : undefined
  const due = fired ? "running now" : next !== undefined ? `in ${countdown(next - now)}` : ""

  return (
    <li
      data-lane={lane.key}
      onMouseEnter={onPoint}
      className={cn(
        "group -mx-3 grid h-12 cursor-pointer grid-cols-[4.25rem_minmax(0,1fr)_auto] items-center gap-3 border-b border-hairline px-3 transition-colors last:border-b-0 hover:bg-row-hover md:grid-cols-[5rem_1.75rem_minmax(0,1fr)_7.5rem_6.5rem]",
        pointed && "bg-row-hover",
        arrived && "animate-rise",
      )}
      // The whole row answers the pointer; the name is the button a keyboard
      // reaches (§12).
      onClick={(event) => {
        if (!(event.target as HTMLElement).closest("button")) onOpen()
      }}
    >
      <span
        className="numeric flex items-baseline gap-1.5 text-body"
        title={next !== undefined ? longClock(next, zone) : undefined}
      >
        {at?.day && <span className="text-hint text-muted-foreground">{at.day}</span>}
        <span className={cn("font-medium", soonest ? "text-foreground" : "text-foreground/90")}>
          {at?.time ?? "—"}
        </span>
      </span>
      <ProductLogo
        id={laneProduct(lane)}
        size="sm"
        fallback={lane.kind === "timer" ? Stopwatch : Clock}
        className="hidden size-7 md:flex [&_img]:size-4"
      />
      <div className="min-w-0">
        <button
          type="button"
          onClick={onOpen}
          onFocus={onPoint}
          className="block max-w-full truncate text-left text-body font-medium hover:underline"
          title={lane.runs}
        >
          {lane.name}
        </button>
        <p className="truncate text-hint text-muted-foreground first-letter:uppercase">
          {lane.when}
        </p>
      </div>
      <span className="hidden min-w-0 items-center gap-2 text-hint text-muted-foreground md:flex">
        <span
          aria-hidden
          className="size-2 shrink-0 rounded-[3px]"
          style={{ background: KIND[lane.kind].color }}
        />
        <span className="truncate">{KIND[lane.kind].one}</span>
      </span>
      <span
        className={cn(
          "numeric text-right text-hint whitespace-nowrap",
          soonest || fired ? "text-foreground" : "text-muted-foreground",
        )}
      >
        {due}
      </span>
    </li>
  )
}

/**
 * What fires next across every schedule, at the right end of the page's
 * identity line: its name, its clock time and a countdown to the second — the
 * one figure on the page that changes while it is read.
 */
export function NextUp({ lanes, zone }: { lanes: ScheduleLane[]; zone?: string }) {
  const now = useNow(1000)
  let best: { lane: ScheduleLane; at: number } | undefined
  for (const lane of lanes) {
    const at = upcoming(lane, now)
    if (at !== undefined && (!best || at < best.at)) best = { lane, at }
  }
  if (!best) {
    return <p className="text-body text-muted-foreground">Nothing is due</p>
  }
  const at = clockLabel(best.at, now, zone)
  return (
    <div
      data-slot="next-up"
      className="flex min-w-0 items-center gap-4"
      title={longClock(best.at, zone)}
    >
      <div className="min-w-0 sm:text-right">
        <p className="eyebrow">Next run</p>
        <p className="flex min-w-0 items-center gap-1.5 text-body sm:justify-end">
          <span
            aria-hidden
            className="size-2 shrink-0 rounded-[3px]"
            style={{ background: KIND[best.lane.kind].color }}
          />
          <span className="truncate font-medium">{best.lane.name}</span>
          <span className="numeric shrink-0 text-muted-foreground">
            at {at.day ? `${at.day} ` : ""}
            {at.time}
          </span>
        </p>
      </div>
      <p className="numeric text-2xl font-semibold tracking-tight tabular-nums">
        {countdown(best.at - now)}
      </p>
    </div>
  )
}

/** "in 4m 07s", to the second, for a run that is still ahead. */
export function Countdown({ at }: { at: number }) {
  const now = useNow(1000)
  return <>{at > now ? `in ${countdown(at - now)}` : "due now"}</>
}
