"use client"

import { useState } from "react"
import {
  AcronymMarkdown,
  ChatBubble,
  CheckCircle,
  CrossCircle,
  FileText,
  PaperAirplane,
  RefreshClockwise,
} from "@/components/icons"
import { get, post, errorMessage } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { Segments } from "@/components/deploy/settings/segments"
import { DiffView } from "@/components/files/diff-view"
import { FileIcon } from "@/components/files/file-icon"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { ForgeFace } from "@/components/git/marks"
import { Markdown } from "@/components/git/markdown"
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

/** A review's verdict as the timeline says it, with its mark and hue. */
const REVIEW_STATE: Record<string, { action: string; Glyph?: typeof CheckCircle; tone?: string }> =
  {
    APPROVED: { action: "approved these changes", Glyph: CheckCircle, tone: "text-success" },
    CHANGES_REQUESTED: {
      action: "requested changes",
      Glyph: CrossCircle,
      tone: "text-destructive",
    },
    COMMENTED: { action: "reviewed" },
    DISMISSED: { action: "had a review dismissed", tone: "text-muted-foreground" },
  }

/**
 * One comment on a request, the way a forge draws one: who, what they did,
 * when, and the body rendered from its Markdown inside an edge — the
 * description is the first of these, so the conversation reads as one thread
 * from the top of the request down.
 */
export function CommentCard({
  author,
  action,
  at,
  body,
  empty,
  repoUrl,
  mark,
  location,
}: {
  author?: string
  action: string
  at?: string
  body?: string
  /** What the card says when the body is empty; nothing is drawn under the head without it. */
  empty?: string
  repoUrl?: string
  /** A verdict's glyph in its hue, for a review. */
  mark?: React.ReactNode
  /** The file and line an inline comment is on. */
  location?: string
}) {
  const text = body?.trim()
  return (
    <article className="overflow-hidden rounded-lg border border-hairline">
      <header className="flex min-w-0 items-center gap-1.5 border-b border-hairline bg-surface-header px-2.5 py-1.5 text-hint text-muted-foreground">
        <ForgeFace login={author} provider="github" size="xs" />
        <span className="shrink-0 font-medium text-foreground">{author ?? "unknown"}</span>
        {mark}
        <span className="truncate">{action}</span>
        {at && <span className="ml-auto shrink-0">{relativeTime(at)}</span>}
      </header>
      {location && (
        <p className="truncate border-b border-hairline bg-surface-sunken px-2.5 py-1 font-mono text-micro text-muted-foreground">
          {location}
        </p>
      )}
      {text ? (
        <Markdown source={text} repoUrl={repoUrl} className="px-3 py-2.5" />
      ) : empty ? (
        <p className="px-3 py-2.5 text-hint text-muted-foreground italic">{empty}</p>
      ) : null}
    </article>
  )
}

const FILE_STATUS: Record<string, { letter: string; colour: string }> = {
  added: { letter: "A", colour: "var(--git-added)" },
  modified: { letter: "M", colour: "var(--git-modified)" },
  changed: { letter: "M", colour: "var(--git-modified)" },
  removed: { letter: "D", colour: "var(--git-deleted)" },
  renamed: { letter: "R", colour: "var(--git-renamed)" },
  copied: { letter: "C", colour: "var(--git-renamed)" },
}

type Verdict = "comment" | "APPROVE" | "REQUEST_CHANGES"

/**
 * The conversation on a request and the files it changes, with the box to
 * answer under them.
 *
 * The answer is a section of the page, not a button that opens a dialog over
 * what is being answered. It is one box for the two things a reader says to
 * a request: a comment, which joins the conversation and needs nothing but
 * the words, and a review, which is a verdict pinned to the commit that was
 * read — so Approve and Request changes carry the head the files were loaded
 * at, and refuse once new commits arrive until the reader has reloaded and
 * looked at them. Write and Preview are the forge's two tabs: the preview is
 * the same renderer the comment will be read in.
 */
export function PullReview({
  pull,
  ctx,
  repoUrl,
  onChanged,
}: {
  pull: GitPullRequest
  ctx: PreviewContext
  repoUrl?: string
  onChanged: () => void
}) {
  const [tab, setTab] = useState<"conversation" | "files">("conversation")
  const [page, setPage] = useState(1)
  const [file, setFile] = useState<string | null>(null)
  // Keep the reviewed revision stable while composing. Polling the summary
  // must not silently retarget an approval to a newer push.
  const [head, setHead] = useState(pull.headSha)
  const [body, setBody] = useState("")
  const [verdict, setVerdict] = useState<Verdict>("comment")
  const [writing, setWriting] = useState<"write" | "preview">("write")
  const [sending, setSending] = useState(false)
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
  const open = pull.state === "open"
  // A review with no words is the wrapper GitHub records around a batch of
  // inline comments; the comments themselves are their own entries.
  const entries = (conversation.data?.entries ?? []).filter(
    (e) => !(e.kind === "reviews" && e.state === "COMMENTED" && !e.body.trim()),
  )

  const reload = () => {
    setHead(pull.headSha)
    setPage(1)
    setFile(null)
    files.refresh()
    conversation.refresh()
    onChanged()
  }

  const posted = () => {
    setBody("")
    setWriting("write")
    setVerdict("comment")
    setTab("conversation")
    conversation.refresh()
    onChanged()
  }

  // A comment joins the conversation and goes straight out. A verdict is
  // published on GitHub against one commit, so it is confirmed first, naming
  // the commit it lands on.
  const send = async () => {
    setError(undefined)
    if (verdict !== "comment") {
      const event = verdict
      ctx.confirm({
        title:
          event === "APPROVE" ? `Approve #${pull.number}?` : `Request changes on #${pull.number}?`,
        description: `Publish this review on GitHub for commit ${head?.slice(0, 7)}.`,
        confirmLabel: "Publish review",
        action: async () => {
          try {
            await post(
              `/git/github/pulls/${pull.number}/review`,
              { body, event, headSha: head },
              { query: { path: ctx.repoPath } },
            )
            posted()
          } catch (e) {
            setError(new Error(errorMessage(e)))
            throw e
          }
        },
      })
      return
    }
    setSending(true)
    try {
      await post(
        `/git/github/pulls/${pull.number}/comment`,
        { body: body.trim() },
        { query: { path: ctx.repoPath } },
      )
      notify.success(`Commented on #${pull.number}`)
      posted()
    } catch (e) {
      setError(new Error(errorMessage(e)))
    } finally {
      setSending(false)
    }
  }

  const blocked =
    sending ||
    !!ctx.busy ||
    (verdict === "comment" && !body.trim()) ||
    (verdict === "REQUEST_CHANGES" && !body.trim()) ||
    (verdict !== "comment" && (!head || changed))

  return (
    <section className="border-t border-hairline">
      <div className="flex h-9 items-center border-b border-hairline px-1">
        <button
          type="button"
          role="tab"
          aria-selected={tab === "conversation"}
          className={tabClasses(tab === "conversation", "h-9 px-2.5")}
          onClick={() => {
            setTab("conversation")
            setPage(1)
          }}
        >
          <ChatBubble aria-hidden className="size-3.5" />
          Conversation
          {pull.comments > 0 && <ChipCount>{pull.comments}</ChipCount>}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === "files"}
          className={tabClasses(tab === "files", "h-9 px-2.5")}
          onClick={() => {
            setTab("files")
            setPage(1)
          }}
        >
          <FileText aria-hidden className="size-3.5" />
          Files changed
          {pull.files ? <ChipCount>{pull.files}</ChipCount> : null}
        </button>
        <span className="flex-1" />
        <Button size="xs" variant="ghost" onClick={reload}>
          <RefreshClockwise />
          Reload
        </Button>
      </div>
      {changed && (
        <Notice tone="warning" title="New commits were pushed" className="m-3">
          Reload and read them before approving or requesting changes — a review is pinned to the
          commit it was read at.
        </Notice>
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
                {files.data.files.map((f) => {
                  const status = FILE_STATUS[f.status] ?? FILE_STATUS.modified
                  return (
                    <li key={f.filename}>
                      <button
                        type="button"
                        aria-pressed={file === f.filename}
                        className={cn(
                          "flex w-full items-center gap-2 px-3 py-1.5 text-left focus-ring-inset hover:bg-row-hover",
                          file === f.filename && "bg-accent",
                        )}
                        onClick={() => setFile(file === f.filename ? null : f.filename)}
                      >
                        <span
                          aria-label={f.status}
                          className="w-3 shrink-0 font-mono text-hint font-semibold"
                          style={{ color: status.colour }}
                        >
                          {status.letter}
                        </span>
                        <FileIcon
                          entry={{
                            name: f.filename.split("/").pop() ?? f.filename,
                            isDir: false,
                            isSymlink: false,
                          }}
                          className="size-4 shrink-0"
                        />
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
                  )
                })}
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
        <div className="space-y-2.5 px-3 py-3">
          {conversation.error && <ErrorState error={conversation.error} />}
          {conversation.loading && <LoadingRows rows={3} />}
          {conversation.data && entries.length === 0 && (
            <p className="text-hint text-muted-foreground">
              No comments yet — be the first to say something below.
            </p>
          )}
          {entries.map((e) => {
            const review = e.kind === "reviews" ? REVIEW_STATE[e.state ?? ""] : undefined
            return (
              <CommentCard
                key={`${e.kind}:${e.id}`}
                author={e.author}
                action={
                  review ? review.action : e.kind === "inline" ? "commented on a line" : "commented"
                }
                mark={
                  review?.Glyph ? (
                    <review.Glyph aria-hidden className={cn("size-3.5 shrink-0", review.tone)} />
                  ) : undefined
                }
                location={e.path ? `${e.path}${e.line ? `:${e.line}` : ""}` : undefined}
                at={e.at}
                body={e.body}
                repoUrl={repoUrl}
              />
            )
          })}
          {conversation.data && (conversation.data.hasMore || page > 1) && (
            <HistoryPaging
              busy={conversation.loading}
              start={(page - 1) * 100}
              unit="items"
              count={conversation.data.entries.length}
              hasMore={conversation.data.hasMore}
              onPrevious={() => setPage(page - 1)}
              onNext={() => setPage(page + 1)}
            />
          )}
        </div>
      )}

      {ctx.canControl && (
        <form
          aria-label={`Comment on #${pull.number}`}
          className="space-y-2.5 border-t border-hairline px-3 py-3"
          onSubmit={(event) => {
            event.preventDefault()
            if (!blocked) void send()
          }}
        >
          <div className="flex min-w-0 items-start gap-2.5">
            {ctx.githubLogin ? (
              <ForgeFace
                login={ctx.githubLogin}
                provider="github"
                size="sm"
                account
                className="mt-0.5"
              />
            ) : null}
            <div className="min-w-0 flex-1 overflow-hidden rounded-lg border border-input bg-control focus-ring-within">
              <div className="flex h-8 items-center border-b border-hairline px-1">
                {(["write", "preview"] as const).map((mode) => (
                  <button
                    key={mode}
                    type="button"
                    role="tab"
                    aria-selected={writing === mode}
                    onClick={() => setWriting(mode)}
                    className={tabClasses(writing === mode, "h-8 px-2.5 text-xs")}
                  >
                    {mode === "write" ? "Write" : "Preview"}
                  </button>
                ))}
                <span className="ml-auto pr-2 text-micro text-muted-foreground">
                  {ctx.githubLogin ? `as ${ctx.githubLogin}` : "as the checkout's owner"}
                </span>
              </div>
              {writing === "write" ? (
                <Textarea
                  aria-label="Comment"
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  onKeyDown={(e) => {
                    if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && !blocked) {
                      e.preventDefault()
                      void send()
                    }
                  }}
                  maxLength={60000}
                  rows={4}
                  placeholder={open ? "Leave a comment, or review the changes" : "Leave a comment"}
                  className="min-h-24 resize-y rounded-none border-0 bg-transparent text-body shadow-none outline-none dark:bg-transparent"
                />
              ) : body.trim() ? (
                <Markdown source={body} repoUrl={repoUrl} className="min-h-24 px-3 py-2.5" />
              ) : (
                <p className="min-h-24 px-3 py-2.5 text-hint text-muted-foreground italic">
                  Nothing to preview.
                </p>
              )}
              <p className="flex items-center gap-1.5 border-t border-hairline px-2.5 py-1 text-micro text-muted-foreground">
                <AcronymMarkdown aria-hidden className="size-3.5" />
                Markdown works · ⌘↵ to send
              </p>
            </div>
          </div>
          {error != null && <ErrorState error={error} />}
          <div className="flex flex-wrap items-center justify-end gap-2">
            {open && (
              <Segments<Verdict>
                label="What to post"
                value={verdict}
                onChange={setVerdict}
                options={[
                  { value: "comment", label: "Comment" },
                  { value: "APPROVE", label: "Approve" },
                  { value: "REQUEST_CHANGES", label: "Request changes" },
                ]}
              />
            )}
            <Button size="sm" type="submit" disabled={blocked} pending={sending}>
              {verdict === "APPROVE" ? (
                <CheckCircle />
              ) : verdict === "REQUEST_CHANGES" ? (
                <CrossCircle />
              ) : (
                <PaperAirplane />
              )}
              {verdict === "APPROVE"
                ? "Approve"
                : verdict === "REQUEST_CHANGES"
                  ? "Request changes"
                  : "Comment"}
            </Button>
          </div>
        </form>
      )}
    </section>
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
