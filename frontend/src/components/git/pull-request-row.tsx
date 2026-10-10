"use client"

import { External } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { previewStatus } from "@/lib/pull-requests"
import type { DeploymentPreview, GitPullRequest } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ChoiceRow } from "@/components/flow"
import { BranchChip, ForgeFace } from "@/components/git/marks"
import { ChecksMark, CommentCount, PullStateMark, ReviewMark } from "@/components/git/pull-state"
import { IconAction } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { VerbActions, type Verb } from "@/components/verbs"

/** The preview's address, in a tab of its own: a tailnet URL is another origin. */
export function openPreview(preview: DeploymentPreview) {
  if (preview.address?.url) window.open(preview.address.url, "_blank", "noopener")
}

/**
 * One pull request among a repository's, as a choice.
 *
 * The same row on the Git list page, under its checkout, and on a project's
 * Overview, under the repository it deploys — because both lists answer the
 * same question, *which of these is worth testing*, and a reader moving
 * between the two pages should not have to learn the answer's shape twice.
 * Who opened it and whether it is open, merged or closed — the state glyph a
 * forge prints in front of the title, in its colour — what it is, where it
 * goes, how its checks stand and where its preview is; the verbs in the
 * actions slot, declared by the caller,
 * since which of them a surface may offer depends on what the surface knows.
 *
 * A `ChoiceRow` rather than the workspace's compact pull-request line: on
 * these two pages the row is a destination, and the lit edge belongs to
 * things you pick (§16). The workspace's GitHub tab keeps its own row, which
 * has the pressed state a choice does not.
 */
export function PullRequestRow({
  pull,
  preview,
  verbs,
  onOpen,
  verb,
  compact,
  index,
  className,
}: {
  pull: GitPullRequest
  /** The preview built from it, where the caller holds one apart from `pull.preview`. */
  preview?: DeploymentPreview | null
  verbs?: Verb[]
  /** What the press does. Left out, the row opens the pull request on GitHub. */
  onOpen?: () => void
  /** The accessible name of the title — "Open pull request #7" unless the press does something else. */
  verb?: string
  /**
   * One line, the size of a reading in a card: for the foot of a checkout's
   * card on the list page, which it shares with the repository's name and
   * must not outrank.
   */
  compact?: boolean
  index?: number
  className?: string
}) {
  const built = preview ?? pull.preview
  const status = previewStatus(built)
  const reachable = Boolean(built && built.state === "open" && built.address?.published)
  const when = pull.updatedAt ?? pull.createdAt

  const trailing = (
    <>
      {pull.draft && <Tag>draft</Tag>}
      {pull.fork && <Tag tone="warning">fork</Tag>}
      {!compact && (
        <span className="max-md:hidden">
          <ReviewMark review={pull.review} />
        </span>
      )}
      {!compact && <CommentCount count={pull.comments} />}
      <ChecksMark checks={pull.checks} label={!compact} />
      {status && <Status className="text-hint" tone={status.tone} label={status.label} />}
      {compact && <ForgeFace login={pull.author} provider="github" size="xs" />}
      {reachable &&
        built &&
        (compact ? (
          <IconAction label="Open preview" onClick={() => openPreview(built)}>
            <External />
          </IconAction>
        ) : (
          <Button size="xs" variant="ghost" onClick={() => openPreview(built)}>
            <External />
            Open preview
          </Button>
        ))}
    </>
  )

  return (
    <ChoiceRow
      index={index}
      className={cn(compact && "min-h-10 px-2.5 py-1.5", className)}
      leading={
        compact ? (
          <PullStateMark pull={pull} />
        ) : (
          // The author's face, with the request's state in the tile's corner
          // the way a run's state sits on its step's tile.
          <span className="relative shrink-0">
            <ForgeFace login={pull.author} provider="github" size="sm" />
            <span className="absolute -right-1 -bottom-1 rounded-sm bg-(--panel-ground,var(--background)) p-px">
              <PullStateMark pull={pull} className="size-3.5" />
            </span>
          </span>
        )
      }
      title={
        <>
          <span className="numeric text-muted-foreground">#{pull.number}</span> {pull.title}
        </>
      }
      verb={verb ?? `Open pull request #${pull.number}`}
      description={
        compact ? undefined : (
          <span className="inline-flex max-w-full items-center gap-1.5">
            <BranchChip branch={pull.head} className="max-w-[12rem]" />
            <span aria-hidden>→</span>
            <BranchChip branch={pull.base} className="max-w-[10rem]" />
            {pull.author && <span className="truncate">· {pull.author}</span>}
            {when && <span className="shrink-0">· {relativeTime(when)}</span>}
          </span>
        )
      }
      trailing={trailing}
      actions={
        verbs && verbs.length > 0 ? (
          <VerbActions verbs={verbs} dim menuLabel={`More actions for #${pull.number}`} />
        ) : undefined
      }
      onSelect={onOpen ?? (() => window.open(pull.url, "_blank", "noopener"))}
    />
  )
}
