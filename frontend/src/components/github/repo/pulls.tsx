"use client"

import { useState } from "react"
import { ArrowLeft, CheckCircle, Clock, CrossCircle, GitMerge, Slash } from "@/components/icons"
import { ChipStrip, FilterChip } from "@/components/chips"
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { Field } from "@/components/form"
import { ForgeFace } from "@/components/git/marks"
import { PullStateMark, PullStateWord } from "@/components/git/pull-state"
import { Markdown } from "@/components/markdown"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Segments } from "@/components/segments"
import { EmptyNote, ErrorState } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { LoadingRows } from "@/components/loading"
import {
  AbilityButton,
  LabelTag,
  OnGitHub,
  ShowMore,
  useOpenParam,
  usePages,
  useRepo,
} from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { errorMessage, get, post } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  GitHubCheck,
  GitHubMergeRequest,
  GitHubMergeResult,
  GitHubPull,
  GitHubPullDetail,
  GitHubPullPage,
} from "@/lib/github-types"

type PullFilter = "open" | "closed" | "merged" | "all"
const FILTERS: { key: PullFilter; label: string }[] = [
  { key: "open", label: "Open" },
  { key: "closed", label: "Closed" },
  { key: "merged", label: "Merged" },
  { key: "all", label: "All" },
]

const asPull = (pull: GitHubPull) => ({
  state: pull.state,
  draft: pull.draft,
  merged: pull.state === "merged",
})

/**
 * The repository's pull requests by state, each a card that opens the
 * request in place of the list (`&pull=<n>` in the address, so Back returns
 * to the list and a link opens the request).
 */
export function RepoPulls() {
  const [open, setOpen] = useOpenParam("pull")
  const number = open && /^\d+$/.test(open) ? Number(open) : null
  if (number !== null) return <PullDetail number={number} onBack={() => setOpen(null)} />
  return <PullList onOpen={(n) => setOpen(String(n))} />
}

function PullList({ onOpen }: { onOpen: (number: number) => void }) {
  const { path } = useRepo()
  const [filter, setFilter] = useState<PullFilter>("open")
  const pages = usePages<GitHubPull, GitHubPullPage>(
    path("/pulls"),
    { state: filter },
    (p) => p.pulls,
  )
  return (
    <Panel plain aria-label="Pull requests">
      <PanelHeader title="Pull requests" />
      <PanelBody flush className="space-y-3">
        <ChipStrip aria-label="Which pull requests">
          {FILTERS.map((f) => (
            <FilterChip key={f.key} selected={filter === f.key} onClick={() => setFilter(f.key)}>
              {f.label}
            </FilterChip>
          ))}
        </ChipStrip>
        {pages.error && pages.items.length === 0 ? (
          <ErrorState error={pages.error} onRetry={pages.reload} retryable />
        ) : pages.loading && pages.items.length === 0 ? (
          <ChoiceListSkeleton rows={4} />
        ) : pages.items.length === 0 ? (
          <EmptyNote>
            {filter === "all" ? "No pull requests yet." : `No ${filter} pull requests.`}
          </EmptyNote>
        ) : (
          <ChoiceList data-slot="github-pulls">
            {pages.items.map((pull) => (
              <ChoiceRow
                key={pull.number}
                leading={<PullStateMark pull={asPull(pull)} />}
                verb={`Open pull request #${pull.number}`}
                title={
                  <>
                    <span className="numeric text-muted-foreground">#{pull.number}</span>{" "}
                    {pull.title}
                  </>
                }
                description={
                  <>
                    {pull.author} ·{" "}
                    <span className="font-mono">
                      {pull.head.ref} → {pull.base.ref}
                    </span>{" "}
                    · <span className="numeric">{relativeTime(pull.updatedAt)}</span>
                  </>
                }
                trailing={
                  <span className="flex items-center gap-1.5 max-sm:hidden">
                    {pull.draft && <Tag>draft</Tag>}
                    {pull.labels.slice(0, 2).map((label) => (
                      <LabelTag key={label.name} label={label} />
                    ))}
                  </span>
                }
                onSelect={() => onOpen(pull.number)}
              />
            ))}
          </ChoiceList>
        )}
        <ShowMore pages={pages} />
      </PanelBody>
    </Panel>
  )
}

/** Back to a view's list, from a request or an issue opened in its place. */
export function BackToList({ label, onBack }: { label: string; onBack: () => void }) {
  return (
    <button
      type="button"
      onClick={onBack}
      className="inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
    >
      <ArrowLeft aria-hidden className="size-3" />
      {label}
    </button>
  )
}

function PullDetail({ number, onBack }: { number: number; onBack: () => void }) {
  const { repo, path, confirm, refresh } = useRepo()
  const read = usePoll(
    (signal) => get<GitHubPullDetail>(path(`/pulls/${number}`), undefined, signal),
    0,
    [repo.fullName, number],
  )
  const [merging, setMerging] = useState(false)
  const pull = read.data
  if (!pull) {
    return (
      <div className="space-y-4">
        <BackToList label="Pull requests" onBack={onBack} />
        {read.error ? (
          <ErrorState error={read.error} onRetry={read.refresh} retryable />
        ) : (
          <LoadingRows />
        )}
      </div>
    )
  }
  const open = pull.state === "open"
  const mergeHeld = pull.draft
    ? "A draft cannot be merged. Mark it ready on GitHub first."
    : pull.mergeable === false
      ? "GitHub says it cannot be merged as it stands: resolve its conflicts first."
      : pull.mergeMethods.length === 0
        ? "This repository allows no merge method."
        : undefined
  const setState = (state: "open" | "closed") =>
    confirm({
      title: state === "closed" ? `Close #${pull.number}` : `Reopen #${pull.number}`,
      confirmLabel: state === "closed" ? "Close" : "Reopen",
      description:
        state === "closed" ? (
          <p>Closes the pull request without merging it. It can be reopened.</p>
        ) : (
          <p>Reopens the pull request for review and merging.</p>
        ),
      action: async () => {
        await post<GitHubPull>(path(`/pulls/${pull.number}/state`), { state })
        read.refresh()
      },
    })
  return (
    <div className="flex min-w-0 flex-col gap-6 pb-6" data-slot="github-pull">
      <BackToList label="Pull requests" onBack={onBack} />
      <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h2 className="text-base font-semibold">
            <span className="numeric text-muted-foreground">#{pull.number}</span> {pull.title}
          </h2>
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-hint text-muted-foreground">
            <PullStateWord pull={asPull(pull)} />
            <span>
              {pull.author} wants <span className="font-mono text-foreground">{pull.head.ref}</span>{" "}
              in <span className="font-mono text-foreground">{pull.base.ref}</span>
            </span>
            <span className="numeric" title={timestamp(pull.updatedAt)}>
              updated {relativeTime(pull.updatedAt)}
            </span>
          </p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {open && (
            <AbilityButton
              ability="merge"
              size="sm"
              disabledReason={mergeHeld}
              onClick={() => setMerging(true)}
            >
              <GitMerge className="size-3.5" />
              Merge
            </AbilityButton>
          )}
          {pull.state !== "merged" && (
            <AbilityButton
              ability="pulls"
              size="sm"
              variant="outline"
              onClick={() => setState(open ? "closed" : "open")}
            >
              {open ? "Close" : "Reopen"}
            </AbilityButton>
          )}
          <OnGitHub href={pull.htmlUrl} />
        </div>
      </div>

      <p className="numeric text-body" data-slot="github-pull-size">
        <span className="text-success">+{pull.additions}</span>{" "}
        <span className="text-destructive">−{pull.deletions}</span> ·{" "}
        {plural(pull.changedFiles, "file")} · {plural(pull.commits, "commit")} ·{" "}
        {pull.mergeable === null
          ? "GitHub is still working out whether it merges cleanly"
          : pull.mergeable
            ? "merges cleanly"
            : "has conflicts"}
      </p>

      <div className="min-w-0">
        {pull.body.trim() ? (
          <Markdown source={pull.body} repoUrl={repo.htmlUrl} />
        ) : (
          <p className="text-body text-muted-foreground">No description.</p>
        )}
      </div>

      <Panel plain aria-label="Checks">
        <PanelHeader
          title="Checks"
          count={pull.checks.length > 0 ? pull.checks.length : undefined}
        />
        <PanelBody flush>
          {!pull.checksReadable ? (
            <EmptyNote>{pull.checksMessage ?? "GitHub did not say."}</EmptyNote>
          ) : pull.checks.length === 0 ? (
            <EmptyNote>No checks ran on its head.</EmptyNote>
          ) : (
            <ul className="divide-y divide-hairline" data-slot="github-checks">
              {pull.checks.map((check, index) => (
                <li
                  key={`${check.name}-${index}`}
                  className="flex min-w-0 items-center gap-2.5 py-2"
                >
                  <CheckMark check={check} />
                  <span className="min-w-0 flex-1 truncate text-body">{check.name}</span>
                  {check.detailsUrl && (
                    <a
                      href={check.detailsUrl}
                      target="_blank"
                      rel="noreferrer"
                      className="text-hint text-brand hover:underline"
                    >
                      Details
                    </a>
                  )}
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Reviews">
        <PanelHeader
          title="Reviews"
          count={pull.reviews.length > 0 ? pull.reviews.length : undefined}
        />
        <PanelBody flush>
          {pull.reviews.length === 0 ? (
            <EmptyNote>Nobody has reviewed it yet.</EmptyNote>
          ) : (
            <ul className="divide-y divide-hairline" data-slot="github-reviews">
              {pull.reviews.map((review) => (
                <li key={review.id} className="flex min-w-0 items-center gap-2.5 py-2 text-body">
                  <ForgeFace login={review.author} provider="github" size="md" />
                  <span className="min-w-0 flex-1 truncate">{review.author}</span>
                  <span
                    className={
                      review.state === "APPROVED"
                        ? "text-success"
                        : review.state === "CHANGES_REQUESTED"
                          ? "text-destructive"
                          : "text-muted-foreground"
                    }
                  >
                    {REVIEW_WORD[review.state]}
                  </span>
                  {review.submittedAt && (
                    <span className="numeric text-hint text-muted-foreground">
                      {relativeTime(review.submittedAt)}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      {merging && (
        <MergeDialog
          pull={pull}
          onClose={() => setMerging(false)}
          onMerged={() => {
            setMerging(false)
            read.refresh()
            refresh()
          }}
        />
      )}
    </div>
  )
}

const REVIEW_WORD: Record<string, string> = {
  APPROVED: "approved",
  CHANGES_REQUESTED: "asked for changes",
  COMMENTED: "commented",
  DISMISSED: "dismissed",
  PENDING: "pending",
}

function CheckMark({ check }: { check: GitHubCheck }) {
  if (check.status !== "completed") {
    return <Clock role="img" aria-label="running" className="size-4 shrink-0 text-warning" />
  }
  if (check.conclusion === "success") {
    return <CheckCircle role="img" aria-label="passed" className="size-4 shrink-0 text-success" />
  }
  if (["failure", "timed_out", "error", "action_required"].includes(check.conclusion ?? "")) {
    return (
      <CrossCircle role="img" aria-label="failed" className="size-4 shrink-0 text-destructive" />
    )
  }
  return (
    <Slash
      role="img"
      aria-label={check.conclusion ?? "completed"}
      className="size-4 shrink-0 text-muted-foreground"
    />
  )
}

const METHOD_LABEL = { merge: "Merge commit", squash: "Squash", rebase: "Rebase" } as const

/**
 * Merging, with the method the repository allows and the head the reader
 * saw: GitHub refuses the merge if the head moved since, which is the point
 * of sending it. A refusal (a conflict, a required check) is GitHub's own
 * sentence, said in the dialog.
 */
function MergeDialog({
  pull,
  onClose,
  onMerged,
}: {
  pull: GitHubPullDetail
  onClose: () => void
  onMerged: () => void
}) {
  const { path } = useRepo()
  const [method, setMethod] = useState(pull.mergeMethods[0])
  const [title, setTitle] = useState("")
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string>()
  const merge = async () => {
    setBusy(true)
    setRefused(undefined)
    try {
      const body: GitHubMergeRequest = { method, sha: pull.head.sha }
      if (title.trim() && method !== "rebase") body.title = title.trim()
      const result = await post<GitHubMergeResult>(path(`/pulls/${pull.number}/merge`), body)
      if (!result.merged) {
        setRefused(result.message)
        return
      }
      notify.success(`Merged #${pull.number}`)
      onMerged()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={`Merge #${pull.number}`}
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={merge} pending={busy}>
            Merge
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="text-body">
          Merges <span className="font-mono">{pull.head.ref}</span> at{" "}
          <span className="font-mono">{pull.head.sha.slice(0, 7)}</span> into{" "}
          <span className="font-mono">{pull.base.ref}</span>.
        </p>
        {pull.mergeMethods.length > 1 && (
          <Field label="Method">
            <Segments
              value={method}
              options={pull.mergeMethods.map((m) => ({ value: m, label: METHOD_LABEL[m] }))}
              onChange={setMethod}
              size="field"
            />
          </Field>
        )}
        {method !== "rebase" && (
          <Field label="Commit title" htmlFor="merge-title">
            <Input
              id="merge-title"
              placeholder={`${pull.title} (#${pull.number})`}
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
          </Field>
        )}
        {refused && (
          <p role="alert" className="text-body text-destructive">
            {refused}
          </p>
        )}
      </div>
    </Modal>
  )
}
