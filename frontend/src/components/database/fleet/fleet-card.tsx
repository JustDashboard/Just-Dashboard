"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Play } from "@/components/icons"
import { bytes, relativeTime, truncateMiddle } from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { CONTROL } from "@/components/flow"
import { Metric } from "@/components/page"
import { ProductGlyphs } from "@/components/product-logo"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { sectionHref, type Engine } from "@/components/database/engine"
import {
  reachWord,
  whereWord,
  type BackupReading,
  type Concern,
} from "@/components/database/fleet/fleet"
import type { FleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineMark, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import { DatabaseStatusMark, fleetStatus } from "@/components/database/shell/status"

/** What a card's third figure is called: a key–value store has clients, not sessions. */
export function sessionsWord(engine: Engine) {
  return engine.kind === "keyvalue" ? "Clients" : "Sessions"
}

/** The engine's word for what it holds, as a column or a figure is headed. */
export function objectsWord(entry: DbFleetEntry) {
  return entry.objectWord[0].toUpperCase() + entry.objectWord.slice(1)
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
 */
export function FleetCard({
  entry,
  engine,
  concerns,
  backup,
  feeds,
  fileSize,
  control,
  index,
}: {
  entry: DbFleetEntry
  engine: Engine
  concerns: Concern[]
  backup: BackupReading | undefined
  feeds: { products: string[]; count: number } | undefined
  /** What a file weighs, where discovery measured it and the engine reports no size. */
  fileSize?: number
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
  const stored = entry.sizesKnown ? entry.bytes : fileSize
  const pages = workPages(engine)

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
                <span className="min-w-0 truncate pr-0.5">
                  {engine.label}
                  {entry.versionNumber ? ` ${entry.versionNumber}` : ""}
                </span>
              </div>
            </div>

            {failed ? (
              // What the server said, where its figures would be: a failure
              // is never drawn as three zeros.
              <p className="line-clamp-2 min-h-10 font-mono text-hint leading-relaxed break-words text-destructive">
                {status.error || "The server did not answer."}
              </p>
            ) : (
              <div
                className={cn(
                  "grid min-h-10 grid-cols-3 gap-x-3 [&>*+*]:border-l [&>*+*]:border-hairline [&>*+*]:pl-3",
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
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                {feeds && feeds.count > 0 && (
                  <span className="flex shrink-0 items-center gap-1.5">
                    feeds
                    <ProductGlyphs ids={feeds.products} max={3} />
                    <span className="numeric">{feeds.count}</span>
                  </span>
                )}
                {backup?.newest ? (
                  <span className={cn("shrink-0", has("stale-backup") && "text-warning")}>
                    backed up {relativeTime(backup.newest)}
                  </span>
                ) : (
                  has("never-backed-up") && (
                    <span className="shrink-0 text-warning">never backed up</span>
                  )
                )}
                {entry.exposure !== "unknown" && (
                  <span className={cn("shrink-0", has("public") && "text-warning")}>
                    {has("public") ? "open to the internet" : reachWord(entry.exposure)}
                  </span>
                )}
                {/* The labels the operator gave it close the line of facts:
                    properties of the database, at the row's edge (§4). */}
                <EnvironmentTag environment={entry.environment} />
                {entry.readOnly && <ProtectedTag />}
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
                      onClick={() => control.power(entry, "start")}
                      aria-label={`Start ${entry.name}`}
                    >
                      <Play className="size-3" />
                      Start
                    </Button>
                  ) : (
                    // The ways in are offered on a server that answers; one
                    // that does not is opened by its card, to see why.
                    entry.ok &&
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
