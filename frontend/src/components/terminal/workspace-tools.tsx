"use client"

import { useEffect, useMemo, useState } from "react"
import { Cross, FloppyDisk, FolderOpen, GitMerge, SidebarRight } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, put } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { FileContent, GitDetect, GitFileChange, GitStatus } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { CodeEditor } from "@/components/code-editor"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { FileTree, type ConfirmRequest } from "@/components/files/file-tree"
import { DiffView } from "@/components/files/diff-view"
import { GitTools } from "@/components/terminal/git-tools"
import { Tag } from "@/components/tag"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Pane, PaneHeader } from "@/components/panel"

type Overlay =
  | { kind: "file"; path: string }
  | { kind: "diff"; title: string; subtitle?: string; body: string; singleFile?: boolean }
  | null

/**
 * The Files + Git companion for the terminal.
 *
 * It owns the git detection and status polls once for both tabs — the tree
 * borrows the status to badge changed files, and the git tab drives them — and
 * it owns every surface that would otherwise be a portalled dialog: the file
 * viewer/editor, the diff, and the confirm. They are drawn *inside* this panel
 * so the whole thing keeps working when the workspace is in the browser's real
 * fullscreen, where a portal to document.body renders outside the fullscreen
 * element and vanishes.
 */
export function WorkspaceTools({
  dir,
  onOpenInFiles,
  onClose,
}: {
  dir?: string
  onOpenInFiles: (path: string) => void
  onClose?: () => void
}) {
  const { can } = useAuth()
  // Which half of the companion you had open, kept across a navigation:
  // somebody working out of git is on that tab all afternoon.
  const [tab, setTab] = useViewState<"files" | "git">("terminal.tools.tab", "files")
  const [overlay, setOverlay] = useState<Overlay>(null)
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)

  /**
   * Switching tab puts the open file or diff away.
   *
   * The overlay covers the panel body, so without this a click on "Git" while
   * a file was open changed a tab nobody could see and read as a dead button.
   * Going to the other tab *is* leaving the file — there is one panel here,
   * not two — and the file is one click away in the tree either way.
   */
  const showTab = (next: "files" | "git") => {
    setOverlay(null)
    setTab(next)
  }

  const detect = usePoll(
    (signal) =>
      dir
        ? get<GitDetect>("/git/detect", { path: dir }, signal)
        : Promise.resolve({ available: true } as GitDetect),
    15000,
    [dir],
    { enabled: Boolean(dir) },
  )

  const repoPath = detect.data?.inRoots ? detect.data.repo?.path : undefined

  const status = usePoll(
    (signal) =>
      repoPath
        ? get<GitStatus>("/git/status", { path: repoPath }, signal)
        : Promise.resolve(null as unknown as GitStatus),
    8000,
    [repoPath],
    { enabled: Boolean(repoPath) },
  )

  // Absolute path → git status, so the tree can badge a changed file wherever
  // it sits. git reports paths relative to the repository root.
  const statusMap = useMemo(() => {
    const map: Record<string, GitFileChange> = {}
    if (repoPath && status.data?.files) {
      for (const f of status.data.files) map[`${repoPath.replace(/\/$/, "")}/${f.path}`] = f
    }
    return map
  }, [repoPath, status.data])

  const treeRoot = repoPath ?? dir
  const refreshGit = () => {
    detect.refresh()
    status.refresh()
  }

  const changed = status.data?.files.length ?? 0

  return (
    <Pane flush className="relative flex-1">
      {/* The two halves are section tabs: the brand underline says which one
          you are in, the way it does under the top bar. No glyph beside the
          word — a folder in front of "Files" is the label twice — and the one
          fact worth carrying across is the count of changed files, which is
          what decides whether the git half needs a visit. */}
      <PaneHeader className="gap-0 px-1 py-0">
        <TabButton
          active={tab === "files"}
          onClick={() => showTab("files")}
          hint="The files under the shell's working directory"
        >
          Files
        </TabButton>
        <TabButton
          active={tab === "git"}
          onClick={() => showTab("git")}
          hint="Stage, commit and push the repository the shell is in"
        >
          Git
          {changed > 0 && <ChipCount>{changed}</ChipCount>}
        </TabButton>
        <span className="flex-1" />
        {onClose && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                aria-label="Hide this panel"
                className="mr-1 size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
                onClick={onClose}
              >
                <SidebarRight className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Hide files &amp; git</TooltipContent>
          </Tooltip>
        )}
      </PaneHeader>

      <div className="relative flex min-h-0 flex-1 flex-col">
        {!treeRoot ? (
          <EmptyState
            className="m-3"
            icon={FolderOpen}
            title="No session"
            description="Open or pick a session and its files and git status show up here."
          />
        ) : (
          // Both tabs stay mounted, and the one you are not looking at is
          // hidden rather than unmounted. Unmounting threw away everything the
          // tree knew — which folders were open, where you had scrolled to —
          // so a glance at the git tab and back landed you at the top of a
          // collapsed tree, five clicks from where you were. Neither tab polls
          // on its own (the panel owns those requests), so the one out of
          // sight costs a render and nothing else.
          <>
            <div className={cn("flex min-h-0 flex-1 flex-col", tab !== "files" && "hidden")}>
              <FileTree
                key={treeRoot}
                root={treeRoot}
                statusMap={statusMap}
                canWrite={can("file.write")}
                canDelete={can("destructive")}
                activeFile={overlay?.kind === "file" ? overlay.path : undefined}
                onOpenFile={(path) => setOverlay({ kind: "file", path })}
                onConfirm={setConfirm}
                onChanged={refreshGit}
                onOpenInFiles={onOpenInFiles}
              />
            </div>
            <div className={cn("flex min-h-0 flex-1 flex-col", tab !== "git" && "hidden")}>
              <GitTools
                dir={dir ?? ""}
                detect={detect.data}
                detectLoading={detect.loading}
                detectError={detect.error}
                status={status}
                canControl={can("service.control")}
                canDestruct={can("destructive")}
                onShowDiff={(d) => setOverlay({ kind: "diff", ...d })}
                onConfirm={setConfirm}
                onChanged={refreshGit}
              />
            </div>
          </>
        )}

        {overlay?.kind === "file" && (
          <InlineFile
            // Keyed on the path so opening another file starts clean rather
            // than inheriting the previous file's unsaved draft.
            key={overlay.path}
            path={overlay.path}
            canWrite={can("file.write")}
            change={statusMap[overlay.path]}
            repoPath={repoPath}
            onClose={() => setOverlay(null)}
            onSaved={refreshGit}
          />
        )}
        {overlay?.kind === "diff" && (
          <InlineDiff
            title={overlay.title}
            subtitle={overlay.subtitle}
            body={overlay.body}
            singleFile={overlay.singleFile}
            onClose={() => setOverlay(null)}
          />
        )}
      </div>

      {confirm && <InlineConfirm request={confirm} onClose={() => setConfirm(null)} />}
    </Pane>
  )
}

function TabButton({
  active,
  onClick,
  hint,
  children,
}: {
  active: boolean
  onClick: () => void
  /** What the tab shows — the label is one word and the count beside it is
   *  the only other clue. */
  hint: string
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-current={active ? "page" : undefined}
          onClick={onClick}
          className={tabClasses(active, "h-9")}
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}

/**
 * The file viewer/editor, drawn over the panel body rather than in a sheet.
 *
 * A changed file can also be read as its diff, from the same place, without
 * going to the git tab and finding it in a list: opening a file you are in the
 * middle of editing and asking "what did I change here" is the same thought,
 * two seconds apart. The file is what opens — the diff is a toggle, because
 * most files are opened to be read or edited rather than reviewed.
 */
function InlineFile({
  path,
  canWrite,
  change,
  repoPath,
  onClose,
  onSaved,
}: {
  path: string
  canWrite: boolean
  /** This file's git status, when it has one and the shell is in a repo. */
  change?: GitFileChange
  repoPath?: string
  onClose: () => void
  onSaved: () => void
}) {
  const [file, setFile] = useState<FileContent>()
  const [draft, setDraft] = useState("")
  const [error, setError] = useState<Error>()
  const [saving, setSaving] = useState(false)
  const [showDiff, setShowDiff] = useState(false)
  // The diff, tagged with what it is a diff *of*. Holding the tag rather than
  // clearing the body when the request changes keeps every write to this state
  // inside the fetch's own callback: a synchronous reset in the effect is a
  // second render of a panel that is already re-rendering.
  const [diff, setDiff] = useState<{ key: string; body?: string; error?: Error }>()
  const [saves, setSaves] = useState(0)

  // The component is keyed on the path by its caller, so it mounts fresh for
  // each file and this only ever fetches — no synchronous reset needed.
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

  // A file that is only staged has its change in the index rather than the
  // working tree — asking for the wrong one returns an empty diff and reads
  // as "no changes" for a file the list says is modified. An untracked file
  // diffs too: the server shows it against nothing, as the addition it is.
  const diffable = Boolean(change && repoPath)
  const staged = Boolean(change?.staged && !change?.worktree)

  // Re-read on every entry to the diff, and after every save: the point of
  // reading it here is that you are editing the file, so a copy from before
  // the last write is exactly the wrong answer.
  const diffKey = `${path}|${staged}|${saves}`
  const current = diff?.key === diffKey ? diff : undefined

  useEffect(() => {
    if (!showDiff || !diffable || !repoPath) return
    const controller = new AbortController()
    const root = repoPath.replace(/\/$/, "")
    const relative = path.startsWith(root + "/") ? path.slice(root.length + 1) : path
    get<{ diff: string }>(
      "/git/diff",
      { path: repoPath, file: relative, staged: staged ? "true" : undefined },
      controller.signal,
    )
      .then((res) => setDiff({ key: diffKey, body: res.diff }))
      .catch((err) => !controller.signal.aborted && setDiff({ key: diffKey, error: err }))
    return () => controller.abort()
  }, [showDiff, diffable, repoPath, path, staged, diffKey])

  const dirty = file !== undefined && draft !== file.content

  const save = async () => {
    setSaving(true)
    try {
      await put("/files/write", { path, content: draft })
      notify.success("Saved", { description: path })
      setFile((f) => (f ? { ...f, content: draft } : f))
      setSaves((n) => n + 1)
      onSaved()
    } catch (err) {
      notify.error("Could not save", err)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="absolute inset-0 z-20 flex flex-col bg-card">
      <PaneHeader className="gap-2">
        <span className="min-w-0 flex-1 truncate font-mono text-xs" title={path}>
          {path.split("/").pop()}
        </span>
        {dirty && <Tag tone="warning">unsaved</Tag>}
        {change && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                size="xs"
                variant={showDiff ? "secondary" : "ghost"}
                aria-pressed={showDiff}
                className="shrink-0 gap-1 px-1.5 text-hint"
                onClick={() => setShowDiff((v) => !v)}
              >
                <GitMerge className="size-3.5" />
                Diff
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {showDiff
                ? "Back to the file"
                : staged
                  ? "Show what is staged for the next commit"
                  : change?.label === "untracked"
                    ? "Show the whole file as the addition it will be"
                    : "Show what changed since the last commit"}
            </TooltipContent>
          </Tooltip>
        )}
        {file && !file.binary && canWrite && !showDiff && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button size="xs" onClick={save} disabled={!dirty || saving} pending={saving}>
                <FloppyDisk className="size-3.5" />
                Save
              </Button>
            </TooltipTrigger>
            <TooltipContent>Write this file back to disk</TooltipContent>
          </Tooltip>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="sm"
              variant="ghost"
              className="size-6 p-0 text-muted-foreground hover:text-foreground"
              aria-label="Close"
              onClick={onClose}
            >
              <Cross className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Close this file (Esc)</TooltipContent>
        </Tooltip>
      </PaneHeader>
      <div className="min-h-0 flex-1">
        {showDiff ? (
          current?.error ? (
            <ErrorState error={current.error} className="m-3" />
          ) : current?.body === undefined ? (
            <LoadingRows className="p-3" rows={6} />
          ) : (
            <DiffView
              className="h-full"
              singleFile
              body={
                current.body ||
                (dirty
                  ? "Nothing committed yet for this file — the change you are looking at is unsaved."
                  : "No textual diff (a binary file, or a change git records without content).")
              }
            />
          )
        ) : (
          <>
            {error && <ErrorState error={error} className="m-3" />}
            {!file && !error && <LoadingRows className="p-3" rows={6} />}
            {file?.binary && (
              <div className="p-4 text-xs text-muted-foreground">
                This looks like a binary file ({bytes(file.size)}); it is not shown here.
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
          </>
        )}
      </div>
    </div>
  )
}

/** A unified diff, coloured, drawn over the panel body. */
function InlineDiff({
  title,
  subtitle,
  body,
  singleFile,
  onClose,
}: {
  title: string
  subtitle?: string
  body: string
  singleFile?: boolean
  onClose: () => void
}) {
  return (
    <div className="absolute inset-0 z-20 flex flex-col bg-card">
      <PaneHeader className="items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-xs" title={title}>
            {title}
          </p>
          {subtitle && <p className="truncate text-micro text-muted-foreground">{subtitle}</p>}
        </div>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="sm"
              variant="ghost"
              className="size-6 shrink-0 p-0 text-muted-foreground hover:text-foreground"
              aria-label="Close"
              onClick={onClose}
            >
              <Cross className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Close the diff (Esc)</TooltipContent>
        </Tooltip>
      </PaneHeader>
      <DiffView body={body} singleFile={singleFile} className="min-h-0 flex-1" />
    </div>
  )
}

/**
 * The confirm surface for the destructive things in this panel — deleting a
 * file, discarding a change. Non-portalled on purpose (fullscreen), and a plain
 * yes/no rather than a typed phrase: the panel supplies the phrase the server
 * still requires, and the file's name shown in the body is the thing being
 * confirmed. Esc cancels, Enter confirms.
 */
function InlineConfirm({ request, onClose }: { request: ConfirmRequest; onClose: () => void }) {
  const [busy, setBusy] = useState(false)
  const runIt = async () => {
    setBusy(true)
    try {
      await request.run()
      onClose()
    } catch (err) {
      notify.error("That did not go through", err)
      setBusy(false)
    }
  }
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
      if (e.key === "Enter") void runIt()
    }
    window.addEventListener("keydown", onKey, { capture: true })
    return () => window.removeEventListener("keydown", onKey, { capture: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return (
    <div className="absolute inset-0 z-30 flex items-center justify-center bg-background/70 p-3 backdrop-blur-sm">
      <div className="w-full max-w-sm rounded-xl border bg-card p-4 shadow-lg">
        <p className="text-body font-medium">{request.title}</p>
        {request.body && (
          <div className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{request.body}</div>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            size="sm"
            variant={request.danger ? "destructive" : "default"}
            onClick={runIt}
            pending={busy}
          >
            {request.confirmLabel ?? "Confirm"}
          </Button>
        </div>
      </div>
    </div>
  )
}
