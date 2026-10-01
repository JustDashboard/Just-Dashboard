"use client"

import { bytes, plural, relativeTime } from "@/lib/format"
import { TileTrend } from "@/components/metrics/sparkline"
import { ProductGlyphs } from "@/components/product-logo"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Skeleton } from "@/components/ui/skeleton"
import type { FleetShow } from "@/components/database/fleet/fleet"
import type { FleetData } from "@/components/database/fleet/use-fleet"

const TILE = "h-full transition-colors group-hover:bg-row-hover"

/**
 * The fleet in five figures, each of which is also the question it answers:
 * pressing one narrows the cards below to the databases it is about — the
 * ones that are not running, the ones holding the data, the ones with
 * sessions open, the ones with no dump from the last day.
 *
 * Every figure says where it came from in its hint, and one that could not be
 * read says so: a fleet that did not answer is a dash, never a zero.
 */
export function FleetReadings({
  data,
  engines,
  show,
  onShow,
}: {
  data: FleetData
  /** The logo ids of the engines in the fleet, for the first tile's hint. */
  engines: string[]
  show: FleetShow
  onShow: (show: FleetShow) => void
}) {
  const { readings, sessionSamples, fleet, summary } = data
  const toggle = (which: FleetShow) => onShow(show === which ? "all" : which)
  const awayWords = [
    readings.stopped > 0 && `${readings.stopped} stopped`,
    readings.failing > 0 && `${readings.failing} not answering`,
  ].filter(Boolean)
  const unsized = readings.total - readings.sized
  const stale = readings.dumpable - readings.fresh
  const summaryFailed = Boolean(summary.error && !summary.data)

  return (
    <StatGrid columns={5} dense className="animate-rise">
      <StatButton label="Show every database" onClick={() => onShow("all")}>
        <StatTile
          className={TILE}
          label="Databases"
          value={<NumberTicker value={readings.total} />}
          hint={
            fleet.error ? (
              "the last check did not finish"
            ) : (
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate">{plural(engines.length, "engine")}</span>
                <ProductGlyphs ids={engines} max={3} />
              </span>
            )
          }
        />
      </StatButton>

      <StatButton
        label={
          show === "down" ? "Show every database again" : "Show the databases that are not running"
        }
        pressed={show === "down"}
        onClick={() => toggle("down")}
      >
        <StatTile
          className={TILE}
          label="Running"
          value={readings.running.toLocaleString()}
          trailing={`of ${readings.total.toLocaleString()}`}
          tone={readings.failing > 0 ? "danger" : readings.stopped > 0 ? "warning" : "default"}
          hint={
            awayWords.length > 0
              ? awayWords.join(" · ")
              : `all answered ${relativeTime(fleet.data?.checkedAt)}`
          }
        />
      </StatButton>

      <StatButton
        label={
          show === "stored" ? "Show every database again" : "Show the databases by what they store"
        }
        pressed={show === "stored"}
        onClick={() => toggle("stored")}
      >
        <StatTile
          className={TILE}
          label="Stored"
          value={readings.sized > 0 ? bytes(readings.bytes) : "—"}
          hint={
            readings.sized === 0
              ? "no server reported its size"
              : unsized > 0
                ? `${unsized} of ${readings.total} did not report a size`
                : `across ${plural(readings.sized, "database")}`
          }
        />
      </StatButton>

      <StatButton
        label={
          show === "busy" ? "Show every database again" : "Show the databases with open sessions"
        }
        pressed={show === "busy"}
        onClick={() => toggle("busy")}
      >
        <StatTile
          className={TILE}
          label="Sessions"
          value={readings.answering > 0 ? readings.sessions.toLocaleString() : "—"}
          trailing={readings.answering > 0 ? "open" : undefined}
          trend={
            <TileTrend
              values={sessionSamples.map((sample) => sample.value)}
              color="var(--chart-2)"
              label="Open sessions since this page was opened"
            />
          }
          hint={
            readings.answering > 0
              ? `on ${plural(readings.answering, "server")}`
              : "no server answered"
          }
        />
      </StatButton>

      <StatButton
        label={
          show === "unprotected"
            ? "Show every database again"
            : "Show the databases with no backup from the last day"
        }
        pressed={show === "unprotected"}
        onClick={() => toggle("unprotected")}
      >
        <StatTile
          className={TILE}
          label="Backed up"
          value={readings.dumpable > 0 ? readings.fresh.toLocaleString() : "—"}
          trailing={readings.dumpable > 0 ? `of ${readings.dumpable}` : undefined}
          tone={readings.dumpable > 0 && stale > 0 ? "warning" : "default"}
          hint={
            summaryFailed
              ? "the dump directories could not be read"
              : readings.dumpable === 0
                ? "nothing here can be dumped"
                : readings.never > 0
                  ? `${readings.never} never backed up`
                  : stale > 0
                    ? `${stale} older than a day`
                    : "every one in the last day"
          }
        />
      </StatButton>
    </StatGrid>
  )
}

/** The five tiles before the fleet has answered, in the shape they arrive in. */
export function ReadingsSkeleton() {
  return (
    <StatGrid columns={5} dense aria-hidden>
      {["Databases", "Running", "Stored", "Sessions", "Backed up"].map((label) => (
        <div key={label} className="flex min-w-0 flex-col gap-1.5 px-5 py-4">
          <p className="eyebrow truncate">{label}</p>
          <Skeleton className="h-7 w-16" />
          <Skeleton className="h-3 w-28" />
        </div>
      ))}
    </StatGrid>
  )
}
