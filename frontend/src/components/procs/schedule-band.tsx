"use client"

import { useMemo, useState } from "react"
import { Clock, Stopwatch } from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural, timestamp } from "@/lib/format"
import { axisHours, countdown, type ScheduleKind, type ScheduleLane } from "@/lib/schedule"
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

/** How many lanes the band draws before "Show all". */
const SHOWN = 8

/**
 * Each place a schedule is kept, in the hue that names it (§15: colour that
 * names a kind is `--tag-*`). None is red or amber, so a lane never reads as
 * a lane that failed.
 */
export const KIND: Record<ScheduleKind, { label: string; color: string }> = {
  cron: { label: "Cron", color: "var(--tag-blue)" },
  timer: { label: "Timers", color: "var(--tag-violet)" },
  system: { label: "System cron", color: "var(--tag-cyan)" },
}

export function laneProduct(lane: ScheduleLane) {
  return lane.kind === "timer" ? unitProduct(lane.product) : programProduct(lane.product)
}

/** The lane's first run still ahead of `now`. */
function upcoming(lane: ScheduleLane, now: number) {
  return lane.times.find((t) => t >= now) ?? (lane.next && lane.next >= now ? lane.next : undefined)
}

/**
 * The next day of everything on the host's clock, one lane per schedule on
 * one axis that starts at this second.
 *
 * It replaced five tiles, and answers the question they answered one figure
 * at a time — what runs, and when — as a picture: a night of jobs bunched at
 * three in the morning, a health check every fifteen minutes as a comb across
 * the whole day, a weekly prune with nothing in it. Each lane's next run is a
 * dot in its kind's hue and the ones after it are ticks; a schedule that fires
 * more often than the axis can draw is a band. A cron line's runs are its
 * expression's; a timer's is the one systemd reports, since its later runs
 * are its calendar plus a random delay (`lib/schedule.ts`).
 *
 * The axis starts at now and every countdown ticks, so the soonest dot is seen
 * to approach, and the lanes reorder when it passes. A press opens the job or
 * the timer in its sheet.
 */
export function ScheduleBand({
  lanes,
  onOpen,
}: {
  lanes: ScheduleLane[]
  onOpen: (lane: ScheduleLane) => void
}) {
  const now = useNow(1000)
  const [kind, setKind] = useSessionState<ScheduleKind | "">("processes.scheduled.kind", "")
  const [all, setAll] = useState(false)

  const inWindow = useMemo(() => lanes.filter((lane) => lane.times.length > 0), [lanes])
  const counts = useMemo(() => {
    const out: Record<ScheduleKind, number> = { cron: 0, timer: 0, system: 0 }
    for (const lane of inWindow) out[lane.kind]++
    return out
  }, [inWindow])
  const listed = inWindow.filter((lane) => !kind || lane.kind === kind)
  const shown = all ? listed : listed.slice(0, SHOWN)
  // A schedule that turned up since the last read rises into its place; a
  // narrowed band is a new question, not arrivals, so this keys on all of them.
  const arrived = useArrivals(inWindow.map((lane) => lane.key))
  let soonest: { key: string; at: number } | undefined
  for (const lane of listed) {
    const at = upcoming(lane, now)
    if (at !== undefined && (!soonest || at < soonest.at)) soonest = { key: lane.key, at }
  }

  // What the band does not draw, said under it so a quiet axis is not read as
  // a quiet host: weekly and monthly work past the window, what runs only at
  // boot, and what is switched off.
  const rest = lanes.filter((lane) => (!kind || lane.kind === kind) && lane.times.length === 0)
  const later = rest.filter((lane) => lane.next !== undefined).length
  const atBoot = rest.filter((lane) => lane.job?.schedule.toLowerCase() === "@reboot").length
  const off = rest.filter(
    (lane) => lane.job?.disabled || (lane.timer && lane.timer.activeState !== "active"),
  ).length

  return (
    <Panel plain aria-label="Next 24 hours">
      <PanelHeader title="Next 24 hours">
        <ChipStrip aria-label="Kind" className="mr-auto">
          {(Object.keys(KIND) as ScheduleKind[]).map((key) => (
            <FilterChip
              key={key}
              selected={kind === key}
              onClick={() => setKind(kind === key ? "" : key)}
            >
              <span
                aria-hidden
                className="size-1.5 rounded-full"
                style={{ background: KIND[key].color }}
              />
              {KIND[key].label}
              <ChipCount>{counts[key]}</ChipCount>
            </FilterChip>
          ))}
        </ChipStrip>
      </PanelHeader>
      <PanelBody className="pt-1">
        {listed.length === 0 ? (
          <p className="py-6 text-body text-muted-foreground">
            Nothing {kind ? `in ${KIND[kind].label.toLowerCase()} ` : ""}runs in the next 24 hours.
          </p>
        ) : (
          <>
            <Axis now={now} />
            <ol aria-label="Schedules">
              {shown.map((lane) => (
                <Lane
                  key={lane.key}
                  lane={lane}
                  now={now}
                  arrived={arrived.has(lane.key)}
                  soonest={soonest?.key === lane.key}
                  onOpen={() => onOpen(lane)}
                />
              ))}
            </ol>
          </>
        )}
        {(listed.length > SHOWN || later + atBoot + off > 0) && (
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-t border-hairline pt-3 text-hint text-muted-foreground">
            {listed.length > SHOWN && (
              <>
                <Button
                  variant="ghost"
                  size="xs"
                  className="-ml-2 text-foreground"
                  onClick={() => setAll(!all)}
                >
                  {all ? "Show the next 8" : `Show all ${listed.length}`}
                </Button>
                {later + atBoot + off > 0 && <FactDot />}
              </>
            )}
            {[
              later > 0 && `${later} due after that`,
              atBoot > 0 && `${atBoot} only at boot`,
              off > 0 && `${off} switched off`,
            ]
              .filter(Boolean)
              .map((text, i) => (
                <span key={i} className="inline-flex items-center gap-2">
                  {i > 0 && <FactDot />}
                  {text}
                </span>
              ))}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

/** Where `at` falls across the window, as a share of the track. */
function place(at: number, now: number) {
  return Math.min(100, Math.max(0, ((at - now) / BAND_WINDOW) * 100))
}

/** One column template for the axis and every lane, so a dot sits under its hour. */
const ROW = "grid grid-cols-1 md:grid-cols-[minmax(0,15rem)_minmax(0,1fr)_5.5rem]"

const HOUR_LABEL = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
})
const DAY_LABEL = new Intl.DateTimeFormat(undefined, { weekday: "short" })

/**
 * The hours across the top, every three on the hour, the day's name where
 * the date turns; under it, a hairline down the whole band at each, so a dot
 * three lanes down is read against the hour above it.
 */
function Axis({ now }: { now: number }) {
  // Recomputed on the minute, not the second: the hours only move when one
  // passes.
  const minute = Math.floor(now / 60_000) * 60_000
  const hours = useMemo(() => axisHours(minute, minute + BAND_WINDOW, 3), [minute])
  return (
    <div aria-hidden className={ROW}>
      <div className="hidden md:block" />
      <div className="relative h-6 text-micro text-muted-foreground">
        <span className="absolute top-1 left-0 font-medium text-foreground">now</span>
        {hours.map((at) => {
          const midnight = new Date(at).getHours() === 0
          const left = place(at, now)
          // A label that would sit on top of "now" or run off the end is
          // left to the hairline alone.
          if (left < 6 || left > 96) return null
          return (
            <span
              key={at}
              className={cn(
                "numeric absolute top-1 -translate-x-1/2 whitespace-nowrap",
                midnight && "font-medium text-foreground",
                // Every other label gives way on a phone's narrower track.
                new Date(at).getHours() % 6 !== 0 && "max-sm:hidden",
              )}
              style={{ left: `${left}%` }}
            >
              {midnight ? DAY_LABEL.format(at) : HOUR_LABEL.format(at)}
            </span>
          )
        })}
      </div>
      <div className="hidden md:block" />
    </div>
  )
}

function Lane({
  lane,
  now,
  arrived,
  soonest,
  onOpen,
}: {
  lane: ScheduleLane
  now: number
  arrived: boolean
  soonest: boolean
  onOpen: () => void
}) {
  const next = upcoming(lane, now)
  const color = KIND[lane.kind].color
  const ahead = lane.times.filter((t) => t >= now)
  const fired = lane.times.find((t) => t < now && now - t < 60_000)
  const minute = Math.floor(now / 60_000) * 60_000
  const grid = useMemo(() => axisHours(minute, minute + BAND_WINDOW, 3), [minute])
  const due = fired ? "now" : next !== undefined ? `in ${countdown(next - now)}` : ""

  return (
    <li
      className={cn(
        ROW,
        "group -mx-3 cursor-pointer border-t border-hairline px-3 transition-colors hover:bg-row-hover",
        arrived && "animate-rise",
      )}
      // The whole row answers the pointer; the name is the button a keyboard
      // reaches (§12).
      onClick={(event) => {
        if (!(event.target as HTMLElement).closest("button")) onOpen()
      }}
    >
      <div className="flex min-w-0 items-center gap-3 py-2 md:pr-4">
        <ProductLogo
          id={laneProduct(lane)}
          size="sm"
          fallback={lane.kind === "timer" ? Stopwatch : Clock}
          className="size-7 [&_img]:size-4"
        />
        <div className="min-w-0 flex-1">
          <button
            type="button"
            onClick={onOpen}
            className="block max-w-full truncate text-left text-body font-medium hover:underline"
            title={lane.runs}
          >
            {lane.name}
          </button>
          <p className="truncate text-hint text-muted-foreground">{lane.when}</p>
        </div>
        <span className="numeric shrink-0 text-hint md:hidden">{due}</span>
      </div>
      <div
        role="img"
        aria-label={
          lane.dense
            ? `${lane.name}: more than ${ahead.length} runs in the next 24 hours`
            : `${lane.name}: ${plural(ahead.length, "run")} in the next 24 hours`
        }
        className="relative h-6 max-md:mb-2 md:h-auto"
      >
        {grid.map((at) => (
          <span
            key={at}
            aria-hidden
            className={cn(
              "absolute inset-y-0 w-px",
              new Date(at).getHours() === 0 ? "bg-border" : "bg-hairline",
            )}
            style={{ left: `${place(at, now)}%` }}
          />
        ))}
        <span aria-hidden className="absolute inset-x-0 top-1/2 h-px bg-hairline" />
        {lane.dense && ahead.length > 0 && (
          <span
            aria-hidden
            className="absolute top-1/2 right-0 h-1 -translate-y-1/2 rounded-full"
            style={{
              left: `${place(ahead[0], now)}%`,
              background: `color-mix(in oklab, ${color} 45%, transparent)`,
            }}
          />
        )}
        {!lane.dense &&
          ahead.slice(1).map((at) => (
            <span
              key={at}
              aria-hidden
              title={timestamp(new Date(at).toISOString())}
              className="absolute top-1/2 h-2.5 w-0.5 -translate-x-1/2 -translate-y-1/2 rounded-full"
              style={{
                left: `${place(at, now)}%`,
                background: `color-mix(in oklab, ${color} 60%, transparent)`,
              }}
            />
          ))}
        {next !== undefined && next - now <= BAND_WINDOW && (
          <span
            aria-hidden
            title={timestamp(new Date(next).toISOString())}
            className="absolute top-1/2 flex size-2.5 -translate-x-1/2 -translate-y-1/2"
            style={{ left: `${place(next, now)}%` }}
          >
            {/* The one dot on the band that is about to happen breathes:
                its countdown is the clock's own, not a remembered figure. */}
            {soonest && (
              <span
                className="absolute inset-0 animate-breathe rounded-full"
                style={{ background: color }}
              />
            )}
            <span
              className="relative size-2.5 rounded-full ring-2 ring-background"
              style={{ background: color }}
            />
          </span>
        )}
      </div>
      <div className="hidden items-center justify-end md:flex">
        <span
          className={cn("numeric text-hint", soonest ? "text-foreground" : "text-muted-foreground")}
          title={next !== undefined ? timestamp(new Date(next).toISOString()) : undefined}
        >
          {due}
        </span>
      </div>
    </li>
  )
}

/**
 * What fires next across every schedule, at the right end of the page's
 * identity line: its mark, its name and a countdown to the second — the one
 * figure on the page that changes while it is read.
 */
export function NextUp({ lanes }: { lanes: ScheduleLane[] }) {
  const now = useNow(1000)
  let best: { lane: ScheduleLane; at: number } | undefined
  for (const lane of lanes) {
    const at = upcoming(lane, now)
    if (at !== undefined && (!best || at < best.at)) best = { lane, at }
  }
  if (!best) {
    return <p className="text-body text-muted-foreground">Nothing is due</p>
  }
  return (
    <div
      data-slot="next-up"
      className="flex min-w-0 items-center gap-3"
      title={timestamp(new Date(best.at).toISOString())}
    >
      <div className="min-w-0 sm:text-right">
        <p className="eyebrow">Next run</p>
        <p className="flex min-w-0 items-center gap-1.5 text-body sm:justify-end">
          <span
            aria-hidden
            className="size-1.5 shrink-0 rounded-full"
            style={{ background: KIND[best.lane.kind].color }}
          />
          <span className="truncate font-medium">{best.lane.name}</span>
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
