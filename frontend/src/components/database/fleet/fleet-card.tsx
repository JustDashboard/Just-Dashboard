"use client"

import Link from "next/link"
import { Play, Wrench } from "@/components/icons"
import {
  byteParts,
  compactCount,
  grouped,
  plural,
  relativeTime,
  truncateMiddle,
} from "@/lib/format"
import type { DbFleetEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useChoicePress } from "@/components/choice-row"
import { ProductGlyphs } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { BlurFade } from "@/components/ui/blur-fade"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { sectionHref, type Engine } from "@/components/database/engine"
import {
  concernLevel,
  nounFor,
  objectNouns,
  sessionNouns,
  whereWord,
  type BackupReading,
  type Concern,
  type Nouns,
} from "@/components/database/fleet/fleet"
import type { FleetControl } from "@/components/database/fleet/use-fleet-control"
import { EngineMark, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import {
  DatabaseStatusMark,
  fleetStatus,
  statusLabel,
  statusTone,
} from "@/components/database/shell/status"

/**
 * The hue a fact is said in: none while nothing is wrong with it, and the
 * level of the concern it is about once something is, so one fact never has
 * two weights on one page.
 */
export function factTone(level: Concern["level"] | undefined) {
  if (level === "critical") return "text-destructive"
  if (level === "warning") return "text-warning"
  return undefined
}

/** The two pages of a database the work is done on, in the engine's own words. */
export function workPages(engine: Engine) {
  return engine.sections.filter((section) => section.group === "work").slice(0, 2)
}

/**
 * Where it runs, where that says more than its name: a container named as
 * the connection is (the usual case for one the dashboard started) is the
 * card's title a second time, and the shelf already says it is a container.
 */
export function placeBesides(entry: DbFleetEntry): { text: string; mono: boolean } | undefined {
  const where = whereWord(entry)
  if (entry.source === "docker" && (!entry.container || entry.container === entry.name)) {
    return undefined
  }
  if (entry.source === "unknown") return undefined
  return { text: where.text, mono: where.mono }
}

/**
 * One database on the control center: a card you open, drawn as its engine.
 *
 * Built on `SpotlightBorder` directly, the way a deployment's card is. The
 * press anywhere opens the database's home by the rule every card takes
 * (`useChoicePress`): not on a control of its own, not at the end of a text
 * selection, and in a new tab with Cmd, Ctrl or Shift. The name is the real
 * link a keyboard reaches.
 *
 * Four lines, each of which says one thing (24 Nov 2026, at the operator's
 * request: it was "too boring", its middle made no sense and it was too much
 * small text):
 *
 * - **Which one**: the engine's mark, the name, and under it the product.
 *   The state is its dot; the word is said only when it is not the expected
 *   one ("stopped", "unreachable"), since "connected" on every card was a
 *   word nobody read. Where it runs follows only when it says more than the
 *   name: a container named as the connection was the name again in mono.
 * - **What it holds**: size, tables and sessions as figures in the body's
 *   size with their nouns small beside them, and what uses it as the marks
 *   of those things. The sessions' last hour, a line in the database's own
 *   hue beside "1 session open", went: a stepped line with no scale read as
 *   noise rather than a reading.
 * - **What is wrong, and the ways in**: the foot says what needs a hand —
 *   open to the internet, never backed up — and otherwise when it was last
 *   backed up, beside the engine's two work pages. "this server only" and
 *   the like went: they are the expected answer.
 *
 * There is no arrow at the head (the operator's word): the whole card and
 * its name open it, and its menu is words without glyphs.
 *
 * A stopped server keeps its card with its figures in the muted ink and
 * Start as its verb; one that is being started, stopped or dumped runs a
 * light round its edge and says so. A failure says what the server answered
 * where the figures would be, in two lines that hold the figures' height.
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
  const href = sectionHref(entry.id)
  const press = useChoicePress({ href })
  const busy = control.busyWord(entry)
  const status = fleetStatus(entry)
  const down = entry.state === "stopped" || entry.state === "paused"
  // A MongoDB that this kernel keeps from starting is a failure whether
  // Docker is restarting it or it has stopped: Start would not help it.
  const kernel = entry.kernelProblem
  const fix = control.mitigation(entry)
  const failed = (!entry.ok && !down) || Boolean(kernel)
  const place = placeBesides(entry)
  const has = (kind: Concern["kind"]) => concerns.some((concern) => concern.kind === kind)
  const pages = workPages(engine)
  const product = `${engine.label}${entry.versionNumber ? ` ${entry.versionNumber}` : ""}`
  const backedUp = backup?.newest
    ? { words: `backed up ${relativeTime(backup.newest)}`, kind: "stale-backup" as const }
    : has("never-backed-up")
      ? { words: "never backed up", kind: "never-backed-up" as const }
      : undefined
  const size = stored !== undefined ? byteParts(stored) : undefined

  return (
    // The slot is the one every lit row in the section carries: a card is a
    // row you take, drawn taller.
    <li className="min-w-0" data-slot="choice-row" data-card="database" data-state={entry.state}>
      {/* Each card lands a beat after the one before it, capped so a fleet
          of forty does not take three seconds. */}
      <BlurFade delay={Math.min(index, 11) * 0.04} className="h-full">
        <SpotlightBorder className="h-full">
          {busy && (
            <span aria-hidden className="pointer-events-none absolute -inset-px rounded-xl">
              <BorderBeam size={80} duration={4} />
            </span>
          )}
          {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions -- the pointer's larger target: the card's name is the link a keyboard opens it by */}
          <div
            onClick={press.onClick}
            className="group group/choice flex h-full min-w-0 cursor-pointer flex-col gap-3 rounded-xl p-4"
          >
            <div className="grid min-w-0 grid-cols-start-main-end items-center gap-x-3 gap-y-0.5">
              <EngineMark engine={engine} className={cn("row-span-2", down && "opacity-60")} />
              <Link
                href={href}
                aria-label={`Open ${entry.name}`}
                className="block max-w-full min-w-0 truncate rounded-sm text-title leading-tight font-semibold tracking-tight focus-ring"
              >
                {entry.name}
              </Link>
              <span className="-my-1 -mr-1.5 flex items-center gap-1">
                {entry.readOnly && <ProtectedTag compact className="mr-1" />}
                <VerbMenu verbs={control.verbsFor(entry)} label={`Actions for ${entry.name}`} />
              </span>
              <div className="col-span-2 col-start-2 flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
                {busy ? (
                  <TextShimmer className="shrink-0 text-xs font-medium">{`${busy}…`}</TextShimmer>
                ) : status.state === "running" ? (
                  // The expected state is its dot alone, named for a screen
                  // reader and the pointer.
                  <span
                    data-slot="database-status"
                    title={statusLabel(status.state)}
                    className="flex shrink-0"
                  >
                    <StatusDot tone={statusTone(status.state)} label={statusLabel(status.state)} />
                  </span>
                ) : (
                  <DatabaseStatusMark status={status} />
                )}
                {/* A hair of padding: flush with its text, a truncating box
                    clips the last glyph without earning the ellipsis. */}
                <span className="min-w-0 shrink truncate pr-0.5" title={product}>
                  {product}
                </span>
                {place && (
                  // Cut in the middle: the end of a path is the file's name.
                  <span
                    data-slot="fleet-place"
                    title={place.text}
                    className={cn("min-w-0 flex-1 truncate pr-0.5", place.mono && "font-mono")}
                  >
                    {truncateMiddle(place.text, 28)}
                  </span>
                )}
                <EnvironmentTag
                  environment={entry.environment}
                  className={cn("max-w-28", !place && "ml-auto")}
                />
              </div>
            </div>

            {failed ? (
              // What the server said, where its figures would be: a failure
              // is never drawn as three zeros. Two whole lines and no part of
              // a third, in the height the figures take.
              <div className="flex min-h-8 min-w-0 items-center">
                {kernel ? (
                  // The reason, not the refused dial: in words, with the
                  // sentence behind it for the pointer and in the dialog.
                  <p
                    data-slot="fleet-error"
                    title={kernel.detail}
                    className="line-clamp-2 min-w-0 text-hint leading-4 break-words text-destructive"
                  >
                    {kernel.message}
                  </p>
                ) : (
                  <p
                    data-slot="fleet-error"
                    title={status.error}
                    className="line-clamp-2 min-w-0 font-mono text-hint leading-4 break-words text-destructive"
                  >
                    {status.error || "The server did not answer."}
                  </p>
                )}
              </div>
            ) : (
              <div
                data-slot="fleet-reading"
                className="flex min-h-8 min-w-0 items-center justify-between gap-3"
              >
                {/* Figures that cannot all be read whole at this width are
                    cut from the end, a whole figure at a time. */}
                <p
                  data-slot="fleet-holds"
                  className="flex h-6 min-w-0 flex-wrap items-baseline gap-x-3.5 overflow-hidden"
                >
                  {size && (
                    <Figure
                      figure={size.value.toFixed(size.decimals)}
                      noun={size.unit}
                      muted={down}
                    />
                  )}
                  {!down && <Count n={entry.objects} nouns={objectNouns(entry.objectWord)} />}
                  {!down && <Count n={entry.sessions} nouns={sessionNouns(engine.kind)} />}
                  {down && !size && (
                    <span className="text-body text-muted-foreground">No reading</span>
                  )}
                </p>
                {feeds && feeds.count > 0 && (
                  <span
                    data-slot="fleet-feeds"
                    title={`Used by ${plural(feeds.count, "app")}`}
                    className="flex shrink-0 items-center gap-1.5 text-hint text-muted-foreground"
                  >
                    used by
                    {feeds.products.length > 0 ? (
                      <ProductGlyphs ids={feeds.products} max={3} />
                    ) : (
                      <span className="numeric">{feeds.count}</span>
                    )}
                  </span>
                )}
              </div>
            )}

            {/* What needs a hand, else the one fact that is good to know,
                beside the ways in: the card's last line is the same line on
                every card. A fact that does not fit is cut, and its whole
                sentence is in the card's own pages. */}
            <div className="mt-auto flex min-w-0 items-center justify-between gap-3 border-t border-hairline pt-3">
              <p
                data-slot="fleet-facts"
                className="flex h-5 min-w-0 items-center gap-x-3 overflow-hidden text-hint whitespace-nowrap text-muted-foreground [&>*]:min-w-0 [&>*]:truncate"
              >
                {has("public") && (
                  <span className={factTone(concernLevel(concerns, "public"))}>
                    open to the internet
                  </span>
                )}
                {backedUp && (
                  <span className={factTone(concernLevel(concerns, backedUp.kind))}>
                    {backedUp.words}
                  </span>
                )}
              </p>
              <span className="-my-1 -mr-1.5 flex shrink-0 items-center gap-0.5">
                {fix && !busy ? (
                  <Button
                    size="xs"
                    variant="outline"
                    className="text-foreground"
                    onClick={() => control.mitigate(entry, fix)}
                    aria-label={`Apply the kernel mitigation to ${entry.name}`}
                  >
                    <Wrench className="size-3" />
                    Apply mitigation
                  </Button>
                ) : control.canStart(entry) && !busy && !kernel ? (
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
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}

/**
 * One reading on a card: the figure a step up from the body, its noun small
 * beside it, so three of them read as "8.9 MB  16 tables  1 session" at a
 * glance rather than as a sentence to parse. A figure too long for the
 * card's width is said compactly, and whole in its title.
 */
function Figure({
  figure,
  noun,
  title,
  muted,
}: {
  figure: string
  noun: string
  title?: string
  muted?: boolean
}) {
  return (
    <span className="numeric shrink-0 whitespace-nowrap" title={title}>
      <span
        className={cn(
          "text-title font-semibold tracking-tight",
          muted ? "text-muted-foreground" : "text-foreground",
        )}
      >
        {figure}
      </span>{" "}
      <span className="text-hint text-muted-foreground">{noun}</span>
    </span>
  )
}

/** A count and its noun, said once and as many, the figure compact past five digits. */
function Count({ n, nouns }: { n: number; nouns: Nouns }) {
  const short = compactCount(n, { exactBelow: 100_000 })
  return (
    <Figure
      figure={short}
      noun={nounFor(n, nouns)}
      title={short === grouped(n) ? undefined : `${grouped(n)} ${nounFor(n, nouns)}`}
    />
  )
}
