"use client"

import { useState } from "react"
import { ChevronDown, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { ChangeKind, Release } from "@/lib/types"
import { BlurFade } from "@/components/ui/blur-fade"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { ChangeLabel, KIND_STYLE, releaseDay } from "@/components/update/release-notes"

/**
 * The dashboard's history, as a timeline.
 *
 * It was a stack of release notes with a hairline between them: a 15px version
 * over a paragraph over every change of every release, so 0.7.0's fourteen
 * changes and their details were one wall and the version it belonged to had
 * scrolled away by the third. The shape here is the changelog timeline most
 * products converged on — the version held in a column of its own that stays
 * beside its release while that release is read, a rail down the page with a
 * mark where each release sits on it, and the notes to the right at a measure
 * a line can be read at.
 *
 * - **The version is the landmark.** It is set at the headline figure's 24px
 *   (§8), because in a history the number is the thing found without reading,
 *   and it is `sticky` at `lg` so a long release keeps its name in view.
 * - **The mark says where you are, not what happened.** Filled brand is the
 *   version this dashboard is running (§3's location); an outlined brand ring
 *   is a release published after it, waiting; the rest are the past, muted.
 *   None carries a character — that is §4's pill.
 * - **A release is scanned before it is read.** Under the title, what it holds
 *   by kind — "6 new · 3 fixed · 1 security" — in the kinds' own colours, then
 *   the lines. Past the newest two releases the list folds after its first
 *   three changes; a search opens every one, because a hit behind a fold is a
 *   hit nobody sees.
 *
 * Aceternity's and Magic UI's timelines were the references, and neither was
 * imported: both draw the rail as a scroll-driven gradient beam, which is a
 * motion §11 does not have and a gradient §15 does not allow. What they are
 * right about — the sticky label and the rail — is layout, and is here.
 */
export function ReleaseTimeline({
  releases,
  installed,
  newer,
  expanded,
  className,
}: {
  releases: Release[]
  /** The version this dashboard is running. */
  installed?: string
  /** Versions published after it, which the check turned up. */
  newer: Set<string>
  /** Open every release — a search is running over them. */
  expanded?: boolean
  className?: string
}) {
  return (
    <div className={cn("min-w-0", className)}>
      {releases.map((release, index) => (
        <BlurFade key={release.version} delay={Math.min(index, 6) * 0.04}>
          <Entry
            release={release}
            installed={release.version === installed}
            newer={newer.has(release.version)}
            latest={index === 0 && newer.size > 0}
            open={expanded || index < 2 || newer.has(release.version)}
            last={index === releases.length - 1}
          />
        </BlurFade>
      ))}
    </div>
  )
}

/** How many of a release's changes are shown before it folds. */
const FOLD = 3

const KIND_ORDER: ChangeKind[] = ["security", "added", "changed", "fixed", "deprecated", "removed"]

function Entry({
  release,
  installed,
  newer,
  latest,
  open,
  last,
}: {
  release: Release
  installed: boolean
  newer: boolean
  latest: boolean
  open: boolean
  last: boolean
}) {
  const [unfolded, setUnfolded] = useState(false)
  const all = open || unfolded || release.changes.length <= FOLD + 1
  const shown = all ? release.changes : release.changes.slice(0, FOLD)
  const counts = KIND_ORDER.map((kind) => ({
    kind,
    n: release.changes.filter((change) => change.kind === kind).length,
  })).filter((count) => count.n > 0)

  return (
    <article
      id={`release-${release.version}`}
      className="grid min-w-0 scroll-mt-6 gap-x-10 lg:grid-cols-[11rem_minmax(0,1fr)]"
    >
      <header className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 pb-3 lg:sticky lg:top-6 lg:flex-col lg:items-start lg:self-start lg:pb-0">
        <h3
          className={cn(
            "numeric text-2xl leading-tight font-semibold tracking-tight",
            !installed && !newer && "text-foreground/80",
          )}
        >
          {release.version}
        </h3>
        <time dateTime={release.date} className="text-hint text-muted-foreground">
          {releaseDay(release.date)}
        </time>
        <span className="flex flex-wrap gap-x-3 gap-y-1 lg:mt-1">
          {installed && <Tag>Installed</Tag>}
          {latest && <Tag>Latest</Tag>}
          {release.breaking && (
            <Tag tone="warning" icon={Warning}>
              Needs attention
            </Tag>
          )}
        </span>
      </header>

      <section
        aria-label={`Release ${release.version}`}
        className={cn(
          "relative min-w-0 border-l border-hairline pl-6 sm:pl-8",
          last ? "pb-2" : "pb-14",
        )}
      >
        <Mark installed={installed} newer={newer} />

        <div className="max-w-2xl space-y-2">
          <p className="text-base leading-snug font-semibold tracking-tight">{release.title}</p>
          {release.summary && (
            <p className="text-body leading-relaxed text-muted-foreground">{release.summary}</p>
          )}
          {counts.length > 0 && (
            <p className="flex flex-wrap gap-x-3 gap-y-1 pt-0.5 text-xs">
              {counts.map(({ kind, n }) => (
                <span key={kind} className="flex items-baseline gap-1">
                  <span className="numeric text-foreground">{n}</span>
                  <span className={KIND_STYLE[kind]?.className}>
                    {(KIND_STYLE[kind]?.label ?? kind).toLowerCase()}
                  </span>
                </span>
              ))}
            </p>
          )}
        </div>

        {/* A breaking release is never folded away behind a summary: the whole
            point of the flag is that the operator has something to do by hand,
            and a warning they have to expand to read is a warning they will
            discover afterwards. */}
        {release.breaking && release.breakingNote && (
          <Notice title="Before you update" tone="warning" className="mt-4 max-w-2xl">
            {release.breakingNote}
          </Notice>
        )}

        <ul className="mt-5 max-w-3xl space-y-3.5">
          {shown.map((change, i) => (
            <li key={i} className="flex gap-4">
              <span className="w-16 shrink-0">
                <ChangeLabel kind={change.kind} />
              </span>
              <span className="min-w-0 flex-1 space-y-1">
                <span className="block text-body leading-snug font-medium">{change.text}</span>
                {change.detail && (
                  <span className="block text-xs leading-relaxed text-muted-foreground">
                    {change.detail}
                  </span>
                )}
              </span>
            </li>
          ))}
        </ul>

        {!all && (
          <button
            type="button"
            onClick={() => setUnfolded(true)}
            className="mt-3 -ml-2 inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground focus-ring transition-colors hover:bg-row-hover hover:text-foreground"
          >
            <ChevronDown aria-hidden className="size-3.5" />
            {release.changes.length - FOLD} more{" "}
            {release.changes.length - FOLD === 1 ? "change" : "changes"}
          </button>
        )}
      </section>
    </article>
  )
}

/** Where a release sits on the rail: running, waiting, or past. */
function Mark({ installed, newer }: { installed: boolean; newer: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        // Centred on the one-pixel rule, on the line of the release's title.
        "absolute top-1.5 -left-[5.5px] size-2.5 rounded-full ring-4 ring-background",
        installed
          ? "bg-brand"
          : newer
            ? "border-2 border-brand bg-background"
            : "bg-muted-foreground/50",
      )}
    />
  )
}
