"use client"

import { useState } from "react"
import { get, post, errorMessage } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { relativeTime } from "@/lib/format"
import { DiffView } from "@/components/files/diff-view"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { Field } from "@/components/form"
import { FilterChip } from "@/components/tabs"
import { PreviewHeader } from "@/components/git/preview-header"
import { HistoryPaging } from "@/components/git/inspect-panels"
import type { PreviewContext } from "@/components/git/preview-panel"
import type { GitPullRequest } from "@/lib/types"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type PullFile = {
  filename: string
  previous_filename?: string
  status: string
  additions: number
  deletions: number
  patch?: string
}
type Conversation = {
  entries: {
    id: number
    kind: string
    author: string
    body: string
    state?: string
    at: string
    path?: string
    line?: number
  }[]
  hasMore: boolean
}

export function PullReview({
  pull,
  ctx,
  onChanged,
}: {
  pull: GitPullRequest
  ctx: PreviewContext
  onChanged: () => void
}) {
  const [tab, setTab] = useState("files")
  const [page, setPage] = useState(1)
  const [file, setFile] = useState<string | null>(null)
  // Keep the reviewed revision stable while composing. Polling the summary
  // must not silently retarget an approval to a newer push.
  const [head, setHead] = useState(pull.headSha)
  const [body, setBody] = useState("")
  const [event, setEvent] = useState("COMMENT")
  const [error, setError] = useState<Error>()
  const files = usePoll(
    (signal) =>
      get<{ files: PullFile[]; hasMore: boolean; limited: boolean }>(
        `/git/github/pulls/${pull.number}/files`,
        { path: ctx.repoPath, head, page },
        signal,
      ),
    0,
    [ctx.repoPath, pull.number, head, page],
    { enabled: tab === "files" && !!head },
  )
  const conversation = usePoll(
    (signal) =>
      get<Conversation>(
        `/git/github/pulls/${pull.number}/conversation`,
        { path: ctx.repoPath, page },
        signal,
      ),
    0,
    [ctx.repoPath, pull.number, page],
    { enabled: tab === "conversation" },
  )
  const selected = files.data?.files.find((f) => f.filename === file)
  const changed = !!head && pull.headSha !== head
  const submit = () => {
    ctx.confirm({
      title:
        event === "APPROVE"
          ? `Approve #${pull.number}?`
          : event === "REQUEST_CHANGES"
            ? `Request changes on #${pull.number}?`
            : `Post comment on #${pull.number}?`,
      description: `Publish this review on GitHub for commit ${head?.slice(0, 7)}.`,
      confirmLabel: "Publish review",
      action: async () => {
        setError(undefined)
        try {
          await ctx.run("Review published", () =>
            post(
              `/git/github/pulls/${pull.number}/review`,
              { body, event, headSha: head },
              { query: { path: ctx.repoPath } },
            ).then(() => ({ command: "GitHub review", ok: true, output: "" })),
          )
          setBody("")
          conversation.refresh()
          onChanged()
        } catch (e) {
          setError(new Error(errorMessage(e)))
          throw e
        }
      },
    })
  }
  return (
    <div className="border-t border-hairline">
      <div className="flex flex-wrap items-center gap-1 border-b border-hairline px-3 py-1.5">
        <FilterChip
          selected={tab === "files"}
          onClick={() => {
            setTab("files")
            setPage(1)
          }}
        >
          Changed files
        </FilterChip>
        <FilterChip
          selected={tab === "conversation"}
          onClick={() => {
            setTab("conversation")
            setPage(1)
          }}
        >
          Conversation
        </FilterChip>
        <Button
          size="xs"
          variant="ghost"
          onClick={() => {
            setHead(pull.headSha)
            setPage(1)
            setFile(null)
            files.refresh()
            conversation.refresh()
            onChanged()
          }}
        >
          Reload
        </Button>
      </div>
      {changed && (
        <p className="px-3 py-2 text-hint text-warning">
          New commits were pushed. Reload and review them before publishing.
        </p>
      )}
      {tab === "files" ? (
        <>
          {files.error && <ErrorState className="m-3" error={files.error} />}
          {files.loading && <LoadingRows className="p-3" rows={3} />}
          {!head && (
            <p className="p-3 text-hint text-muted-foreground">
              Reload the request to read its commit revision.
            </p>
          )}
          {files.data && (
            <>
              {files.data.limited && (
                <p className="p-3 text-hint text-warning">
                  GitHub limits this list to 3,000 files. Open the request on GitHub for the
                  remaining changes.
                </p>
              )}
              <ul className="divide-y divide-hairline">
                {files.data.files.map((f) => (
                  <li key={f.filename}>
                    <button
                      type="button"
                      aria-pressed={file === f.filename}
                      className="flex w-full items-center gap-2 px-3 py-2 text-left focus-ring-inset hover:bg-row-hover"
                      onClick={() => setFile(file === f.filename ? null : f.filename)}
                    >
                      <span className="min-w-0 flex-1 truncate font-mono text-hint">
                        {f.previous_filename ? `${f.previous_filename} → ` : ""}
                        {f.filename}
                      </span>
                      <span className="numeric shrink-0 font-mono text-hint">
                        <span className="text-(--git-added)">+{f.additions}</span>{" "}
                        <span className="text-(--git-deleted)">−{f.deletions}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
              <HistoryPaging
                busy={files.loading}
                start={(page - 1) * 100}
                unit="items"
                count={files.data.files.length}
                hasMore={files.data.hasMore}
                onPrevious={() => setPage(page - 1)}
                onNext={() => setPage(page + 1)}
              />
            </>
          )}
          {selected &&
            (selected.patch ? (
              <DiffView body={selected.patch} singleFile lineNumbers />
            ) : (
              <p className="p-3 text-hint text-muted-foreground">
                GitHub supplied no text patch for this file. It may be binary or too large; open it
                on GitHub to inspect it.
              </p>
            ))}
        </>
      ) : (
        <>
          {conversation.error && <ErrorState className="m-3" error={conversation.error} />}
          {conversation.loading && <LoadingRows className="p-3" rows={3} />}
          {conversation.data && (
            <>
              <ul className="divide-y divide-hairline">
                {conversation.data.entries.map((e) => (
                  <li key={`${e.kind}:${e.id}`} className="space-y-1 px-3 py-2">
                    <p className="text-hint text-muted-foreground">
                      {e.author} · {e.state?.toLowerCase().replaceAll("_", " ") || "comment"}
                      {e.at ? ` · ${relativeTime(e.at)}` : ""}
                      {e.path ? ` · ${e.path}:${e.line || ""}` : ""}
                    </p>
                    <p className="text-xs break-words whitespace-pre-wrap">
                      {e.body || "No comment."}
                    </p>
                  </li>
                ))}
              </ul>
              {conversation.data.entries.length === 0 && (
                <p className="p-3 text-hint text-muted-foreground">No conversation yet.</p>
              )}
              <HistoryPaging
                busy={conversation.loading}
                start={(page - 1) * 100}
                unit="items"
                count={conversation.data.entries.length}
                hasMore={conversation.data.hasMore}
                onPrevious={() => setPage(page - 1)}
                onNext={() => setPage(page + 1)}
              />
            </>
          )}
        </>
      )}
      {ctx.canControl && pull.state === "open" && (
        <div className="space-y-3 border-t border-hairline p-3">
          <Field label="Review">
            <Select value={event} onValueChange={setEvent}>
              <SelectTrigger aria-label="Review action">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="COMMENT">Comment</SelectItem>
                <SelectItem value="APPROVE">Approve</SelectItem>
                <SelectItem value="REQUEST_CHANGES">Request changes</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Comment">
            <Textarea
              aria-label="Review comment"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              maxLength={60000}
              rows={3}
            />
          </Field>
          {error != null && <ErrorState error={error} />}
          <Button
            size="sm"
            variant="outline"
            disabled={!!ctx.busy || !head || changed || (event !== "APPROVE" && !body.trim())}
            onClick={submit}
          >
            Publish review
          </Button>
        </div>
      )}
    </div>
  )
}

type Job = {
  databaseId: number
  name: string
  status: string
  conclusion: string
  steps: { number: number; name: string; status: string; conclusion: string }[]
}
export function WorkflowPreview({
  id,
  ctx,
  onClose,
}: {
  id: number
  ctx: PreviewContext
  onClose: () => void
}) {
  const run = usePoll(
    (signal) =>
      get<{ name: string; url: string; status: string; conclusion: string; jobs: Job[] }>(
        `/git/github/runs/${id}`,
        { path: ctx.repoPath },
        signal,
      ),
    30000,
    [ctx.repoPath, id],
  )
  const [jobID, setJobID] = useState<number | null>(null)
  const [step, setStep] = useState("")
  const [failed, setFailed] = useState(false)
  const job = run.data?.jobs.find((j) => j.databaseId === jobID)
  const log = usePoll(
    (signal) =>
      get<{ body: string }>(
        `/git/github/runs/${id}/log`,
        { path: ctx.repoPath, job: jobID, step: step || undefined, failed },
        signal,
      ),
    0,
    [ctx.repoPath, id, jobID, step, failed],
    { enabled: !!jobID },
  )
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title={run.data?.name || `Workflow run ${id}`}
        subtitle={
          run.data ? `${run.data.status} · ${run.data.conclusion || "pending"}` : "Loading jobs…"
        }
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {run.error && <ErrorState error={run.error} className="m-3" />}
        {run.loading && !run.data && <LoadingRows className="p-3" rows={3} />}
        {run.data && (
          <>
            <div className="px-3 py-2">
              <Button size="xs" variant="ghost" asChild>
                <a href={run.data.url} target="_blank" rel="noreferrer">
                  Open on GitHub
                </a>
              </Button>
            </div>
            <ul className="divide-y divide-hairline">
              {run.data.jobs.map((j) => (
                <li key={j.databaseId}>
                  <button
                    type="button"
                    aria-pressed={j.databaseId === jobID}
                    className="flex w-full gap-2 px-3 py-2 text-left text-xs focus-ring-inset hover:bg-row-hover"
                    onClick={() => {
                      setJobID(j.databaseId)
                      setStep("")
                    }}
                  >
                    <span className="min-w-0 flex-1 truncate">{j.name}</span>
                    <span
                      className={
                        j.conclusion === "failure" ? "text-destructive" : "text-muted-foreground"
                      }
                    >
                      {j.conclusion || j.status}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </>
        )}
        {job && (
          <>
            <div className="flex flex-wrap items-center gap-2 border-y border-hairline p-3">
              <Select value={step || "all"} onValueChange={(v) => setStep(v === "all" ? "" : v)}>
                <SelectTrigger aria-label="Workflow step" className="min-w-0 flex-1">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All steps</SelectItem>
                  {job.steps.map((s) => (
                    <SelectItem key={s.number} value={s.name}>
                      {s.name} · {s.conclusion || s.status}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FilterChip selected={failed} onClick={() => setFailed(!failed)}>
                Failed only
              </FilterChip>
              <Button size="xs" variant="ghost" onClick={log.refresh}>
                Reload log
              </Button>
            </div>
            {log.error && <ErrorState error={log.error} className="m-3" />}
            {log.loading && <LoadingRows className="p-3" rows={4} />}
            {log.data && (
              <pre className="overflow-auto p-3 font-mono text-hint whitespace-pre">
                {log.data.body || "No log output for this selection."}
              </pre>
            )}
          </>
        )}
      </div>
    </div>
  )
}
