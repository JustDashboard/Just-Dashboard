"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, LockClosed, Play } from "@/components/icons"
import { bytes, relativeTime, truncateMiddle } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { CONTROL } from "@/components/flow"
import { Metric } from "@/components/page"
import { ProductGlyphs } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { sectionHref, type Engine } from "@/components/database/engine"
import {
  concernLevel,
  reachWord,
  whereWord,
  type BackupReading,
  type Concern,
} from "@/components/database/fleet/fleet"
import type { FleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineMark, EnvironmentTag } from "@/components/database/kit"
import { DatabaseStatusMark, fleetStatus } from "@/components/database/shell/status"

/** What a card's third figure is called: a key–value store has clients, not sessions. */
export function sessionsWord(engine: Engine) {
  return engine.kind === "keyvalue" ? "Clients" : "Sessions"
}

/** The engine's word for what it holds, as a column or a figure is headed. */
export function objectsWord(entry: DbFleetEntry) {
  return entry.objectWord[0].toUpperCase() + entry.objectWord.slice(1)
}

/**
 * The hue a fact is said in: none while nothing is wrong with it, and the
 * level of the concern it is about once something is — the same level the
 * attention list gives it, so one fact never has two weights on one page.
 */
export function factTone(level: Concern["level"] | undefined) {
  if (level === "critical") return "text-destructive"
  if (level === "warning") return "text-warning"
  return undefined
}

/**
 * A protected connection, where there is room for a mark and not for the
 * word: a card's head, a table's name cell. The word is on the database's own
 * pages; here it is the lock, named for a reader who cannot see it and
 * spelled out to a pointer that rests on it.
 */
export function ProtectedMark({ className }: { className?: string }) {
  return (
    <Tag
      icon={LockClosed}
      title="Protected: the dashboard refuses every change to its data or schema"
      className={className}
    >
      <span className="sr-only">protected</span>
    </Tag>
  )
}

/** The two pages of a database the work is done on, in the engine's own words. */
export function workPages(engine: Engine) {
  return engine.sections.filter((section) => section.group === "work").slice(0, 2)
}

/**
 * One database on the control center: a card you open, drawn as its engine.
 *
 * Built on `SpotlightBorder` directly, the way a deployment's card is: it is
 * taller than a row and lays its readings out in lines. The press anywhere
 * opens the database's home, except on a control of its own (`CONTROL`), and
 * the name is the real link a keyboard reaches.
 *
 * Top to bottom it is what the reader asks of a database in that order: which
 * one and whether it answers; what it holds, in its engine's words; and the
 * quiet facts — what it feeds, when it was last dumped, how far it reaches —
 * which take a colour only when one of them is wrong. A stopped server keeps
 * its card with its figures stepped back and Start as its verb; one that is
 * being started, stopped or dumped runs a light round its edge and says so.
 *
 * What the operator said of it — what it is for, that it is protected — are
 * properties, not readings, and stand at the edge of the head's two lines:
 * the lock beside the menu, the environment after the engine. In the line of
 * facts an amber `staging` sat beside an amber "never backed up" and nobody
 * could tell which of them was the problem.
 */
export function FleetCard({
  entry,
  engine,
  concerns,
  backup,
  feeds,
  stored,
  control,
  index,
}: {
  entry: DbFleetEntry
  engine: Engine
  concerns: Concern[]
  backup: BackupReading | undefined
  feeds: { products: string[]; count: number } | undefined
  /** What it holds in bytes, where anybody knows: the page's one answer. */
  stored: number | undefined
  control: FleetControl
  index: number
}) {
  const router = useRouter()
  const href = sectionHref(entry.id)
  const busy = control.busyWord(entry)
  const status = fleetStatus(entry)
  const down = entry.state === "stopped" || entry.state === "paused"
  const failed = !entry.ok && !down
  const where = whereWord(entry)
  const has = (kind: Concern["kind"]) => concerns.some((concern) => concern.kind === kind)
  const pages = workPages(engine)
  const product = `${engine.label}${entry.versionNumber ? ` ${entry.versionNumber}` : ""}`

  return (
    // The slot is the one every lit row in the section carries: a card is a
    // row you take, drawn taller.
    <li className="min-w-0" data-slot="choice-row" data-card="database" data-state={entry.state}>
      {/* Each card lands a beat after the one before it, capped so a fleet
          of forty does not take three seconds. */}
      <BlurFade delay={Math.min(index, 11) * 0.04} className="h-full">
        <SpotlightBorder radius={360} className="h-full">
          {busy && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
              <BorderBeam size={80} duration={4} />
            </span>
          )}
          <div
            onClick={(event) => {
              // React carries a press inside the open menu up through its
              // portal to here, though it landed nowhere on the card.
              const target = event.target as HTMLElement
              if (!event.currentTarget.contains(target) || target.closest(CONTROL)) return
              router.push(href)
            }}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col gap-3.5 rounded-xl p-4"
          >
            <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1">
              <EngineMark engine={engine} className={cn("row-span-2", down && "opacity-60")} />
              <Link
                href={href}
                aria-label={`Open ${entry.name}`}
                className="block max-w-full min-w-0 truncate rounded-sm text-title leading-tight font-medium focus-ring"
              >
                {entry.name}
              </Link>
              <span className="-my-1 -mr-1.5 flex items-center gap-1">
                {entry.readOnly && <ProtectedMark className="mr-1" />}
                <ArrowRight
                  aria-hidden
                  className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/choice:text-foreground"
                />
                <VerbMenu verbs={control.verbsFor(entry)} label={`Actions for ${entry.name}`} />
              </span>
              <div className="col-span-2 col-start-2 flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
                {busy ? (
                  <TextShimmer className="shrink-0 text-xs font-medium">{`${busy}…`}</TextShimmer>
                ) : (
                  <DatabaseStatusMark status={status} />
                )}
                {/* A hair of padding: flush with its text, a truncating box
                    clips the last glyph without earning the ellipsis. */}
                <span className="min-w-0 flex-1 truncate pr-0.5" title={product}>
                  {product}
                </span>
                <EnvironmentTag environment={entry.environment} className="max-w-28" />
              </div>
            </div>

            {failed ? (
              // What the server said, where its figures would be: a failure
              // is never drawn as three zeros. Two whole lines and no part
              // of a third: the block is the height of two lines of its own
              // leading, inside the height the figures take on other cards.
              <div className="flex min-h-10 min-w-0 items-start">
                <p
                  data-slot="fleet-error"
                  title={status.error}
                  className="line-clamp-2 min-w-0 font-mono text-hint leading-4.5 break-words text-destructive"
                >
                  {status.error || "The server did not answer."}
                </p>
              </div>
            ) : (
              // The middle column is the engine's own noun and the widest of
              // the three words ("Collections"), so it takes the larger share.
              <div
                className={cn(
                  "grid min-h-10 grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_minmax(0,1fr)] gap-x-2.5 [&>*+*]:border-l [&>*+*]:border-hairline [&>*+*]:pl-2.5",
                  down && "opacity-60",
                )}
              >
                <Metric label="Stored" value={stored !== undefined ? bytes(stored) : "—"} />
                <Metric
                  label={objectsWord(entry)}
                  value={down ? "—" : entry.objects.toLocaleString()}
                />
                <Metric
                  label={sessionsWord(engine)}
                  value={down ? "—" : entry.sessions.toLocaleString()}
                />
              </div>
            )}

            {/* Gaps separate the facts rather than a middle dot, which is left
                dangling at the end of a line wherever this wraps. */}
            <div className="mt-auto flex min-w-0 flex-col gap-2 border-t border-hairline pt-3 text-hint text-muted-foreground">
              <div
                data-slot="fleet-facts"
                className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1"
              >
                {feeds && feeds.count > 0 && (
                  <span className="flex shrink-0 items-center gap-1.5">
                    feeds
                    <ProductGlyphs ids={feeds.products} max={3} />
                    <span className="numeric">{feeds.count}</span>
                  </span>
                )}
                {backup?.newest ? (
                  <span
                    className={cn("shrink-0", factTone(concernLevel(concerns, "stale-backup")))}
                  >
                    backed up {relativeTime(backup.newest)}
                  </span>
                ) : (
                  has("never-backed-up") && (
                    <span
                      className={cn(
                        "shrink-0",
                        factTone(concernLevel(concerns, "never-backed-up")),
                      )}
                    >
                      never backed up
                    </span>
                  )
                )}
                {entry.exposure !== "unknown" && (
                  <span className={cn("shrink-0", factTone(concernLevel(concerns, "public")))}>
                    {has("public") ? "open to the internet" : reachWord(entry.exposure)}
                  </span>
                )}
              </div>
              {/* Where it runs, and beside it the ways in: the card's last
                  line is the same line on every card. */}
              <div className="flex min-w-0 items-center justify-between gap-3">
                {/* Cut in the middle: the end of a path is the file's name. */}
                <span
                  title={where.text}
                  className={cn("min-w-0 truncate pr-0.5", where.mono && "font-mono")}
                >
                  {truncateMiddle(where.text, 30)}
                </span>
                <span className="-my-1 -mr-1.5 flex shrink-0 items-center gap-0.5">
                  {control.canStart(entry) && !busy ? (
                    <Button
                      size="xs"
                      variant="outline"
                      // The foot is muted and an outline button takes its
                      // ink from where it stands: the one fix a stopped
                      // database offers must not be the dimmest thing on it.
                      className="text-foreground"
                      onClick={() => control.power(entry, "start")}
                      aria-label={`Start ${entry.name}`}
                    >
                      <Play className="size-3" />
                      Start
                    </Button>
                  ) : entry.ok ? (
                    // The ways in are offered on a server that answers.
                    pages.map((page) => (
                      <Button
                        key={page.id}
                        size="xs"
                        variant="ghost"
                        className="text-foreground"
                        asChild
                      >
                        <Link
                          href={sectionHref(entry.id, page.id)}
                          aria-label={`${page.title} of ${entry.name}`}
                        >
                          {page.title}
                        </Link>
                      </Button>
                    ))
                  ) : (
                    // One that fails is fixed where its address and password
                    // are kept: the same fix its finding offers, one press
                    // from the card that says what the server answered.
                    failed && (
                      <Button size="xs" variant="ghost" className="text-foreground" asChild>
                        <Link
                          href={sectionHref(entry.id, "settings")}
                          aria-label={`Settings of ${entry.name}`}
                        >
                          Settings
                        </Link>
                      </Button>
                    )
                  )}
                </span>
              </div>
            </div>
          </div>
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}
