"use client"

import Link from "next/link"
import { ArrowRight, Globe, Key, LockClosed } from "@/components/icons"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { SidePanel } from "@/components/side-panel"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { ShortSha } from "@/components/git/marks"
import { Button } from "@/components/ui/button"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  DeploymentRelease,
  ReleaseComparisonResponse,
  ReleaseListChange,
  ReleaseUpdateStatus,
} from "@/lib/types"
import { CHANGE_TONE, sentence, shortIdentity, shortRevision } from "@/components/deploy/vocabulary"

const UPDATE_TEXT: Record<NonNullable<ReleaseUpdateStatus["state"]>, string> = {
  current: "The upstream tag still resolves to the digest this release pinned.",
  outdated: "The upstream tag now points at a different image. Deploying again would pick it up.",
  unknown: "The registry could not be asked about this tag.",
  local: "This release was built here. There is no upstream tag to compare against.",
  pinned: "This release names its image by digest, so the upstream tag cannot move under it.",
}

/** The upstream image's state, as the state it is (§4): a dot and the reader's words. */
const UPDATE_STATUS: Record<
  NonNullable<ReleaseUpdateStatus["state"]>,
  { tone: DotTone; label: string }
> = {
  current: { tone: "running", label: "up to date" },
  outdated: { tone: "warning", label: "newer image upstream" },
  unknown: { tone: "unknown", label: "unknown" },
  local: { tone: "stopped", label: "built here" },
  pinned: { tone: "running", label: "pinned by digest" },
}

/** The letter git prints for a changed file, for a changed setting. */
const LETTER: Record<ReleaseListChange["change"], string> = {
  added: "A",
  removed: "D",
  changed: "M",
  unchanged: "",
}

/** The diff view's own wash behind an added or a removed value (`files/diff-view.tsx`). */
const WASH: Partial<Record<ReleaseListChange["change"], string>> = {
  added: "color-mix(in oklab, var(--git-added) 9%, transparent)",
  removed: "color-mix(in oklab, var(--git-deleted) 9%, transparent)",
}

type Change = {
  key: string
  name: React.ReactNode
  change: ReleaseListChange["change"]
  from?: string
  to?: string
  secret?: boolean
  /** A literal from the host — a variable's or a check's name — rather than a word. */
  mono?: boolean
  mark?: React.ReactNode
}

/**
 * What actually changed between a release and the one it is compared with,
 * and whether the artifacts a rollback would need are still on disk.
 * Variables appear by name and digest; their values are never read here.
 *
 * Read the way a diff is read: each change leads with the letter git prints
 * for a file — A, M, D in the `--git-*` hues, because a removed variable is a
 * change and not a failure (§3) — and its value is the old one struck through
 * beside the new one, on the diff view's own wash. A part of the release that
 * did not change is not drawn as a section saying so; one closing line names
 * them all. The releases are named as every other surface names them, by
 * number.
 *
 * A `SidePanel` of plain sections rather than the framed panel the pre-rebuild
 * `DeploymentReleaseComparison` drew — it is already a detail surface with its
 * own frame, so the sections inside it stay unframed (design system §7).
 */
export function ReleaseComparisonSheet({
  open,
  onOpenChange,
  projectId,
  environmentId,
  releaseId,
  fromReleaseId,
  releases = [],
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  environmentId: number
  /** The newer of the two releases — usually the live one. */
  releaseId?: number
  /** Overrides the server's default predecessor, for "compare with live" from an older row. */
  fromReleaseId?: number
  /** The environment's releases, to name the two by number and commit. */
  releases?: DeploymentRelease[]
}) {
  const result = usePoll(
    (signal) =>
      get<ReleaseComparisonResponse>(
        `/deploy/${projectId}/environments/${environmentId}/releases/${releaseId}/comparison`,
        fromReleaseId ? { from: fromReleaseId } : undefined,
        signal,
      ),
    0,
    [projectId, environmentId, releaseId, fromReleaseId],
    { enabled: open && Boolean(releaseId) },
  )
  const comparison = result.data?.comparison
  const detail = comparison?.detail
  const from = releases.find((release) => release.id === comparison?.fromReleaseId)
  const to = releases.find((release) => release.id === comparison?.toReleaseId)

  const sections: { title: string; changes: Change[] }[] = detail
    ? [
        {
          title: "Runtime and source",
          changes: detail.fields
            .filter((field) => field.changed)
            .map((field) => {
              // A revision reads as the header draws the two releases: short.
              const revision = field.field === "source revision"
              return {
                key: field.field,
                name: sentence(field.field),
                change: "changed" as const,
                from: revision ? shortRevision(field.from) : field.from,
                to: revision ? shortRevision(field.to) : field.to,
              }
            }),
        },
        { title: "Variables", changes: listChanges(detail.variables, true) },
        { title: "Dependencies", changes: listChanges(detail.dependencies, false) },
        { title: "Checks", changes: listChanges(detail.checks, true) },
        {
          title: "Domains",
          changes: listChanges(detail.domains, true).map((change) => {
            const Glyph = /https/.test(change.to ?? change.from ?? "") ? LockClosed : Globe
            return {
              ...change,
              mark: <Glyph aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />,
            }
          }),
        },
      ]
    : []
  const changed = sections.filter((section) => section.changes.length > 0)
  const unchanged = sections.filter((section) => section.changes.length === 0)

  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="What changed"
      description="Runtime, source, variables, dependencies, checks and domains compared between two releases."
      width="lg"
      actions={
        comparison && (
          <span
            title={`Release ${from ? `#${from.number}` : comparison.fromReleaseId} compared with release ${to ? `#${to.number}` : comparison.toReleaseId}`}
            className="numeric flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body font-medium"
          >
            <ReleaseName release={from} id={comparison.fromReleaseId} />
            <ArrowRight aria-hidden className="size-3.5 text-muted-foreground" />
            <span className="sr-only">compared with</span>
            <ReleaseName release={to} id={comparison.toReleaseId} />
          </span>
        )
      }
    >
      {result.error ? (
        <ErrorState error={result.error} />
      ) : !releaseId ? (
        // The poll is disabled with no releaseId, so `!result.data` below
        // would never resolve to anything but a skeleton — this project has
        // no live release to compare yet, not a slow fetch.
        <EmptyNote>There is no live release to compare yet.</EmptyNote>
      ) : !result.data ? (
        <LoadingRows />
      ) : (
        <div
          key={`${comparison?.fromReleaseId}:${comparison?.toReleaseId}`}
          className="animate-rise space-y-6"
        >
          <UpdateEvidence update={result.data.update} />
          {!comparison ? (
            <EmptyNote className="px-0 text-left">
              {result.data.reason ?? "There is nothing to compare."}
            </EmptyNote>
          ) : detail?.status !== "available" ? (
            <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
              <Status tone="unknown" label="Release detail unavailable" />
              {detail?.reason ?? "These releases carry no readable runtime snapshot."}
            </p>
          ) : (
            <>
              {changed.map((section) => (
                <ChangeSection key={section.title} {...section} />
              ))}
              {unchanged.length > 0 && (
                <p className="text-hint text-muted-foreground">
                  {changed.length === 0 ? "Nothing changed: " : "Unchanged: "}
                  {unchanged.map((section) => section.title.toLowerCase()).join(", ")}
                </p>
              )}
            </>
          )}
          <RetainedArtifacts artifacts={result.data.artifacts} />
        </div>
      )}
    </SidePanel>
  )
}

function listChanges(changes: ReleaseListChange[], mono: boolean): Change[] {
  // A secret's value is its digest, which the server marks with its
  // sensitivity; the row's tag already says it, once.
  const value = (text: string | undefined, secret?: boolean) =>
    secret ? text?.replace(/ · secret$/, "") : text
  return changes
    .filter((change) => change.change !== "unchanged")
    .map((change) => ({
      key: change.name,
      name: change.name,
      change: change.change,
      from: value(change.from, change.secret),
      to: value(change.to, change.secret),
      secret: change.secret,
      mono,
    }))
}

/**
 * The source a release was built from, short: a commit as its sha, and an
 * image's digest — which is what an image, Compose or template release records
 * as its revision — as the digest's own short form rather than a commit's
 * seven characters, which would leave only "sha256:".
 */
export function ReleaseRevision({ revision }: { revision?: string }) {
  if (revision?.startsWith("sha256:")) {
    return <span className="shrink-0 font-mono">{shortIdentity(revision)}</span>
  }
  return <ShortSha sha={shortRevision(revision)} />
}

/** "#2 a12bc34": a release by the number every surface calls it, with its commit. */
function ReleaseName({ release, id }: { release?: DeploymentRelease; id: number }) {
  if (!release) return <span>release {id}</span>
  return (
    <span className="inline-flex items-center gap-1.5">
      <span>#{release.number}</span>
      <ReleaseRevision revision={release.sourceRevision} />
    </span>
  )
}

function ChangeSection({ title, changes }: { title: string; changes: Change[] }) {
  return (
    <section className="min-w-0 space-y-1" aria-label={title}>
      <div className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline pb-2">
        <h3 className="text-title leading-tight font-semibold tracking-tight">{title}</h3>
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {changes.length} changed
        </span>
      </div>
      <ul className="divide-y divide-hairline">
        {changes.map((change) => (
          <ChangeRow key={change.key} change={change} />
        ))}
      </ul>
    </section>
  )
}

function ChangeRow({ change }: { change: Change }) {
  const tone = CHANGE_TONE[change.change]
  const colour = tone ? `var(--git-${tone})` : undefined
  return (
    <li className="min-w-0 space-y-1.5 py-2.5">
      <div className="flex min-w-0 items-center gap-2">
        <span
          title={change.change}
          className="w-3 shrink-0 font-mono text-xs font-semibold"
          style={{ color: colour }}
        >
          {LETTER[change.change]}
        </span>
        {change.mark}
        <span
          className={cn(
            "min-w-0 flex-1 truncate",
            change.mono ? "font-mono text-xs" : "text-body font-medium",
          )}
        >
          {change.name}
        </span>
        {change.secret && <Tag icon={Key}>digest only</Tag>}
      </div>
      <p
        className="ml-5 rounded-sm px-1.5 py-0.5 font-mono text-hint wrap-anywhere"
        style={{ background: WASH[change.change] ?? "var(--surface-sunken)" }}
      >
        <Value
          value={change.from}
          className="text-(--git-deleted) line-through decoration-(--git-deleted)/50"
        />
        <span className="text-muted-foreground"> → </span>
        <Value value={change.to} className="text-(--git-added)" />
      </p>
    </li>
  )
}

/** One side of a change; a side that did not exist says so in the page's own quiet. */
function Value({ value, className }: { value?: string; className: string }) {
  if (!value) return <span className="text-muted-foreground italic">none</span>
  return <span className={className}>{value}</span>
}

/**
 * Whether the image this release runs still matches its tag upstream, drawn as
 * the product the image is with its state beside it. A reading, so a row
 * rather than a fenced group.
 */
function UpdateEvidence({ update }: { update: ReleaseUpdateStatus }) {
  const status = update.state && UPDATE_STATUS[update.state]
  return (
    <section aria-label="Upstream image" className="flex min-w-0 items-start gap-3">
      <ProductLogo id={update.reference ? imageProduct(update.reference) : undefined} size="sm" />
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <span className="min-w-0 truncate font-mono text-xs">
            {update.reference ?? "Upstream image"}
          </span>
          {update.status !== "available" || !status ? (
            <Status tone="unknown" label="Update status unavailable" />
          ) : (
            <Status tone={status.tone} label={status.label} />
          )}
        </div>
        {update.status === "available" && (update.localDigest || update.checkedAt) && (
          <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-hint text-muted-foreground">
            {update.localDigest && (
              <>
                local <span className="font-mono">{shortIdentity(update.localDigest)}</span>
              </>
            )}
            {update.remoteDigest && update.remoteDigest !== update.localDigest && (
              <>
                {" → upstream "}
                <span className="font-mono">{shortIdentity(update.remoteDigest)}</span>
              </>
            )}
            {update.checkedAt && ` · checked ${relativeTime(update.checkedAt)}`}
          </p>
        )}
        <p className="text-hint text-muted-foreground">
          {update.status !== "available" || !update.state
            ? (update.reason ?? "Docker could not be asked whether a newer image exists.")
            : update.reason || UPDATE_TEXT[update.state]}
        </p>
      </div>
    </section>
  )
}

function RetainedArtifacts({ artifacts }: { artifacts: ReleaseComparisonResponse["artifacts"] }) {
  return (
    <section className="min-w-0 space-y-1" aria-label="Retained artifacts">
      <div className="flex min-w-0 items-center justify-between gap-3 border-b border-hairline pb-2">
        <h3 className="text-title leading-tight font-semibold tracking-tight">
          Retained artifacts
        </h3>
        <Button variant="ghost" size="xs" asChild>
          <Link href="/docker/images">
            Docker images <ArrowRight className="size-3" />
          </Link>
        </Button>
      </div>
      {artifacts.length === 0 ? (
        <p className="py-2 text-hint text-muted-foreground">This release recorded no artifacts.</p>
      ) : (
        <ul className="divide-y divide-hairline">
          {artifacts.map((artifact) => (
            <li
              key={`${artifact.kind}:${artifact.reference}:${artifact.digest}`}
              className="flex min-w-0 items-start gap-3 py-2.5"
            >
              <ProductLogo id={imageProduct(artifact.reference)} size="sm" />
              <div className="min-w-0 flex-1 space-y-0.5">
                <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="min-w-0 truncate font-mono text-xs">{artifact.reference}</span>
                  <Tag>{artifact.kind}</Tag>
                  <Status
                    tone={artifact.retained ? "running" : "warning"}
                    label={artifact.retained ? "retained" : "prunable"}
                  />
                  {artifact.sizeBytes > 0 && (
                    <span className="numeric ml-auto text-hint text-muted-foreground">
                      {bytes(artifact.sizeBytes)}
                    </span>
                  )}
                </div>
                <p className="text-hint text-muted-foreground">{artifact.reason}</p>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
