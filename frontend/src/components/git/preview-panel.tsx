"use client"

import { useEffect, useState } from "react"
import {
  BranchPlus,
  ClockRewind,
  Copy,
  CornerUpLeft,
  Cross,
  External,
  FloppyDisk,
  GitMerge,
  RotateCounterClockwise,
  GitTag,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import { gitStyle, type GitTone } from "@/lib/git-status"
import type {
  FileContent,
  GitChangedFile,
  GitCommit,
  GitCommitDetail,
  GitComparison,
  GitPullRequest,
  GitResult,
  GitStash,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { CodeEditor } from "@/components/code-editor"
import { DiffView } from "@/components/files/diff-view"
import type { GitRun } from "@/components/git/run"
import { NameDialog } from "@/components/git/name-dialog"
import { MergePullDialog } from "@/components/git/merge-pull-dialog"
import { RefTags } from "@/components/git/ref-tags"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { PaneHeader } from "@/components/panel"
import { VerbBar, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/** What the preview column is showing. */
export type GitPreview =
  | {
      kind: "diff"
      title: string
      subtitle?: string
      body: string
      /** One file's diff, titled with its name — the renderer can drop git's
       *  header rather than repeat it. */
      singleFile?: boolean
    }
  | { kind: "file"; path: string }
  | { kind: "commit"; sha: string; subject?: string; file?: string }
  | { kind: "stash"; stash: GitStash }
  | { kind: "compare"; base: string; head: string }
  | { kind: "pull"; number: number; title?: string }

/** What every preview gets from the workspace. */
export type PreviewContext = {
  repoPath: string
  branch: string
  canWrite: boolean
  canControl: boolean
  canDestruct: boolean
  busy?: string
  run: GitRun
  confirm: (req: ConfirmRequest) => void
  onChanged: () => void
  onSelect: (p: GitPreview) => void
  /** Narrow the history tab to one file's commits. */
  onFileHistory: (path: string) => void
}

/**
 * The right-hand column of the repo workspace: whatever the operator last
 * clicked. A changed file shows as a diff; a commit as its message and the
 * files it touched, each one a click from its own diff; a stash as what it
 * holds; a branch comparison as the commits it would bring; a pull request as
 * its review state and the button that merges it; a file picked from the tree
 * opens in place for editing. This is what keeps the whole thing on one screen
 * — the point of the integrated tree is that looking at a file, or its diff,
 * never sends you to another page.
 */
export function PreviewPanel({
  preview,
  ctx,
  onClose,
}: {
  preview: GitPreview | null
  ctx: PreviewContext
  onClose: () => void
}) {
  if (!preview) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <EmptyState
          icon={GitMerge}
          title="Nothing selected"
          description="Click a changed file to see what changed, a commit to see what it did, or a file in the tree to open it here."
        />
      </div>
    )
  }
  switch (preview.kind) {
    case "diff":
      return (
        <div className="flex min-h-0 flex-1 flex-col">
          <PreviewHeader title={preview.title} subtitle={preview.subtitle} onClose={onClose} />
          <DiffView
            body={preview.body}
            singleFile={preview.singleFile}
            className="min-h-0 flex-1 animate-rise"
          />
        </div>
      )
    case "file":
      return (
        <FilePreview
          key={preview.path}
          path={preview.path}
          canWrite={ctx.canWrite}
          onClose={onClose}
          onChanged={ctx.onChanged}
        />
      )
    case "commit":
      return (
        <CommitPreview
          key={preview.sha}
          sha={preview.sha}
          subject={preview.subject}
          initialFile={preview.file}
          ctx={ctx}
          onClose={onClose}
        />
      )
    case "stash":
      return (
        <StashPreview key={preview.stash.sha} stash={preview.stash} ctx={ctx} onClose={onClose} />
      )
    case "compare":
      return (
        <ComparePreview
          key={`${preview.base}..${preview.head}`}
          base={preview.base}
          head={preview.head}
          ctx={ctx}
          onClose={onClose}
        />
      )
    case "pull":
      return (
        <PullPreview
          key={preview.number}
          number={preview.number}
          title={preview.title}
          ctx={ctx}
          onClose={onClose}
        />
      )
  }
}

/**
 * The strip a preview opens with: its name, a line under it, and the close
 * button. No glyph — the content beneath says what kind of thing it is.
 */
function PreviewHeader({
  title,
  subtitle,
  trailing,
  mono = true,
  onClose,
}: {
  title: string
  subtitle?: React.ReactNode
  trailing?: React.ReactNode
  mono?: boolean
  onClose: () => void
}) {
  return (
    <PaneHeader className="gap-2 px-3">
      <div className="min-w-0 flex-1">
        <p className={cn("truncate text-body font-medium", mono && "font-mono")} title={title}>
          {title}
        </p>
        {subtitle && <p className="truncate text-hint text-muted-foreground">{subtitle}</p>}
      </div>
      {trailing}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            size="sm"
            variant="ghost"
            className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
            aria-label="Close"
            onClick={onClose}
          >
            <Cross className="size-4" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Close the preview</TooltipContent>
      </Tooltip>
    </PaneHeader>
  )
}

function FilePreview({
  path,
  canWrite,
  onClose,
  onChanged,
}: {
  path: string
  canWrite: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const [file, setFile] = useState<FileContent>()
  const [draft, setDraft] = useState("")
  const [error, setError] = useState<Error>()
  const [saving, setSaving] = useState(false)

  // Keyed on the path by the caller, so it mounts fresh per file and this only
  // ever fetches.
  useEffect(() => {
    const controller = new AbortController()
    get<FileContent>("/files/read", { path }, controller.signal)
      .then((f) => {
        setFile(f)
        setDraft(f.content)
      })
      .catch((err) => !controller.signal.aborted && setError(err))
    return () => controller.abort()
  }, [path])

  const dirty = file !== undefined && draft !== file.content

  const save = async () => {
    setSaving(true)
    try {
      await put("/files/write", { path, content: draft })
      notify.success("Saved", { description: path })
      setFile((f) => (f ? { ...f, content: draft } : f))
      // A save can change the working tree, so let the status refresh.
      onChanged()
    } catch (err) {
      notify.error("Could not save", err)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title={path.split("/").pop() ?? path}
        subtitle={path}
        onClose={onClose}
        trailing={
          <>
            {dirty && <Tag tone="warning">unsaved</Tag>}
            {file && !file.binary && canWrite && (
              <Button size="xs" onClick={save} disabled={!dirty || saving} pending={saving}>
                <FloppyDisk className="size-3.5" />
                Save
              </Button>
            )}
          </>
        }
      />
      <div className="min-h-0 flex-1">
        {error && <ErrorState error={error} className="m-3" />}
        {!file && !error && <LoadingRows className="p-3" rows={8} />}
        {file?.binary && (
          <div className="p-4 text-xs text-muted-foreground">
            This is a binary file ({bytes(file.size)}); it is not shown here.
          </div>
        )}
        {file && !file.binary && (
          <CodeEditor
            className="h-full"
            value={draft}
            onChange={setDraft}
            language={file.language}
            readOnly={!canWrite}
          />
        )}
      </div>
    </div>
  )
}

const CHANGE_TONE: Record<string, GitTone> = {
  added: "added",
  deleted: "deleted",
  renamed: "renamed",
  copied: "renamed",
}

const CHANGE_LETTER: Record<string, string> = {
  added: "A",
  deleted: "D",
  renamed: "R",
  copied: "C",
  modified: "M",
}

/**
 * One commit: its message, what it touched, and what can be done with it.
 *
 * The file list is the part a plain diff hides — in a forty-file commit the
 * question "did this touch the migrations" is answered by the list and not
 * by scrolling — and each file is a click from its own diff, drawn under the
 * list so the list stays in view. "All files" is one click away for the
 * reader who wants the whole thing.
 */
function CommitPreview({
  sha,
  subject,
  initialFile,
  ctx,
  onClose,
}: {
  sha: string
  subject?: string
  initialFile?: string
  ctx: PreviewContext
  onClose: () => void
}) {
  const detail = usePoll(
    (signal) => get<GitCommitDetail>("/git/commit", { path: ctx.repoPath, ref: sha }, signal),
    0,
    [ctx.repoPath, sha],
  )
  // Which file's diff is open under the list; "" is every file at once.
  const [file, setFile] = useState<string | null>(initialFile ?? null)
  const [diff, setDiff] = useState<{ key: string; body?: string; error?: string }>()
  const [naming, setNaming] = useState<"branch" | "tag" | null>(null)

  useEffect(() => {
    if (file === null) return
    const controller = new AbortController()
    const key = `${sha}:${file}`
    get<{ diff: string }>(
      "/git/diff",
      { path: ctx.repoPath, ref: sha, file: file || undefined },
      controller.signal,
    )
      .then((res) => setDiff({ key, body: res.diff }))
      .catch((err) => !controller.signal.aborted && setDiff({ key, error: String(err) }))
    return () => controller.abort()
  }, [ctx.repoPath, sha, file])

  const c = detail.data
  const q = { path: ctx.repoPath }
  const current = diff?.key === `${sha}:${file}` ? diff : undefined

  const verbs: Verb[] = []
  verbs.push({
    key: "copy",
    label: "Copy SHA",
    detail: "Put the full commit id on the clipboard.",
    icon: Copy,
    inline: true,
    run: () => void copyText(sha, "Commit id copied"),
  })
  if (ctx.canControl) {
    verbs.push({
      key: "branch",
      label: "Branch here",
      detail: "Start a new branch from this commit and switch to it.",
      icon: BranchPlus,
      run: () => setNaming("branch"),
    })
    verbs.push({
      key: "tag",
      label: "Tag this commit",
      detail: "Pin a name to this commit — a release, a point to come back to.",
      icon: GitTag,
      run: () => setNaming("tag"),
    })
    verbs.push({
      key: "cherry",
      label: "Cherry-pick onto " + ctx.branch,
      detail: "Copy this one commit onto the current branch.",
      icon: CornerUpLeft,
      disabled: !!ctx.busy,
      run: () =>
        void ctx
          .run("Cherry-picked", () => post<GitResult>("/git/cherry-pick", { ref: sha }, { query: q }))
          .catch(() => undefined),
    })
    verbs.push({
      key: "revert",
      label: "Revert this commit",
      detail: "Record a new commit that undoes this one. History keeps both, so it is safe after a push.",
      icon: RotateCounterClockwise,
      disabled: !!ctx.busy,
      run: () =>
        ctx.confirm({
          title: `Revert ${sha.slice(0, 7)}`,
          confirmLabel: "Revert",
          description: (
            <p>
              A new commit is recorded that undoes “{c?.subject ?? subject ?? sha.slice(0, 7)}”.
              Nothing is rewritten; if the undo clashes with later changes, git gives up cleanly and
              says which files.
            </p>
          ),
          action: async () => {
            await ctx.run("Reverted", () =>
              post<GitResult>("/git/revert", { ref: sha }, { query: q }),
            )
          },
        }),
    })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        mono={false}
        title={c?.subject ?? subject ?? sha.slice(0, 7)}
        subtitle={
          c ? (
            <>
              <span className="font-mono">{c.short}</span> · {c.author} · {timestamp(c.at)}
              {c.isMerge ? " · merge" : ""}
            </>
          ) : (
            "loading…"
          )
        }
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {detail.error && <ErrorState error={detail.error} className="m-3" />}
        {detail.loading && !c && <LoadingRows className="p-3" rows={5} />}
        {c && (
          <div className="animate-rise">
            <div className="space-y-2 border-b border-hairline px-3 py-2.5">
              {c.refs && <RefTags refs={c.refs} />}
              {c.body && (
                <p className="text-xs leading-relaxed whitespace-pre-wrap text-muted-foreground">
                  {c.body}
                </p>
              )}
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
                <span className="numeric">
                  {c.changes.length} file{c.changes.length === 1 ? "" : "s"}
                </span>
                <span className="numeric font-mono">
                  <span className="text-(--git-added)">+{c.insertions}</span>{" "}
                  <span className="text-(--git-deleted)">−{c.deletions}</span>
                </span>
                {c.parents && c.parents.length > 0 && (
                  <span className="numeric font-mono">
                    parent{c.parents.length === 1 ? "" : "s"}{" "}
                    {c.parents.map((p, i) => (
                      <button
                        key={p}
                        type="button"
                        className="hover:text-foreground hover:underline"
                        onClick={() => ctx.onSelect({ kind: "commit", sha: p })}
                      >
                        {p.slice(0, 7)}
                        {i < c.parents!.length - 1 ? " " : ""}
                      </button>
                    ))}
                  </span>
                )}
              </div>
              <VerbBar verbs={verbs} />
            </div>

            <div className="sticky top-0 z-10 flex h-8 items-center gap-2 border-b border-hairline bg-card px-3">
              <span className="eyebrow">Files</span>
              <span className="flex-1" />
              <button
                type="button"
                aria-pressed={file === ""}
                onClick={() => setFile(file === "" ? null : "")}
                className={cn(
                  "rounded-sm px-1.5 py-0.5 text-hint transition-colors",
                  file === ""
                    ? "bg-accent text-foreground"
                    : "text-muted-foreground hover:bg-accent hover:text-foreground",
                )}
              >
                All files
              </button>
            </div>
            <ul className="divide-y divide-hairline">
              {c.changes.map((f) => (
                <ChangedFileRow
                  key={f.path}
                  file={f}
                  active={file === f.path}
                  onClick={() => setFile(file === f.path ? null : f.path)}
                  onHistory={() => ctx.onFileHistory(f.path)}
                />
              ))}
              {c.changes.length === 0 && (
                <li className="px-3 py-4 text-center text-hint text-muted-foreground">
                  This commit changed no files.
                </li>
              )}
            </ul>

            {file !== null && (
              <div className="border-t border-hairline">
                {!current && <LoadingRows className="p-3" rows={4} />}
                {current?.error && <p className="p-3 text-xs text-destructive">{current.error}</p>}
                {current?.body !== undefined && (
                  <DiffView
                    body={current.body || "No textual diff (binary file, or no line changes)."}
                    singleFile={file !== ""}
                    className="animate-rise"
                  />
                )}
              </div>
            )}
          </div>
        )}
      </div>

      <NameDialog
        open={naming === "branch"}
        onOpenChange={(o) => !o && setNaming(null)}
        title="Branch from this commit"
        label="Branch name"
        hint="Letters, digits, dots, dashes and slashes. You switch to it straight away."
        placeholder="fix/the-thing"
        facts={[{ label: "from", value: sha.slice(0, 7), mono: true }]}
        confirmLabel="Create and switch"
        onSubmit={async (name) => {
          await ctx.run(`Created ${name}`, () =>
            post<GitResult>("/git/branch", { ref: name, from: sha }, { query: q }),
          )
        }}
      />
      <NameDialog
        open={naming === "tag"}
        onOpenChange={(o) => !o && setNaming(null)}
        title="Tag this commit"
        label="Tag name"
        hint="v1.2.0 is the usual shape. Tags are local until they are pushed."
        placeholder="v1.0.0"
        facts={[{ label: "at", value: sha.slice(0, 7), mono: true }]}
        message={{
          label: "Message",
          hint: "With a message the tag records who made it and when — what a release wants. Leave it empty for a plain marker.",
          placeholder: "Release notes, or what this point is",
        }}
        confirmLabel="Create tag"
        onSubmit={async (name, message) => {
          await ctx.run(`Tagged ${name}`, () =>
            post<GitResult>("/git/tag", { name, ref: sha, message }, { query: q }),
          )
        }}
      />
    </div>
  )
}

function ChangedFileRow({
  file,
  active,
  onClick,
  onHistory,
}: {
  file: GitChangedFile
  active: boolean
  onClick: () => void
  onHistory: () => void
}) {
  const tone = CHANGE_TONE[file.status] ?? "modified"
  return (
    <li
      className={cn(
        "group flex min-w-0 items-center gap-2 px-3 py-1.5 transition-colors hover:bg-row-hover",
        active && "bg-accent",
      )}
      style={gitStyle(tone)}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="w-3 shrink-0 text-center font-mono text-micro font-medium text-(--git-colour)">
            {CHANGE_LETTER[file.status] ?? "M"}
          </span>
        </TooltipTrigger>
        <TooltipContent>
          {file.status}
          {file.from ? ` from ${file.from}` : ""}
        </TooltipContent>
      </Tooltip>
      <button
        type="button"
        onClick={onClick}
        aria-pressed={active}
        className={cn(
          "min-w-0 flex-1 truncate text-left font-mono text-xs focus-ring-inset hover:underline",
          file.status === "deleted" && "text-muted-foreground line-through",
        )}
      >
        {file.path}
      </button>
      {file.binary ? (
        <Tag>binary</Tag>
      ) : (
        <span className="numeric shrink-0 font-mono text-micro">
          <span className="text-(--git-added)">+{file.insertions}</span>{" "}
          <span className="text-(--git-deleted)">−{file.deletions}</span>
        </span>
      )}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            aria-label={`History of ${file.path}`}
            className="size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground"
            onClick={onHistory}
          >
            <ClockRewind className="size-3.5" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Every commit that touched this file</TooltipContent>
      </Tooltip>
    </li>
  )
}

/** What a stash holds, and the three things to do with it. */
function StashPreview({
  stash,
  ctx,
  onClose,
}: {
  stash: GitStash
  ctx: PreviewContext
  onClose: () => void
}) {
  const diff = usePoll(
    (signal) =>
      get<{ diff: string }>(
        "/git/stash/diff",
        { path: ctx.repoPath, index: stash.index },
        signal,
      ),
    0,
    [ctx.repoPath, stash.sha],
  )
  const q = { path: ctx.repoPath }
  const verbs: Verb[] = []
  if (ctx.canControl) {
    verbs.push({
      key: "apply",
      label: "Apply",
      detail: "Bring these changes back into the working tree and keep the stash.",
      icon: CornerUpLeft,
      inline: true,
      disabled: !!ctx.busy,
      run: () =>
        void ctx
          .run("Stash applied", () =>
            post<GitResult>("/git/stash/apply", { index: stash.index, pop: false }, { query: q }),
          )
          .then(onClose)
          .catch(() => undefined),
    })
    verbs.push({
      key: "pop",
      label: "Pop",
      detail: "Bring the changes back and drop the stash once they have applied cleanly.",
      icon: RotateCounterClockwise,
      inline: true,
      disabled: !!ctx.busy,
      run: () =>
        void ctx
          .run("Stash popped", () =>
            post<GitResult>("/git/stash/apply", { index: stash.index, pop: true }, { query: q }),
          )
          .then(onClose)
          .catch(() => undefined),
    })
  }
  if (ctx.canDestruct) {
    verbs.push({
      key: "drop",
      label: "Drop",
      detail: "Throw the stash away. The changes in it exist nowhere else.",
      icon: Cross,
      danger: true,
      disabled: !!ctx.busy,
      run: () =>
        ctx.confirm({
          title: `Drop stash ${stash.index}`,
          phrase: "drop stash",
          confirmLabel: "Drop",
          description: (
            <p className="text-destructive">
              “{stash.message}” is thrown away. The changes it holds are not recoverable.
            </p>
          ),
          action: async (c) => {
            await post("/git/stash/drop", { index: stash.index }, { confirm: c, query: q })
            ctx.onChanged()
            onClose()
          },
        }),
    })
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        mono={false}
        title={stash.message}
        subtitle={
          <>
            stash {stash.index}
            {stash.branch ? ` · on ${stash.branch}` : ""}
            {stash.at ? ` · ${relativeTime(stash.at)}` : ""}
          </>
        }
        onClose={onClose}
      />
      {verbs.length > 0 && (
        <div className="shrink-0 border-b border-hairline px-3 py-2">
          <VerbBar verbs={verbs} />
        </div>
      )}
      {diff.error && <ErrorState error={diff.error} className="m-3" />}
      {diff.loading && !diff.data && <LoadingRows className="p-3" rows={6} />}
      {diff.data && (
        <DiffView
          body={diff.data.diff || "This stash holds no textual changes."}
          className="min-h-0 flex-1 animate-rise"
        />
      )}
    </div>
  )
}

/** What one branch has that another does not. */
function ComparePreview({
  base,
  head,
  ctx,
  onClose,
}: {
  base: string
  head: string
  ctx: PreviewContext
  onClose: () => void
}) {
  const cmp = usePoll(
    (signal) => get<GitComparison>("/git/compare", { path: ctx.repoPath, base, head }, signal),
    0,
    [ctx.repoPath, base, head],
  )
  const d = cmp.data
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        title={`${base} ← ${head}`}
        subtitle={
          d ? (
            <>
              <span className="numeric">{d.ahead}</span> commit{d.ahead === 1 ? "" : "s"} to bring
              in{d.behind > 0 ? ` · ${base} is ${d.behind} ahead of ${head}` : ""} ·{" "}
              <span className="numeric">{d.files}</span> file{d.files === 1 ? "" : "s"} ·{" "}
              <span className="numeric font-mono">
                <span className="text-(--git-added)">+{d.insertions}</span>{" "}
                <span className="text-(--git-deleted)">−{d.deletions}</span>
              </span>
            </>
          ) : (
            "comparing…"
          )
        }
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {cmp.error && <ErrorState error={cmp.error} className="m-3" />}
        {cmp.loading && !d && <LoadingRows className="p-3" rows={5} />}
        {d && d.commits.length === 0 && (
          <EmptyState
            className="m-3"
            icon={GitMerge}
            title={`${base} already has everything on ${head}`}
          />
        )}
        {d && d.commits.length > 0 && (
          <ul className="animate-rise divide-y divide-hairline">
            {d.commits.map((c) => (
              <CommitRow
                key={c.sha}
                commit={c}
                onClick={() => ctx.onSelect({ kind: "commit", sha: c.sha, subject: c.subject })}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

/** A commit as one line, for the lists a preview draws. */
export function CommitRow({ commit: c, onClick }: { commit: GitCommit; onClick: () => void }) {
  return (
    <li className="min-w-0">
      <button
        type="button"
        onClick={onClick}
        className="flex w-full min-w-0 items-start gap-3 px-3 py-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <span className="min-w-0 flex-1">
          <span className="block truncate text-xs">{c.subject}</span>
          <span className="block truncate text-micro text-muted-foreground">
            <span className="font-mono">{c.short}</span> · {c.author} · {relativeTime(c.at)}
          </span>
        </span>
        {(c.insertions > 0 || c.deletions > 0) && (
          <span className="numeric mt-px shrink-0 font-mono text-micro">
            <span className="text-(--git-added)">+{c.insertions}</span>{" "}
            <span className="text-(--git-deleted)">−{c.deletions}</span>
          </span>
        )}
      </button>
    </li>
  )
}

const REVIEW_LABEL: Record<string, string> = {
  approved: "approved",
  changes_requested: "changes requested",
  review_required: "review required",
}

/** A pull request: its state on GitHub, its description, and the merge. */
function PullPreview({
  number,
  title,
  ctx,
  onClose,
}: {
  number: number
  title?: string
  ctx: PreviewContext
  onClose: () => void
}) {
  const pull = usePoll(
    (signal) =>
      get<GitPullRequest>(`/git/github/pulls/${number}`, { path: ctx.repoPath }, signal),
    30_000,
    [ctx.repoPath, number],
  )
  const [merging, setMerging] = useState(false)
  const p = pull.data
  const q = { path: ctx.repoPath }

  const verbs: Verb[] = [
    {
      key: "open",
      label: "Open on GitHub",
      detail: "The request's own page, with the conversation and the review.",
      icon: External,
      inline: true,
      run: () => window.open(p?.url, "_blank", "noopener"),
    },
  ]
  if (ctx.canControl && p && p.state === "open") {
    verbs.unshift({
      key: "merge",
      label: "Merge",
      detail: "Merge it into its base branch on GitHub, the way the button on its page does.",
      icon: GitMerge,
      inline: true,
      disabled: !!ctx.busy || p.draft || p.mergeable === "conflicting",
      run: () => setMerging(true),
    })
    verbs.push({
      key: "checkout",
      label: "Check out the branch",
      detail: `Fetch ${p.head} and switch this working tree to it, to try the change here.`,
      icon: BranchPlus,
      disabled: !!ctx.busy,
      run: () =>
        void ctx
          .run(`Checked out ${p.head}`, () =>
            post<GitResult>(`/git/github/pulls/${number}/checkout`, undefined, { query: q }).then(
              () => ({ command: "gh pr checkout", output: `On ${p.head}`, ok: true }),
            ),
          )
          .catch(() => undefined),
    })
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PreviewHeader
        mono={false}
        title={p?.title ?? title ?? `#${number}`}
        subtitle={
          p ? (
            <>
              #{p.number} · <span className="font-mono">{p.head}</span> →{" "}
              <span className="font-mono">{p.base}</span>
              {p.author ? ` · ${p.author}` : ""}
              {p.createdAt ? ` · ${relativeTime(p.createdAt)}` : ""}
            </>
          ) : (
            `#${number}`
          )
        }
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        {pull.error && <ErrorState error={pull.error} className="m-3" />}
        {pull.loading && !p && <LoadingRows className="p-3" rows={5} />}
        {p && (
          <div className="animate-rise space-y-3 px-3 py-2.5">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
              <Status
                tone={
                  p.state === "merged"
                    ? "running"
                    : p.state === "closed"
                      ? "stopped"
                      : p.draft
                        ? "notice"
                        : "running"
                }
                label={p.state === "open" && p.draft ? "draft" : p.state}
              />
              {p.checks && (
                <Status
                  tone={
                    p.checks === "success" ? "running" : p.checks === "failure" ? "danger" : "warning"
                  }
                  label={
                    p.checks === "success"
                      ? "checks passed"
                      : p.checks === "failure"
                        ? "checks failed"
                        : "checks running"
                  }
                />
              )}
              {p.review && REVIEW_LABEL[p.review] && (
                <Status
                  tone={
                    p.review === "approved"
                      ? "running"
                      : p.review === "changes_requested"
                        ? "danger"
                        : "notice"
                  }
                  label={REVIEW_LABEL[p.review]}
                />
              )}
              {p.mergeable === "conflicting" && <Status tone="danger" label="has conflicts" />}
              <span className="numeric text-hint text-muted-foreground">
                {p.files ?? 0} file{p.files === 1 ? "" : "s"} ·{" "}
                <span className="font-mono">
                  <span className="text-(--git-added)">+{p.additions ?? 0}</span>{" "}
                  <span className="text-(--git-deleted)">−{p.deletions ?? 0}</span>
                </span>
              </span>
            </div>
            <VerbBar verbs={verbs} />
            {p.body ? (
              <p className="text-xs leading-relaxed break-words whitespace-pre-wrap text-muted-foreground">
                {p.body}
              </p>
            ) : (
              <p className="text-hint text-muted-foreground">No description.</p>
            )}
          </div>
        )}
      </div>
      {p && (
        <MergePullDialog
          open={merging}
          onOpenChange={setMerging}
          repoPath={ctx.repoPath}
          pull={p}
          onMerged={() => {
            pull.refresh()
            ctx.onChanged()
          }}
        />
      )}
    </div>
  )
}
