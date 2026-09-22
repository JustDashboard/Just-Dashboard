"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { timestamp } from "@/lib/format"
import type { GitBlame, GitReflogEntry, GitResult, GitSignature } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { SourceBranch } from "@/components/git/glyphs"
import { NameDialog } from "@/components/git/name-dialog"
import { PreviewHeader } from "@/components/git/preview-header"
import type { PreviewContext } from "@/components/git/preview-panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Status } from "@/components/status-dot"

export function RecoveryPreview({ ctx, onClose }: { ctx: PreviewContext; onClose: () => void }) {
  const [skip, setSkip] = useState(0)
  const [rescue, setRescue] = useState<GitReflogEntry>()
  const log = usePoll(
    (signal) => get<GitReflogEntry[]>("/git/reflog", { path: ctx.repoPath, skip }, signal),
    0,
    [ctx.repoPath, skip],
  )
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title="Recovery timeline"
        mono={false}
        subtitle="Recent branch movements, including commits removed from history"
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {log.error && <ErrorState error={log.error} className="m-3" />}
        {log.loading && !log.data && <LoadingRows className="p-3" rows={6} />}
        {log.data?.length === 0 && (
          <EmptyState icon={SourceBranch} title="No recorded movements" className="m-3" />
        )}
        <ul className="divide-y divide-hairline">
          {log.data?.map((entry, i) => (
            <li key={`${skip + i}:${entry.sha}`} className="flex items-center gap-2 px-3 py-2">
              <button
                type="button"
                className="min-w-0 flex-1 text-left focus-ring-inset hover:text-foreground"
                onClick={() => ctx.onSelect({ kind: "commit", sha: entry.sha })}
              >
                <span className="block truncate text-body">{entry.message}</span>
                <span className="text-hint text-muted-foreground">
                  <span className="font-mono">{entry.sha.slice(0, 7)}</span> · {entry.author} ·{" "}
                  {timestamp(entry.at)}
                </span>
              </button>
              {ctx.canControl && (
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!!ctx.busy}
                  onClick={() => setRescue(entry)}
                >
                  Rescue
                </Button>
              )}
            </li>
          ))}
        </ul>
      </div>
      <HistoryPaging
        start={skip}
        count={log.data?.length ?? 0}
        hasMore={log.data?.length === 100}
        busy={log.loading}
        onPrevious={() => setSkip(Math.max(0, skip - 100))}
        onNext={() => setSkip(skip + 100)}
      />
      <NameDialog
        open={!!rescue}
        onOpenChange={(open) => !open && setRescue(undefined)}
        title="Rescue this commit"
        label="New branch name"
        confirmLabel="Create recovery branch"
        hint="The current branch and your files stay as they are."
        facts={
          rescue ? [{ label: "commit", value: rescue.sha.slice(0, 7), mono: true }] : undefined
        }
        onSubmit={async (name) => {
          if (!rescue) return
          await ctx.run("Recovery branch created", () =>
            post<GitResult>(
              "/git/recover",
              { name, ref: rescue.sha },
              { query: { path: ctx.repoPath } },
            ),
          )
        }}
      />
    </div>
  )
}

export function BlamePreview({
  ctx,
  file,
  refName = "HEAD",
  onClose,
}: {
  ctx: PreviewContext
  file: string
  refName?: string
  onClose: () => void
}) {
  const [start, setStart] = useState(1)
  const blame = usePoll(
    (signal) =>
      get<GitBlame>("/git/blame", { path: ctx.repoPath, file, ref: refName, start }, signal),
    0,
    [ctx.repoPath, file, refName, start],
  )
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader title={file} subtitle={`Line history at ${refName}`} onClose={onClose} />
      <div className="min-h-0 flex-1 overflow-auto">
        {blame.error && <ErrorState error={blame.error} className="m-3" />}
        {blame.loading && !blame.data && <LoadingRows className="p-3" rows={8} />}
        <ol className="divide-y divide-hairline">
          {blame.data?.lines.map((line) => (
            <li
              key={line.line}
              className="flex min-w-max items-start gap-3 px-3 py-1 font-mono text-xs"
            >
              <span className="numeric w-8 shrink-0 text-right text-muted-foreground">
                {line.line}
              </span>
              <button
                className="w-32 shrink-0 truncate text-left text-hint text-muted-foreground focus-ring-inset hover:text-foreground"
                title={`${line.subject} · ${timestamp(line.at)}`}
                onClick={() => ctx.onSelect({ kind: "commit", sha: line.sha, file })}
              >
                {line.sha.slice(0, 7)} · {line.author}
              </button>
              <span className="whitespace-pre">{line.content || " "}</span>
            </li>
          ))}
        </ol>
      </div>
      <HistoryPaging
        start={start - 1}
        count={blame.data?.lines.length ?? 0}
        hasMore={!!blame.data?.hasMore}
        busy={blame.loading}
        unit="lines"
        onPrevious={() => setStart(Math.max(1, start - 200))}
        onNext={() => setStart(start + 200)}
      />
    </div>
  )
}

export function HistoryPaging({
  start,
  count,
  hasMore,
  busy,
  unit = "entries",
  onPrevious,
  onNext,
}: {
  start: number
  count: number
  hasMore: boolean
  busy: boolean
  unit?: string
  onPrevious: () => void
  onNext: () => void
}) {
  return (
    <div className="flex shrink-0 items-center gap-2 border-t border-hairline px-3 py-2">
      <span className="numeric min-w-0 flex-1 text-hint text-muted-foreground">
        {count ? `${start + 1}–${start + count}` : "0"} {unit}
      </span>
      <Button size="xs" variant="ghost" disabled={start === 0 || busy} onClick={onPrevious}>
        Previous
      </Button>
      <Button size="xs" variant="outline" disabled={!hasMore || busy} onClick={onNext}>
        Next
      </Button>
    </div>
  )
}

const signatureLabels: Record<string, string> = {
  G: "Good signature",
  B: "Bad signature",
  U: "Good signature, trust unknown",
  X: "Expired signature",
  Y: "Expired signing key",
  R: "Revoked signing key",
  E: "Signature could not be checked",
  N: "Unsigned commit",
}

export function CommitSignature({ repoPath, sha }: { repoPath: string; sha: string }) {
  const signature = usePoll(
    (signal) => get<GitSignature>("/git/signature", { path: repoPath, ref: sha }, signal),
    0,
    [repoPath, sha],
  )
  const s = signature.data
  return (
    <div
      className="flex flex-wrap items-center gap-2 text-hint text-muted-foreground"
      title={s?.fingerprint || s?.key}
    >
      {s && s.status !== "N" && (
        <Status
          label={s.status === "G" ? "verified" : "not verified"}
          tone={s.status === "G" ? "running" : ["B", "R"].includes(s.status) ? "danger" : "warning"}
        />
      )}
      {signature.error
        ? "Signature check unavailable"
        : s
          ? `${signatureLabels[s.status] ?? "Unknown signature status"}${s.signer ? ` · ${s.signer}` : ""}`
          : "Checking signature…"}
    </div>
  )
}
