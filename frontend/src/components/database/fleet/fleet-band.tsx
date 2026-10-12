"use client"

import Link from "next/link"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyphs, hasProductLogo } from "@/components/product-logo"
import { ShareBar } from "@/components/procs/workloads"
import { NumberTicker } from "@/components/ui/number-ticker"
import { sectionHref, type Engine } from "@/components/database/engine"
import {
  backupRunway,
  shares,
  STALE_AT,
  type BackupStanding,
  type RunwayRow,
} from "@/components/database/fleet/band"
import { sessionsWord } from "@/components/database/fleet/fleet-card"
import { EngineGlyph } from "@/components/database/kit"

/** How many databases a block names; the rest are counted under it. */
const SHOWN = 5

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })

const rowClass =
  "flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"

/**
 * The fleet read three ways under the identity line, each block a question
 * the cards answer only one database at a time: where the bytes are, who is
 * connected, and how old every database's newest dump is.
 *
 * Every database is drawn in its own hue — the same on its span, its dot, its
 * key in a row and the edge of its card — and as its engine's own mark, so
 * "the big orange one is analytics" holds from the bar to the card. A row
 * opens the database it is about; a row on the runway opens its dumps.
 *
 * The section's "Needs attention" list stood here and was removed at the
 * operator's request. What it said is still said where the thing is: a dump
 * that is over a week old or was never taken is amber on the runway and on
 * the card, a server that does not answer is the verdict at the identity
 * line's end and the first card on its shelf, and a server found running
 * that waits for a password is a row under *Found on this server*.
 */
export function FleetBand({
  entries,
  hueOf,
  engineOf,
  storedOf,
  newestOf,
  dumps,
  feeds,
}: {
  entries: DbFleetEntry[]
  hueOf: (entry: DbFleetEntry) => string
  engineOf: (entry: DbFleetEntry) => Engine
  storedOf: (entry: DbFleetEntry) => number | undefined
  newestOf: (entry: DbFleetEntry) => string | undefined
  dumps: (entry: DbFleetEntry) => boolean
  feeds: Map<number, { products: string[]; count: number }>
}) {
  return (
    <div
      data-slot="fleet-band"
      className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2 xl:grid-cols-3 [&>*]:min-w-0"
    >
      <StorageBlock entries={entries} hueOf={hueOf} engineOf={engineOf} storedOf={storedOf} />
      <SessionsBlock entries={entries} hueOf={hueOf} engineOf={engineOf} feeds={feeds} />
      <BackupsBlock
        entries={entries}
        hueOf={hueOf}
        engineOf={engineOf}
        newestOf={newestOf}
        dumps={dumps}
      />
    </div>
  )
}

/** The key that finds a database's span on a bar, in the hue its card carries. */
function HueKey({ color }: { color: string }) {
  return (
    <span
      aria-hidden
      data-slot="hue-key"
      className="h-2.5 w-0.75 shrink-0 rounded-full"
      style={{ background: color }}
    />
  )
}

/** A block's figure in its header: the reading in the foreground, its word after it. */
function HeadFigure({ children, word }: { children: React.ReactNode; word: string }) {
  return (
    <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
      <span className="font-medium text-foreground">{children}</span>
      {word}
    </span>
  )
}

/** The line under a block saying what it left out, or nothing when it left nothing out. */
function Remainder({ parts }: { parts: React.ReactNode[] }) {
  const shown = parts.filter(Boolean)
  if (shown.length === 0) return null
  return (
    <p className="flex flex-wrap items-center gap-x-2 gap-y-1 border-t border-hairline pt-3 text-hint text-muted-foreground">
      {shown.map((part, i) => (
        <span key={i} className="inline-flex items-center gap-2">
          {i > 0 && <span className="text-muted-foreground/40">·</span>}
          {part}
        </span>
      ))}
    </p>
  )
}

/** A short bar of one database's figure against the largest, in its hue. */
function Meter({ value, largest, color }: { value: number; largest: number; color: string }) {
  return (
    <span
      aria-hidden
      className="hidden h-1 w-12 shrink-0 overflow-hidden rounded-full bg-meter-track sm:block"
    >
      <span
        className="block h-full rounded-full transition-[width] duration-700 ease-out"
        style={{ width: `${largest > 0 ? (value / largest) * 100 : 0}%`, background: color }}
      />
    </span>
  )
}

/** The engine and its version, the few words a row has room for after the name. */
function productLine(entry: DbFleetEntry, engine: Engine) {
  return `${engine.label}${entry.versionNumber ? ` ${entry.versionNumber}` : ""}`
}

function StorageBlock({
  entries,
  hueOf,
  engineOf,
  storedOf,
}: {
  entries: DbFleetEntry[]
  hueOf: (entry: DbFleetEntry) => string
  engineOf: (entry: DbFleetEntry) => Engine
  storedOf: (entry: DbFleetEntry) => number | undefined
}) {
  const read = shares(entries, storedOf, SHOWN)
  return (
    <Panel plain aria-label="Storage">
      <PanelHeader
        title="Storage"
        actions={<HeadFigure word="stored">{bytes(read.total)}</HeadFigure>}
      />
      <PanelBody className="space-y-3 pt-4">
        {read.top.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            No database has reported what it holds.
          </p>
        ) : (
          <>
            <ShareBar
              label="What each database holds"
              capacity={read.total}
              rest={read.rest}
              parts={read.top.map(({ entry, value }) => ({
                key: String(entry.id),
                value,
                color: hueOf(entry),
                label: `${entry.name} ${bytes(value)}`,
              }))}
              format={(value) => bytes(value)}
            />
            <ul aria-label="Largest databases" className="-mx-2">
              {read.top.map(({ entry, value }) => {
                const engine = engineOf(entry)
                return (
                  <li key={entry.id}>
                    <Link
                      href={sectionHref(entry.id)}
                      aria-label={`Open ${entry.name}`}
                      title={`${entry.name} holds ${bytes(value)}`}
                      className={rowClass}
                    >
                      <HueKey color={hueOf(entry)} />
                      <span className="flex size-4 shrink-0 items-center justify-center">
                        <EngineGlyph engine={engine} />
                      </span>
                      <span className="min-w-0 truncate font-medium">{entry.name}</span>
                      <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
                        {productLine(entry, engine)}
                      </span>
                      <span className="numeric shrink-0 font-medium">{bytes(value)}</span>
                      <Meter value={value} largest={read.largest} color={hueOf(entry)} />
                    </Link>
                  </li>
                )
              })}
            </ul>
          </>
        )}
        <Remainder
          parts={[
            read.more > 0 && `${plural(read.more, "more database")}`,
            read.unknown > 0 && `${plural(read.unknown, "database")} not reporting a size`,
          ]}
        />
      </PanelBody>
    </Panel>
  )
}

/**
 * Who is connected now: the open sessions (a key–value store's clients) of
 * every server that answered, as one bar, and beside each of the busiest the
 * things the map has seen reading it, drawn as their products.
 */
function SessionsBlock({
  entries,
  hueOf,
  engineOf,
  feeds,
}: {
  entries: DbFleetEntry[]
  hueOf: (entry: DbFleetEntry) => string
  engineOf: (entry: DbFleetEntry) => Engine
  feeds: Map<number, { products: string[]; count: number }>
}) {
  // A server that did not answer has no count to give, which is not zero.
  const read = shares(entries, (entry) => (entry.ok ? entry.sessions : undefined), SHOWN)
  return (
    <Panel plain aria-label="Sessions">
      <PanelHeader
        title="Sessions"
        actions={
          <HeadFigure word="open now">
            <NumberTicker value={read.total} />
          </HeadFigure>
        }
      />
      <PanelBody className="space-y-3 pt-4">
        {read.top.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            Nothing is connected to any database right now.
          </p>
        ) : (
          <>
            <ShareBar
              label="Open sessions by database"
              capacity={read.total}
              rest={read.rest}
              parts={read.top.map(({ entry, value }) => ({
                key: String(entry.id),
                value,
                color: hueOf(entry),
                label: `${entry.name} ${value.toLocaleString()} open`,
              }))}
              format={(value) => `${value.toLocaleString()} open`}
            />
            <ul aria-label="Busiest databases" className="-mx-2">
              {read.top.map(({ entry, value }) => {
                const engine = engineOf(entry)
                // The readers drawn as their products; a reader nothing names
                // (another machine, this server's processes) has no mark.
                const marks = (feeds.get(entry.id)?.products ?? []).filter(hasProductLogo)
                return (
                  <li key={entry.id}>
                    <Link
                      href={sectionHref(entry.id)}
                      aria-label={`Open ${entry.name}`}
                      title={`${value.toLocaleString()} ${sessionsWord(engine).toLowerCase()} open on ${entry.name}`}
                      className={rowClass}
                    >
                      <HueKey color={hueOf(entry)} />
                      <span className="flex size-4 shrink-0 items-center justify-center">
                        <EngineGlyph engine={engine} />
                      </span>
                      <span className="min-w-0 truncate font-medium">{entry.name}</span>
                      <span className="flex min-w-0 flex-1 items-center gap-1.5 text-hint text-muted-foreground">
                        {marks.length > 0 && (
                          <>
                            <span className="shrink-0">feeds</span>
                            <ProductGlyphs ids={marks} max={3} />
                          </>
                        )}
                      </span>
                      <span className="numeric shrink-0 font-medium">
                        {compact.format(value)}
                        <span className="ml-1 font-normal text-muted-foreground">
                          {sessionsWord(engine).toLowerCase()}
                        </span>
                      </span>
                      <Meter value={value} largest={read.largest} color={hueOf(entry)} />
                    </Link>
                  </li>
                )
              })}
            </ul>
          </>
        )}
        <Remainder
          parts={[
            read.more > 0 && `${plural(read.more, "more database")} with sessions`,
            read.unknown > 0 && `${plural(read.unknown, "server")} not answering`,
          ]}
        />
      </PanelBody>
    </Panel>
  )
}

const AGE_TONE: Record<BackupStanding, string> = {
  fresh: "text-muted-foreground",
  aging: "text-muted-foreground",
  stale: "text-warning",
  never: "font-medium text-warning",
}

/**
 * Every database that can be dumped from here on one runway from "now" to a
 * fortnight, each a dot in its hue where its newest dump's age puts it, with
 * the stretch past a week washed amber — the age every other reading of the
 * section calls stale. Never-dumped ones come first, a hollow amber ring at
 * the far end. A row opens the database's dumps.
 */
function BackupsBlock({
  entries,
  hueOf,
  engineOf,
  newestOf,
  dumps,
}: {
  entries: DbFleetEntry[]
  hueOf: (entry: DbFleetEntry) => string
  engineOf: (entry: DbFleetEntry) => Engine
  newestOf: (entry: DbFleetEntry) => string | undefined
  dumps: (entry: DbFleetEntry) => boolean
}) {
  const rows = backupRunway(entries, { newestOf, dumps })
  const shown = rows.slice(0, SHOWN)
  const fresh = rows.filter((row) => row.standing === "fresh").length
  const cannot = entries.filter(
    (entry) => !entry.broken && entry.state !== "broken" && !dumps(entry),
  ).length
  return (
    <Panel plain aria-label="Backups" className="lg:col-span-2 xl:col-span-1">
      <PanelHeader
        title="Backups"
        actions={
          rows.length > 0 && (
            <HeadFigure word={`of ${rows.length} in the last day`}>
              <NumberTicker value={fresh} />
            </HeadFigure>
          )
        }
      />
      <PanelBody className="pt-1">
        {shown.length === 0 ? (
          <p className="py-3 text-body text-muted-foreground">
            No database here can be dumped from the dashboard.
          </p>
        ) : (
          <ol aria-label="Backup runway">
            {shown.map((row, index) => (
              <Runway
                key={row.entry.id}
                row={row}
                color={hueOf(row.entry)}
                engine={engineOf(row.entry)}
                first={index === 0}
              />
            ))}
          </ol>
        )}
        <Remainder
          parts={[
            rows.length > shown.length && `${rows.length - shown.length} more`,
            cannot > 0 && `${plural(cannot, "database")} the dashboard cannot dump`,
          ]}
        />
      </PanelBody>
    </Panel>
  )
}

function Runway({
  row,
  color,
  engine,
  first,
}: {
  row: RunwayRow
  color: string
  engine: Engine
  first: boolean
}) {
  const { entry, standing, newest, position } = row
  const age = standing === "never" ? "never backed up" : `backed up ${relativeTime(newest)}`
  const left = `${(position ?? 1) * 100}%`
  return (
    // The hairline stays inside the block; only the press target bleeds, so
    // its wash lines its text up with the title as every other row's does.
    <li className={cn(!first && "border-t border-hairline")}>
      <Link
        href={sectionHref(entry.id, "backups")}
        aria-label={`Backups of ${entry.name}`}
        className="-mx-2 block rounded-md px-2 py-2 focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <span className="flex min-w-0 items-center gap-2">
          <HueKey color={color} />
          <span className="flex size-4 shrink-0 items-center justify-center">
            <EngineGlyph engine={engine} />
          </span>
          <span className="min-w-0 flex-1 truncate text-body font-medium">{entry.name}</span>
          <span className={cn("numeric shrink-0 text-hint", AGE_TONE[standing])}>{age}</span>
        </span>
        <span
          role="img"
          aria-label={`${entry.name}: ${age}`}
          className="relative mt-1.5 block h-2.5"
        >
          <span
            aria-hidden
            className="absolute inset-y-0 right-0 rounded-sm bg-wash-warning"
            style={{ left: `${STALE_AT * 100}%` }}
          />
          <span aria-hidden className="absolute inset-x-0 top-1/2 h-px bg-hairline" />
          {standing === "never" ? (
            <span
              aria-hidden
              className="absolute top-1/2 right-0 size-2.5 -translate-y-1/2 rounded-full border-2 border-warning bg-background"
            />
          ) : (
            <>
              <span
                aria-hidden
                className="absolute top-1/2 left-0 h-px -translate-y-1/2 transition-[width] duration-700 ease-out"
                style={{
                  width: left,
                  background: `color-mix(in oklab, ${color} 60%, transparent)`,
                }}
              />
              <span
                aria-hidden
                className="absolute top-1/2 size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full transition-[left] duration-700 ease-out"
                style={{ left, background: color }}
              />
            </>
          )}
        </span>
      </Link>
    </li>
  )
}
