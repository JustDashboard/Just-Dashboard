"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ArrowLeft,
  Archive,
  ChevronDoubleDown,
  ChevronDoubleUp,
  CloudUpload,
  CornerUpLeft,
  ClockRewind,
  FolderOpen,
  Link as LinkIcon,
  RefreshClockwise,
  Terminal,
  UserSettings,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { GitFileChange, GitRepo, GitResult, GitStash, GitStatus } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePanelSize } from "@/lib/panel-size"
import { usePoll } from "@/hooks/use-poll"
import { useGitHubAccount } from "@/hooks/use-github"
import { useAuth } from "@/hooks/use-auth"
import { workspaceTab } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { FileTree, type ConfirmRequest as TreeConfirmRequest } from "@/components/files/file-tree"
import { SourceBranch, SourceFork } from "@/components/git/glyphs"
import { branchLabel } from "@/components/git/marks"
import { AheadBehind } from "@/components/git/ahead-behind"
import { GitHelp } from "@/components/git/help"
import { ChangesPanel } from "@/components/git/changes-panel"
import { HistoryPanel } from "@/components/git/history-panel"
import { BranchesPanel } from "@/components/git/branches-panel"
import { GraphPanel } from "@/components/git/graph-panel"
import { GitHubAccountControl } from "@/components/git/github-account"
import { GitHubPanel } from "@/components/git/github-panel"
import { IdentityDialog } from "@/components/git/identity-dialog"
import { PreviewPanel, type GitPreview, type PreviewContext } from "@/components/git/preview-panel"
import { RemotesDialog } from "@/components/git/remotes-dialog"
import { WorktreesDialog } from "@/components/git/worktrees-dialog"
import { useGitRun } from "@/components/git/run"
import { ResizeHandle } from "@/components/resize-handle"
import { Page } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { VerbMenu, type Verb } from "@/components/verbs"

type Tab = "changes" | "history" | "branches" | "github"

// The preview is the column that needs the room — a diff is wider than a
// file name or a commit subject — so the other two start narrow and can be
// dragged wider; at 1280 with the sidebar open the diff still gets 400px.
const TREE = { min: 208, max: 480, base: 240 }
const WORK = { min: 300, max: 640, base: 368 }
// What the preview column may never be dragged below. A per-panel maximum is
// not a layout constraint: two panes each inside their own limit still add up
// to more than a 1280 row has, and the preview — `flex-1 min-w-0`, so it
// yields to everything — collapsed to nothing. This is the number that makes
// the row's arithmetic close.
const PREVIEW_MIN = 360

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v))

/**
 * One repository, as a place to work rather than a report to read.
 *
 * Three columns inside one frame, the shape every git client that people
 * actually enjoy has converged on: the file tree on the left, what-changed
 * and history in the middle, and whatever you last clicked — a diff, a
 * commit, a file — filling the right. One frame with two hairlines rather
 * than three framed panels with gutters between them: three boxes floating
 * on the page read as three things, and the screen is one working surface,
 * exactly as the terminal page draws its rail, emulator and tools.
 *
 * The strip across the top is the repository's reading and its verbs: where
 * you are, which branch, how far from its upstream, then fetch, pull and push
 * inline — the three pressed every hour — and everything else behind one
 * menu where each verb carries a sentence. Everything destructive goes
 * through the shared typed-confirmation dialog, and every unfamiliar word
 * carries its meaning one hover away (see help.tsx), so the same screen
 * serves someone committing for the first time and someone who has done it
 * ten thousand times.
 *
 * The address bar may open it on a pull request (`?pull=`) or on a tab
 * (`?tab=`): the list page's cards link into the GitHub tab that way, in the
 * same history entry as the checkout. Read with `useSearchParams` rather
 * than `useQuerySelection`, whose memory is per pathname — a pull request
 * remembered for `/git` would reopen on the next checkout entered. The word
 * in the address decides the tab only until the reader presses one: the tab
 * they chose is theirs, and it is what is remembered.
 */
export function RepoWorkspace({
  repo,
  onBack,
  onRepoChanged,
}: {
  repo: GitRepo
  onBack: () => void
  onRepoChanged: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const search = useSearchParams()
  const pullParam = search.get("pull")
  const activePull = pullParam && /^\d+$/.test(pullParam) ? Number(pullParam) : undefined
  const askedTab = activePull ? "github" : workspaceTab(search.get("tab"))
  const [tab, setTab] = useViewState<Tab>("git.repo.tab", "changes")
  // The tab the reader pressed on this visit, which outranks the address.
  const [pressed, setPressed] = useState<Tab>()
  const shown = pressed ?? askedTab ?? tab
  const pick = useCallback(
    (next: Tab) => {
      setPressed(next)
      setTab(next)
    },
    [setTab],
  )
  const [preview, setPreviewState] = useState<GitPreview | null>(() =>
    activePull ? { kind: "pull", number: activePull } : null,
  )
  const [graphOpen, setGraphOpen] = useState(false)
  const [historyFile, setHistoryFile] = useState<string>()
  const [remotesOpen, setRemotesOpen] = useState(false)
  const [identityOpen, setIdentityOpen] = useState(false)
  const [worktreesOpen, setWorktreesOpen] = useState(false)
  const [treeWidth, setTreeWidth, resetTreeWidth] = usePanelSize("git.tree", TREE.base)
  const [workWidth, setWorkWidth, resetWorkWidth] = usePanelSize("git.work", WORK.base)
  const [rowWidth, setRowWidth] = useState(0)

  // Opening a diff or a file takes the right column, so the graph steps aside.
  const setPreview = useCallback((p: GitPreview | null) => {
    setPreviewState(p)
    if (p) setGraphOpen(false)
  }, [])

  // What the three columns actually have between them, which is the only thing
  // that can say whether a stored width still fits. The row is measured rather
  // than assumed because the sidebar opens and closes beside it.
  //
  // A ref callback rather than an effect, because an effect runs *after* the
  // browser has painted: with a width left over from a wider monitor, one
  // frame of the collapsed layout would be drawn before the fit applied. React
  // flushes the state set here before paint, and runs the returned cleanup
  // when the node goes.
  const rowRef = useCallback((el: HTMLDivElement | null) => {
    if (!el) return
    setRowWidth(el.clientWidth)
    const observer = new ResizeObserver(() => setRowWidth(el.clientWidth))
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  // A width the operator asked for, reduced to one the row can honour. The
  // middle column is fitted first and the tree second, so when the row runs
  // out it is the file names that give way and not the work: the changes list
  // is what the screen is open for. Below `lg` the columns stack and neither
  // width is applied, so the numbers here are free to be nonsense there.
  const fitPanel = (want: number, self: { min: number; max: number }, other: number) => {
    if (!rowWidth) return clamp(want, self.min, self.max)
    return Math.max(self.min, Math.min(self.max, rowWidth - PREVIEW_MIN - other, want))
  }
  const workPx = fitPanel(workWidth, WORK, TREE.min)
  const treePx = fitPanel(treeWidth, TREE, workPx)

  const canControl = can("service.control")
  const canDestruct = can("destructive")
  const canWrite = can("file.write")
  const q = { path: repo.path }

  const status = usePoll(
    (signal) => get<GitStatus>("/git/status", { path: repo.path }, signal),
    12000,
    [repo.path],
  )
  const stashes = usePoll(
    (signal) => get<GitStash[]>("/git/stashes", { path: repo.path }, signal),
    0,
    [repo.path, status.data?.stashes ?? 0],
    { enabled: (status.data?.stashes ?? 0) > 0 },
  )
  const head = status.data?.repo ?? repo
  const changeCount = status.data?.files.length ?? 0

  // One poll for the whole workspace: the header chip, the identity the commit
  // box shows, and the GitHub tab are three views of the same answer.
  const github = useGitHubAccount(repo.path)

  const onChanged = useCallback(() => {
    status.refresh()
    onRepoChanged()
  }, [status, onRepoChanged])
  const { busy, run } = useGitRun(onChanged)

  // The tree badges changed files; git reports them relative to the repo root.
  const statusMap = useMemo(() => {
    const map: Record<string, GitFileChange> = {}
    for (const f of status.data?.files ?? []) map[`${repo.path.replace(/\/$/, "")}/${f.path}`] = f
    return map
  }, [repo.path, status.data])

  // The tree speaks the fullscreen-safe ConfirmRequest shape; here it just maps
  // onto the same typed-confirmation dialog everything else uses.
  const treeConfirm = (req: TreeConfirmRequest) =>
    confirm({
      title: req.title,
      description: req.body,
      confirmLabel: req.confirmLabel,
      action: async () => {
        await req.run()
      },
    })

  const branch = head.detached ? "" : head.branch
  const activeKey = previewKey(preview)

  const ctx: PreviewContext = {
    canAdmin: can("system.admin"),
    repoPath: repo.path,
    branch: branch || "HEAD",
    canWrite,
    canControl,
    canDestruct,
    busy,
    run,
    confirm,
    onChanged,
    onSelect: setPreview,
    onFileHistory: (path) => {
      setHistoryFile(path)
      pick("history")
    },
  }

  const stashCount = status.data?.stashes ?? 0
  const clean = Boolean(status.data?.clean)
  const more: Verb[] = []
  if (canControl) {
    more.push(
      {
        key: "stash",
        label: "Stash changes",
        icon: Archive,
        disabled: clean || !!busy,
        run: () =>
          void run("Stashed", () => post<GitResult>("/git/stash", {}, { query: q })).catch(
            () => undefined,
          ),
      },
      {
        key: "pop",
        label: "Pop the latest stash",
        icon: CornerUpLeft,
        disabled: stashCount === 0 || !!busy,
        run: () =>
          void run("Stash popped", () =>
            post<GitResult>("/git/stash/pop", undefined, { query: q }),
          ).catch(() => undefined),
      },
      {
        key: "tags",
        label: "Push all tags",
        icon: CloudUpload,
        disabled: !!busy,
        run: () =>
          void run("Pushed tags", () => post<GitResult>("/git/push/tags", {}, { query: q })).catch(
            () => undefined,
          ),
      },
      {
        key: "identity",
        label: "Who commits here",
        icon: UserSettings,
        run: () => setIdentityOpen(true),
      },
    )
  }
  more.push({
    key: "remotes",
    label: "Remotes",
    icon: LinkIcon,
    run: () => setRemotesOpen(true),
  })
  more.push({
    key: "worktrees",
    label: "Worktrees",
    icon: SourceBranch,
    run: () => setWorktreesOpen(true),
  })
  more.push({
    key: "recovery",
    label: "Recovery timeline",
    icon: ClockRewind,
    run: () => setPreview({ kind: "recovery" }),
  })
  if (canControl)
    more.push({
      key: "rebase",
      label: "Edit local history",
      icon: ClockRewind,
      disabled: !!busy || !!status.data?.operation,
      run: () => setPreview({ kind: "rebase" }),
    })
  more.push({
    key: "files",
    label: "Open in Files",
    icon: FolderOpen,
    run: () => router.push(`/files?path=${encodeURIComponent(repo.path)}`),
  })
  for (const item of [
    {
      kind: "forge" as const,
      label: "GitLab and Gitea",
    },
    {
      kind: "submodules" as const,
      label: "Submodules",
    },
    {
      kind: "lfs" as const,
      label: "Git LFS",
    },
    {
      kind: "exchange" as const,
      label: "Patch exchange",
    },
  ])
    more.push({
      key: item.kind,
      label: item.label,
      icon: SourceBranch,
      run: () => setPreview({ kind: item.kind }),
    })
  if (can("terminal")) {
    more.push({
      key: "shell",
      label: "Open a shell here",
      icon: Terminal,
      run: () => router.push(`/terminal?cwd=${encodeURIComponent(repo.path)}`),
    })
  }

  const operation = status.data?.operation

  return (
    <Page fill className="gap-3 px-2 py-2 md:px-3 md:py-3">
      {operation && (
        <Notice tone="warning" title={`A ${operation} is in progress`}>
          {operation === "bisect"
            ? "Finish the bisect in the terminal with git bisect reset."
            : "Resolve each conflicted file in Changes, then continue when the result is ready."}
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button size="xs" variant="outline" onClick={() => pick("changes")}>
              Show changes
            </Button>
            {operation !== "bisect" && canControl && (
              <Button
                size="xs"
                disabled={
                  !!busy || (status.data?.files.some((file) => file.label === "conflicted") ?? true)
                }
                onClick={() =>
                  void run("Operation continued", () =>
                    post<GitResult>("/git/operation/continue", {}, { query: q }),
                  ).catch(() => undefined)
                }
              >
                Continue {operation}
              </Button>
            )}
            {operation !== "bisect" && canDestruct && (
              <Button
                size="xs"
                variant="ghost"
                disabled={!!busy}
                onClick={() =>
                  confirm({
                    title: `Abort ${operation}`,
                    phrase: "abort operation",
                    confirmLabel: "Abort operation",
                    description:
                      "Return to the state before the operation started. Uncommitted conflict resolutions made since it started are discarded.",
                    action: async (phrase) => {
                      await run("Operation aborted", () =>
                        post<GitResult>("/git/operation/abort", {}, { query: q, confirm: phrase }),
                      )
                      setPreview(null)
                    },
                  })
                }
              >
                Abort
              </Button>
            )}
            {can("terminal") && (
              <Link
                href={`/terminal?cwd=${encodeURIComponent(repo.path)}`}
                className="text-foreground underline underline-offset-4"
              >
                Open a shell here
              </Link>
            )}
          </div>
        </Notice>
      )}

      {/* One frame around the whole workbench: a strip across the top, then
          the three columns separated by hairlines. */}
      <div
        style={{ "--jd-tree": `${treePx}px`, "--jd-work": `${workPx}px` } as React.CSSProperties}
        className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card"
      >
        <PaneHeader className="gap-2 px-2">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                size="sm"
                variant="ghost"
                className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
                aria-label="Back to repositories"
                onClick={onBack}
              >
                <ArrowLeft className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Back to all repositories</TooltipContent>
          </Tooltip>
          <div className="min-w-0">
            <p className="truncate text-body font-medium" title={repo.path}>
              {repo.name}
            </p>
            <p className="truncate font-mono text-micro text-muted-foreground" title={repo.path}>
              {repo.path}
            </p>
          </div>

          <span className="flex-1" />

          {/* Where HEAD is. The branch is a button into the Branches tab; the
              line under it says what it tracks, because "will this push go
              where I think" is decided by that and nowhere else. */}
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                onClick={() => pick("branches")}
                className="flex min-w-0 shrink items-center gap-1.5 text-left focus-ring-inset"
              >
                <SourceBranch
                  className={cn(
                    "size-3.5 shrink-0",
                    head.detached ? "text-destructive" : "text-muted-foreground",
                  )}
                />
                <span className="min-w-0">
                  <span
                    className={cn(
                      "block max-w-[14rem] truncate font-mono text-xs font-medium",
                      head.detached && "text-destructive",
                    )}
                  >
                    {branchLabel(head.branch, head.detached)}
                  </span>
                  <span className="hidden max-w-[14rem] truncate text-micro text-muted-foreground sm:block">
                    {head.empty
                      ? "no commits yet"
                      : head.detached
                        ? "detached HEAD"
                        : head.gone
                          ? `${head.upstream} is gone`
                          : head.upstream
                            ? `tracks ${head.upstream}`
                            : "no upstream — push publishes it"}
                  </span>
                </span>
              </button>
            </TooltipTrigger>
            <TooltipContent>
              {head.detached
                ? "HEAD is detached — switch or manage branches"
                : "Switch or manage branches"}
            </TooltipContent>
          </Tooltip>
          {head.detached && <Tag tone="danger">detached</Tag>}
          {head.gone && <Tag tone="danger">upstream gone</Tag>}
          <AheadBehind ahead={head.ahead} behind={head.behind} />

          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                size="sm"
                variant="outline"
                disabled={!!busy}
                pending={busy === "Fetched"}
                onClick={() =>
                  void run("Fetched", () =>
                    post<GitResult>("/git/fetch", undefined, { query: { ...q, prune: true } }),
                  ).catch(() => undefined)
                }
              >
                <RefreshClockwise className="size-4" />
                <span className="hidden md:inline">Fetch</span>
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Check the remote for new commits without changing your files
            </TooltipContent>
          </Tooltip>
          {canControl && (
            <>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={!!busy}
                    pending={busy === "Pulled"}
                    onClick={() =>
                      void run("Pulled", () =>
                        post<GitResult>("/git/pull", undefined, { query: q }),
                      ).catch(() => undefined)
                    }
                  >
                    <ChevronDoubleDown className="size-4" />
                    <span className="hidden md:inline">Pull</span>
                    {head.behind > 0 && <ChipCount>{head.behind}</ChipCount>}
                  </Button>
                </TooltipTrigger>
                <TooltipContent>
                  Bring the latest committed changes down from the remote (fast-forward only)
                </TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="sm"
                    disabled={!!busy}
                    pending={busy === "Pushed"}
                    onClick={() =>
                      void run("Pushed", () =>
                        post<GitResult>("/git/push", undefined, { query: q }),
                      ).catch(() => undefined)
                    }
                  >
                    <ChevronDoubleUp className="size-4" />
                    <span className="hidden md:inline">Push</span>
                    {head.ahead > 0 && <ChipCount>{head.ahead}</ChipCount>}
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Send your committed changes up to the remote</TooltipContent>
              </Tooltip>
            </>
          )}
          <VerbMenu verbs={more} label="More git actions" />
          <GitHubAccountControl repoPath={repo.path} status={github} compact />
          <GitHelp />
        </PaneHeader>

        {/* The three columns. Side by side they share the frame's height and
            each scrolls inside itself. Stacked on a small screen they cannot
            — three panes do not fit in a phone's viewport — so there the
            frame scrolls and each pane keeps a *definite* height of its own.
            Definite is the load-bearing word: a pane sized by its content
            puts the commit box below the fold of a page that does not
            scroll, which is exactly where it went. */}
        <div
          ref={rowRef}
          className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto lg:flex-row lg:overflow-hidden"
        >
          <div className="relative flex h-[15rem] shrink-0 flex-col border-b border-hairline lg:h-auto lg:min-h-0 lg:w-(--jd-tree) lg:border-r lg:border-b-0">
            <FileTree
              // Remount on a branch switch: a different branch can be a different
              // set of files, and a cached tree would keep showing the old one.
              key={head.branch}
              root={repo.path}
              statusMap={statusMap}
              canWrite={canWrite}
              canDelete={canDestruct}
              activeFile={preview?.kind === "file" ? preview.path : undefined}
              onOpenFile={(path) => setPreview({ kind: "file", path })}
              onConfirm={treeConfirm}
              onChanged={() => status.refresh()}
              onOpenInFiles={(path) => router.push(`/files?path=${encodeURIComponent(path)}`)}
            />
            <ResizeHandle
              side="left"
              label="File tree width"
              value={treePx}
              min={TREE.min}
              max={TREE.max}
              onChange={(px, commit) => setTreeWidth(clamp(px, TREE.min, TREE.max), commit)}
              onReset={resetTreeWidth}
              className="absolute inset-y-0 -right-1 z-20"
            />
          </div>

          <div className="relative flex h-[30rem] shrink-0 flex-col border-b border-hairline lg:h-auto lg:min-h-0 lg:w-(--jd-work) lg:border-r lg:border-b-0">
            <div className="flex h-9 shrink-0 [scrollbar-width:none] items-center overflow-x-auto border-b border-hairline px-1">
              <TabButton active={shown === "changes"} onClick={() => pick("changes")}>
                Changes
                {changeCount > 0 && <ChipCount>{changeCount}</ChipCount>}
              </TabButton>
              <TabButton active={shown === "history"} onClick={() => pick("history")}>
                History
              </TabButton>
              <TabButton active={shown === "branches"} onClick={() => pick("branches")}>
                Branches
              </TabButton>
              <TabButton active={shown === "github"} onClick={() => pick("github")}>
                GitHub
              </TabButton>
              <span className="flex-1" />
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-pressed={graphOpen}
                    aria-label="Branch graph"
                    className={cn(
                      "size-7 shrink-0 p-0",
                      graphOpen
                        ? "bg-accent text-foreground"
                        : "text-muted-foreground hover:text-foreground",
                    )}
                    onClick={() => setGraphOpen((v) => !v)}
                  >
                    <SourceFork className="size-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>See every branch and where it forked</TooltipContent>
              </Tooltip>
            </div>
            {shown === "changes" && (
              <ChangesPanel
                repoPath={repo.path}
                status={status}
                stashes={stashCount > 0 ? (stashes.data ?? []) : []}
                busy={busy}
                canControl={canControl}
                canDestruct={canDestruct}
                run={run}
                confirm={confirm}
                onSelect={setPreview}
                active={activeKey}
                onChanged={onChanged}
              />
            )}
            {shown === "history" && (
              <HistoryPanel
                // A commit, a reset or a switch changes what history is, so the
                // list is keyed on the tip and remounts when it moves.
                key={`${head.head ?? ""}:${head.branch}`}
                repoPath={repo.path}
                branch={branch || "HEAD"}
                file={historyFile}
                onClearFile={() => setHistoryFile(undefined)}
                busy={busy}
                canControl={canControl}
                canDestruct={canDestruct}
                run={run}
                confirm={confirm}
                onSelect={setPreview}
                active={activeKey}
                onChanged={onChanged}
              />
            )}
            {shown === "branches" && (
              <BranchesPanel
                key={`${head.head ?? ""}:${head.branch}`}
                repoPath={repo.path}
                current={branch || "HEAD"}
                busy={busy}
                canControl={canControl}
                canDestruct={canDestruct}
                run={run}
                confirm={confirm}
                onSelect={setPreview}
                onChanged={onChanged}
              />
            )}
            {shown === "github" && (
              <GitHubPanel
                repoPath={repo.path}
                branch={branch}
                github={github.data}
                busy={busy}
                canControl={canControl}
                canAdmin={can("system.admin")}
                run={run}
                confirm={confirm}
                onSelect={setPreview}
                active={activeKey}
                activePull={activePull}
                onChanged={onChanged}
              />
            )}
            <ResizeHandle
              side="left"
              label="Changes panel width"
              value={workPx}
              min={WORK.min}
              max={WORK.max}
              onChange={(px, commit) => setWorkWidth(clamp(px, WORK.min, WORK.max), commit)}
              onReset={resetWorkWidth}
              className="absolute inset-y-0 -right-1 z-20"
            />
          </div>

          <div
            data-slot="git-preview"
            className="flex h-[26rem] shrink-0 flex-col lg:h-auto lg:min-h-0 lg:min-w-0 lg:flex-1 lg:shrink"
          >
            {graphOpen ? (
              <GraphPanel
                repoPath={repo.path}
                onClose={() => setGraphOpen(false)}
                onSelect={setPreview}
              />
            ) : (
              <PreviewPanel preview={preview} ctx={ctx} onClose={() => setPreview(null)} />
            )}
          </div>
        </div>
      </div>
      <WorktreesDialog
        open={worktreesOpen}
        onOpenChange={setWorktreesOpen}
        repoPath={repo.path}
        canControl={canControl}
        canDestruct={canDestruct}
        canTerminal={can("terminal")}
        confirm={confirm}
        onChanged={onChanged}
      />
      <RemotesDialog
        open={remotesOpen}
        onOpenChange={setRemotesOpen}
        repoPath={repo.path}
        canControl={canControl}
        canDestruct={canDestruct}
        confirm={confirm}
        onChanged={onChanged}
      />
      <IdentityDialog
        open={identityOpen}
        onOpenChange={setIdentityOpen}
        repoPath={repo.path}
        identity={status.data?.identity}
        onSaved={onChanged}
      />
      {dialog}
    </Page>
  )
}

/** The key a list row compares itself against to read as the one open in the preview. */
function previewKey(p: GitPreview | null): string | undefined {
  if (!p) return undefined
  switch (p.kind) {
    case "partial":
      return `${p.staged ? "staged" : "unstaged"}:${p.file}`
    case "conflict":
      return `unstaged:${p.file}`
    case "commit":
      return `commit:${p.sha}`
    case "stash":
      return `stash:${p.stash.sha}`
    case "pull":
      return `pull:${p.number}`
    case "workflow":
      return `workflow:${p.id}`
    case "diff":
      return `${p.subtitle?.startsWith("staged") ? "staged" : "unstaged"}:${p.title}`
    default:
      return undefined
  }
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={tabClasses(active, "h-9")}
    >
      {children}
    </button>
  )
}
