"use client"

import { bytes, plural, relativeTime } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { BarReading } from "@/components/bar-reading"
import { ProductGlyphs, hasProductLogo } from "@/components/product-logo"
import { NumberTicker } from "@/components/ui/number-ticker"
import { sectionHref, type Engine } from "@/components/database/engine"
import {
  backupAges,
  backupCounts,
  shares,
  type BackupStanding,
} from "@/components/database/fleet/band"
import { sessionsWord } from "@/components/database/fleet/fleet-card"
import { EngineGlyph } from "@/components/database/kit"

/** How many databases a block names; the rest are counted at its strip's end. */
const SHOWN = 5

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })

/**
 * The fleet read three ways under the identity line, each block a question
 * the cards answer only one database at a time: where the bytes are, who is
 * connected, and how old every database's newest dump is. Each is a bar and
 * one line, so it is one height with a reading or without, and the cards under
 * the band do not move as readings land. The fleet is polled, so nothing here
 * is marked live.
 *
 * Every database is drawn in its own hue — the same on its span and the size
 * bar on its card — and as its engine's own mark, so "the big orange one is
 * analytics" holds from the bar to the card. A press opens the database it is
 * about; a press under Backups opens the dumps of the worst in that standing.
 *
 * The section's "Needs attention" list stood here and was removed at the
 * operator's request. What it said is still said where the thing is: a dump
 * that is over a week old or was never taken is amber under Backups and on
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
      <BackupsBlock entries={entries} newestOf={newestOf} dumps={dumps} />
    </div>
  )
}

/** A block's figure in its header: the reading in the foreground, its word after it. */
function HeadFigure({ children, word }: { children: React.ReactNode; word: string }) {
  return (
    <span className="numeric flex h-7 items-center gap-1 text-hint whitespace-nowrap text-muted-foreground">
      <span className="font-medium text-foreground">{children}</span>
      {word}
    </span>
  )
}

/** What a block's strip leaves out, cut to what fits with the whole of it on a pointer. */
function Note({ parts }: { parts: (string | false)[] }) {
  const words = parts.filter(Boolean).join(" · ")
  if (!words) return null
  return (
    <span className="truncate" title={words}>
      {words}
    </span>
  )
}

/** The engine and its version, the few words a line has room for after the name. */
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
    <BarReading
      title="Storage"
      label="Storage"
      list="Largest databases"
      rest={read.rest}
      restLabel="Other databases"
      format={(value) => bytes(value)}
      figure={<HeadFigure word="stored">{bytes(read.total)}</HeadFigure>}
      message={read.top.length === 0 ? "No database has reported what it holds." : undefined}
      note={
        <Note
          parts={[
            read.more > 0 && plural(read.more, "more database"),
            read.unknown > 0 && `${plural(read.unknown, "database")} not reporting a size`,
          ]}
        />
      }
      parts={read.top.map(({ entry, value }) => {
        const engine = engineOf(entry)
        return {
          key: String(entry.id),
          value,
          color: hueOf(entry),
          mark: <EngineGlyph engine={engine} />,
          name: entry.name,
          detail: productLine(entry, engine),
          figure: bytes(value),
          label: `${entry.name} ${bytes(value)}`,
          title: `${entry.name} holds ${bytes(value)}`,
          href: { label: `Open ${entry.name}`, to: sectionHref(entry.id) },
        }
      })}
    />
  )
}

/**
 * Who is connected now: the open sessions (a key–value store's clients) of
 * every server that answered, as one bar, and on the line the things the map
 * has seen reading the database, drawn as their products.
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
    <BarReading
      title="Sessions"
      label="Sessions"
      list="Busiest databases"
      trail
      rest={read.rest}
      restLabel="Other databases"
      format={(value) => `${value.toLocaleString()} open`}
      figure={
        <HeadFigure word="open now">
          <NumberTicker value={read.total} />
        </HeadFigure>
      }
      message={
        read.top.length === 0 ? "Nothing is connected to any database right now." : undefined
      }
      note={
        <Note
          parts={[
            read.more > 0 && `${plural(read.more, "more database")} with sessions`,
            read.unknown > 0 && `${plural(read.unknown, "server")} not answering`,
          ]}
        />
      }
      parts={read.top.map(({ entry, value }) => {
        const engine = engineOf(entry)
        const word = sessionsWord(engine).toLowerCase()
        // The readers drawn as their products; a reader nothing names (another
        // machine, this server's processes) has no mark.
        const marks = (feeds.get(entry.id)?.products ?? []).filter(hasProductLogo)
        return {
          key: String(entry.id),
          value,
          color: hueOf(entry),
          mark: <EngineGlyph engine={engine} />,
          name: entry.name,
          detail: marks.length > 0 && (
            <span className="flex items-center gap-1.5">
              feeds
              <ProductGlyphs ids={marks} max={3} />
            </span>
          ),
          figure: (
            <>
              {compact.format(value)}
              <span className="ml-1 font-normal text-muted-foreground">{word}</span>
            </>
          ),
          label: `${entry.name} ${value.toLocaleString()} open`,
          title: `${value.toLocaleString()} ${word} open on ${entry.name}`,
          href: { label: `Open ${entry.name}`, to: sectionHref(entry.id) },
        }
      })}
    />
  )
}

const AGE_TONE: Record<BackupStanding, string> = {
  fresh: "text-muted-foreground",
  aging: "text-muted-foreground",
  stale: "text-warning",
  never: "font-medium text-warning",
}

/**
 * Each standing's span and word, the worst first: a fixed bar's first part is
 * the one its line reads at rest, and that is the one worth reading.
 */
const STANDINGS: { standing: BackupStanding; color: string; word: string }[] = [
  { standing: "never", color: "var(--warning)", word: "never" },
  {
    standing: "stale",
    color: "color-mix(in oklab, var(--warning) 50%, transparent)",
    word: "over a week old",
  },
  { standing: "aging", color: "var(--muted-foreground)", word: "this week" },
  { standing: "fresh", color: "var(--success)", word: "today" },
]

/**
 * Every database that can be dumped from here: how many were backed up today,
 * this week, longer ago or never, as one bar, each standing read by the worst
 * database in it. A press opens that database's dumps.
 */
function BackupsBlock({
  entries,
  newestOf,
  dumps,
}: {
  entries: DbFleetEntry[]
  newestOf: (entry: DbFleetEntry) => string | undefined
  dumps: (entry: DbFleetEntry) => boolean
}) {
  const rows = backupAges(entries, { newestOf, dumps })
  const counts = backupCounts(rows)
  const cannot = entries.filter(
    (entry) => !entry.broken && entry.state !== "broken" && !dumps(entry),
  ).length
  return (
    <BarReading
      title="Backups"
      label="Backups"
      list="Databases by last backup"
      className="lg:col-span-2 xl:col-span-1"
      fixed
      figure={
        rows.length > 0 && (
          <HeadFigure word={`of ${rows.length} in the last day`}>
            <NumberTicker value={counts.fresh} />
          </HeadFigure>
        )
      }
      message={rows.length === 0 ? "No database here can be dumped from the dashboard." : undefined}
      note={
        <Note parts={[cannot > 0 && `${plural(cannot, "database")} the dashboard cannot dump`]} />
      }
      parts={STANDINGS.filter(({ standing }) => counts[standing] > 0).map(
        ({ standing, color, word }) => {
          // The rows run worst first, so this is the worst in its standing.
          const { entry, newest } = rows.find((row) => row.standing === standing)!
          const count = counts[standing]
          const age = standing === "never" ? "never backed up" : `backed up ${relativeTime(newest)}`
          return {
            key: standing,
            value: count,
            color,
            mark: <span className="numeric text-hint font-medium">{count}</span>,
            name: `${count} ${word}`,
            detail: entry.name,
            figure: <span className={cn("text-hint font-normal", AGE_TONE[standing])}>{age}</span>,
            label: `${count} ${word}`,
            title: `${entry.name}: ${age}`,
            href: { label: `Backups of ${entry.name}`, to: sectionHref(entry.id, "backups") },
          }
        },
      )}
    />
  )
}
