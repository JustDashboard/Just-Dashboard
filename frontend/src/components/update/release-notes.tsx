"use client"

import { Warning } from "@/components/icons"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { cn } from "@/lib/utils"
import type { ChangeKind, Release } from "@/lib/types"

/**
 * A release, rendered.
 *
 * The changelog arrives as data rather than prose (see internal/selfupdate for
 * why), and this is what that buys: a kind against every line, in a column of
 * its own, so a release is scanned rather than read. Somebody deciding whether
 * to upgrade a root-equivalent panel on their own server is looking for two
 * things — did anything break, and is there a security fix — and both are
 * answerable at a glance from the left-hand column alone.
 *
 * The kinds are a closed set on the server, so there is no "unknown kind"
 * branch here beyond the neutral fallback: a release inventing a seventh kind
 * fails the Go test suite before it can render as an unstyled grey row.
 */

const KIND_STYLE: Record<ChangeKind, { label: string; className: string }> = {
  added: { label: "New", className: "text-[var(--tag-green)]" },
  changed: { label: "Changed", className: "text-[var(--tag-blue)]" },
  fixed: { label: "Fixed", className: "text-[var(--tag-cyan)]" },
  removed: { label: "Removed", className: "text-[var(--tag-slate)]" },
  security: { label: "Security", className: "text-[var(--tag-red)]" },
  deprecated: { label: "Ageing", className: "text-[var(--tag-amber)]" },
}

function ChangeLabel({ kind }: { kind: ChangeKind }) {
  const style = KIND_STYLE[kind] ?? { label: kind, className: "text-muted-foreground" }
  return (
    <span
      className={cn("eyebrow shrink-0 pt-0.5 text-micro leading-4 tracking-wider", style.className)}
    >
      {style.label}
    </span>
  )
}

/** A calendar day as a person writes one. The value is YYYY-MM-DD, not a time. */
function releaseDay(date: string): string {
  const parsed = new Date(`${date}T00:00:00Z`)
  if (Number.isNaN(parsed.getTime())) return date
  return parsed.toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  })
}

export function ReleaseNotes({
  release,
  installed,
  className,
}: {
  release: Release
  /** Marks the version the reader is already running. */
  installed?: boolean
  className?: string
}) {
  return (
    <section className={cn("space-y-2.5", className)}>
      <header className="space-y-1">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <h3 className="numeric text-title font-semibold tracking-tight">{release.version}</h3>
          <span className="text-hint text-muted-foreground">{releaseDay(release.date)}</span>
          {installed && <Tag>Installed</Tag>}
          {release.breaking && (
            <Tag tone="warning" icon={Warning}>
              Needs attention
            </Tag>
          )}
        </div>
        <p className="text-body font-medium">{release.title}</p>
        {release.summary && (
          <p className="text-body leading-relaxed text-muted-foreground">{release.summary}</p>
        )}
      </header>

      {/* A breaking release is never folded away behind a summary: the whole
          point of the flag is that the operator has something to do by hand,
          and a warning they have to expand to read is a warning they will
          discover afterwards. */}
      {release.breaking && release.breakingNote && (
        <Notice title="Before you update" tone="warning">
          {release.breakingNote}
        </Notice>
      )}

      <ul className="space-y-2">
        {release.changes.map((change, i) => (
          <li key={i} className="flex gap-3">
            <span className="w-14 shrink-0">
              <ChangeLabel kind={change.kind} />
            </span>
            <span className="min-w-0 flex-1 space-y-0.5">
              <span className="block text-body leading-snug">{change.text}</span>
              {change.detail && (
                <span className="block text-xs leading-relaxed text-muted-foreground">
                  {change.detail}
                </span>
              )}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

/** Several releases with a rule between them, newest first. */
export function ReleaseList({
  releases,
  installed,
  className,
}: {
  releases: Release[]
  installed?: string
  className?: string
}) {
  return (
    <div className={cn("divide-y divide-hairline", className)}>
      {releases.map((release) => (
        <div key={release.version} className="py-4 first:pt-0 last:pb-0">
          <ReleaseNotes release={release} installed={release.version === installed} />
        </div>
      ))}
    </div>
  )
}
