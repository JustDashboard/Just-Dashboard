"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import {
  ArrowUpRight,
  CheckCircle,
  ChevronDoubleDown,
  ChevronDoubleUp,
  ChevronDown,
  GitBranch,
} from "@/components/icons"
import { get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { splitDiff, type FileDiff } from "@/lib/diff-files"
import type { GitDetect, GitFileChange, GitStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { DiffView } from "@/components/files/diff-view"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"

/** More untracked files than this are listed without their contents. */
const UNTRACKED_READ = 20
/** A file whose diff is longer than this starts folded. */
const LONG = 160

type Section = {
  change: GitFileChange
  staged?: FileDiff
  unstaged?: FileDiff
}

/**
 * The work in the repository the shell is in, as its diff — nothing else.
 *
 * This column was a whole git client: staging, a commit box, history,
 * branches, stashes. The Git page is that client, a click away, and beside a
 * terminal the question is narrower — *what have I changed?* — which is
 * answered by reading the change, not by operating on it. So it is the
 * changed files, each with its status and its line counts, and each diff
 * under its name, folded where it is long. The staged and the unstaged halves
 * of a file are both shown, because both are the work.
 *
 * The file list is the status the panel already polls for the tree's badges;
 * the diffs are read again whenever that list changes, as two requests for
 * the whole tree plus one per untracked file, which git has no whole-tree
 * diff for.
 */
export function DiffTools({
  detect,
  detectLoading,
  detectError,
  status,
}: {
  detect?: GitDetect
  detectLoading: boolean
  detectError?: Error
  status: { data?: GitStatus | null; loading: boolean; error?: Error }
}) {
  const repo = detect?.inRoots ? detect.repo : undefined
  const files = useMemo(() => status.data?.files ?? [], [status.data])
  // Read the diffs again when what changed changes, not on a timer of their
  // own: the status poll is the clock.
  const signature = files.map((f) => `${f.index}${f.worktree}${f.path}`).join("|")

  const fetchDiffs = useCallback(
    async (signal: AbortSignal) => {
      if (!repo) return null
      const path = repo.path
      const untracked = files.filter((f) => f.label === "untracked").slice(0, UNTRACKED_READ)
      const [unstaged, staged, added] = await Promise.all([
        get<{ diff: string }>("/git/diff", { path }, signal),
        get<{ diff: string }>("/git/diff", { path, staged: "true" }, signal),
        Promise.all(
          untracked.map((f) => get<{ diff: string }>("/git/diff", { path, file: f.path }, signal)),
        ),
      ])
      return {
        unstaged: [...splitDiff(unstaged.diff), ...added.flatMap((d) => splitDiff(d.diff))],
        staged: splitDiff(staged.diff),
      }
    },
    // The signature stands in for the list's contents.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [repo?.path, signature],
  )
  const diffs = usePoll(fetchDiffs, 0, [repo?.path, signature], { enabled: Boolean(repo) })

  const sections = useMemo<Section[]>(
    () =>
      files.map((change) => ({
        change,
        staged: diffs.data?.staged.find((d) => d.path === change.path),
        unstaged: diffs.data?.unstaged.find((d) => d.path === change.path),
      })),
    [files, diffs.data],
  )
  const totals = sections.reduce(
    (sum, s) => ({
      additions: sum.additions + (s.staged?.additions ?? 0) + (s.unstaged?.additions ?? 0),
      deletions: sum.deletions + (s.staged?.deletions ?? 0) + (s.unstaged?.deletions ?? 0),
    }),
    { additions: 0, deletions: 0 },
  )
  // Folded is the exception, so the set holds what the reader turned from
  // its default rather than every section's state.
  const [flipped, setFlipped] = useState<Set<string>>(new Set())
  const [allOpen, setAllOpen] = useState<boolean | null>(null)
  const isOpen = (s: Section) => {
    const lines =
      (s.staged?.body.split("\n").length ?? 0) + (s.unstaged?.body.split("\n").length ?? 0)
    const base = allOpen ?? (lines <= LONG && sections.length <= 12)
    return flipped.has(s.change.path) ? !base : base
  }
  const toggle = (path: string) =>
    setFlipped((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  const setAll = (open: boolean) => {
    setAllOpen(open)
    setFlipped(new Set())
  }

  if (detectError) return <ErrorState error={detectError} className="m-3" />
  if (detectLoading && !detect) return <LoadingRows className="p-3" rows={5} />
  if (!detect?.available) {
    return (
      <EmptyState
        className="m-3"
        icon={GitBranch}
        title="git is not installed"
        description="Install git on the host and the shell's changes show up here."
      />
    )
  }
  if (!repo) {
    return (
      <EmptyState
        className="m-3"
        icon={GitBranch}
        title="Not in a repository"
        description="cd into a git checkout and what you have changed there shows up here."
      />
    )
  }

  const branch = status.data?.repo.branch ?? repo.branch
  const ahead = status.data?.repo.ahead ?? repo.ahead
  const behind = status.data?.repo.behind ?? repo.behind

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-3 py-1.5">
        <ProductGlyph id="git" />
        <span className="min-w-0 truncate font-mono text-xs text-foreground" title={repo.path}>
          {branch || "detached"}
        </span>
        {(ahead > 0 || behind > 0) && (
          <span className="numeric shrink-0 text-hint text-muted-foreground">
            {ahead > 0 && `↑${ahead}`} {behind > 0 && `↓${behind}`}
          </span>
        )}
        <span className="flex-1" />
        <Link
          href={`/git?repo=${encodeURIComponent(repo.path)}`}
          className="inline-flex shrink-0 items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring transition-colors hover:text-foreground"
        >
          Open in Git
          <ArrowUpRight aria-hidden className="size-3" />
        </Link>
      </div>

      {files.length === 0 ? (
        status.loading && !status.data ? (
          <LoadingRows className="p-3" rows={4} />
        ) : (
          <EmptyState
            className="m-3"
            icon={CheckCircle}
            title="Nothing changed"
            description="The working tree matches the last commit."
          />
        )
      ) : (
        <>
          <div className="flex min-h-9 shrink-0 items-center gap-3 border-b border-hairline px-3">
            <span className="numeric text-xs text-foreground">
              {files.length} {files.length === 1 ? "file" : "files"}
            </span>
            <span className="numeric text-xs font-medium text-success">+{totals.additions}</span>
            <span className="numeric text-xs font-medium text-destructive">
              −{totals.deletions}
            </span>
            <span className="flex-1" />
            <IconAction label="Unfold every file" className="size-7" onClick={() => setAll(true)}>
              <ChevronDoubleDown />
            </IconAction>
            <IconAction label="Fold every file" className="size-7" onClick={() => setAll(false)}>
              <ChevronDoubleUp />
            </IconAction>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {diffs.error && !diffs.data && <ErrorState error={diffs.error} className="m-3" />}
            <ul aria-label="Changed files" className="divide-y divide-hairline">
              {sections.map((section) => (
                <FileSection
                  key={section.change.path}
                  section={section}
                  open={isOpen(section)}
                  loading={!diffs.data}
                  onToggle={() => toggle(section.change.path)}
                />
              ))}
            </ul>
          </div>
        </>
      )}
    </div>
  )
}

/** The letter git would print, in the colour of what it means. */
function statusLetter(change: GitFileChange) {
  if (change.label === "untracked") return "U"
  const letter = change.index.trim() || change.worktree.trim() || "M"
  return letter
}

const LETTER: Record<string, string> = {
  M: "text-warning",
  A: "text-success",
  U: "text-success",
  D: "text-destructive",
  R: "text-[var(--tag-cyan)]",
  C: "text-[var(--tag-cyan)]",
}

function FileSection({
  section,
  open,
  loading,
  onToggle,
}: {
  section: Section
  open: boolean
  loading: boolean
  onToggle: () => void
}) {
  const { change, staged, unstaged } = section
  const letter = statusLetter(change)
  const slash = change.path.lastIndexOf("/")
  const dir = slash >= 0 ? change.path.slice(0, slash + 1) : ""
  const name = change.path.slice(slash + 1)
  const additions = (staged?.additions ?? 0) + (unstaged?.additions ?? 0)
  const deletions = (staged?.deletions ?? 0) + (unstaged?.deletions ?? 0)
  const both = Boolean(staged && unstaged)

  return (
    <li className="min-w-0">
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        title={change.from ? `${change.from} → ${change.path}` : change.path}
        className="sticky top-0 z-10 flex w-full min-w-0 items-center gap-2 bg-card px-3 py-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <ChevronDown
          aria-hidden
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            !open && "-rotate-90",
          )}
        />
        <span
          className={cn(
            "w-3 shrink-0 font-mono text-xs font-semibold",
            LETTER[letter] ?? "text-muted-foreground",
          )}
        >
          {letter}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-xs">
          <span className="text-muted-foreground">{dir}</span>
          <span className={cn("text-foreground", letter === "D" && "line-through")}>{name}</span>
        </span>
        {(additions > 0 || deletions > 0) && (
          <span className="numeric flex shrink-0 gap-1.5 text-hint font-medium">
            {additions > 0 && <span className="text-success">+{additions}</span>}
            {deletions > 0 && <span className="text-destructive">−{deletions}</span>}
          </span>
        )}
      </button>
      {open && (
        <div className="min-w-0 border-t border-hairline bg-surface-sunken">
          {loading ? (
            <LoadingRows className="p-3" rows={3} />
          ) : !staged && !unstaged ? (
            <p className="px-3 py-2 text-hint text-muted-foreground">
              {change.label === "untracked"
                ? "A new file — open it from Files to read it."
                : "No textual change (a binary file, or a mode change)."}
            </p>
          ) : (
            <>
              {staged && (
                <Half label={both ? "Staged" : undefined}>
                  <DiffView body={staged.body} singleFile lineNumbers />
                </Half>
              )}
              {unstaged && (
                <Half label={both ? "Not staged" : undefined}>
                  <DiffView body={unstaged.body} singleFile lineNumbers />
                </Half>
              )}
            </>
          )}
        </div>
      )}
    </li>
  )
}

function Half({ label, children }: { label?: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      {label && <p className="eyebrow px-3 pt-2 pb-1">{label}</p>}
      {children}
    </div>
  )
}
