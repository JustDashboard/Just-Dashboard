"use client"

import { useEffect, useMemo, useState } from "react"
import { Cross } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { GitBranch, GitGraph, GitGraphCommit } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { GitPreview } from "@/components/git/preview-panel"
import { SourceFork } from "@/components/git/glyphs"
import { RefTags } from "@/components/git/ref-tags"
import { HistoryPaging } from "@/components/git/inspect-panels"
import { SearchInput } from "@/components/page"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { PaneHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * The branch topology a hosted forge draws and a working copy hides: every
 * local and remote tip laid out in lanes, so "which branch came off which, and
 * where has it got to since" is one glance rather than a mental reconstruction
 * from `git log`.
 *
 * The lanes are assigned on the server (gitx.Graph) — the same rule the docker
 * and proxy renderers follow, one implementation of what the shape means. Here
 * we only draw: a lane is a coloured column, a commit is a dot on its lane, and
 * an edge runs from a commit down to each of its parents.
 */

const ROW = 34 // px per commit row — matches the history list's rhythm
const COL = 16 // px between lanes
const PAD = 14 // px from the left edge to lane 0

// Enough hues to tell adjacent lanes apart, each legible on the near-black and
// near-white surfaces this panel renders against. Lanes past the end wrap.
//
// The `--tag-*` tokens, which exist for exactly this: eight fixed hues, held at
// one lightness that holds up against both cards, and deliberately *not*
// computed from the palette — a lane's colour is an identity, and an identity
// that shifted with the theme would stop being the same lane. This file used to
// carry its own eight hex literals, which were the same idea arrived at twice.
const LANES = [
  "var(--tag-blue)",
  "var(--tag-green)",
  "var(--tag-amber)",
  "var(--tag-violet)",
  "var(--tag-red)",
  "var(--tag-cyan)",
  "var(--tag-pink)",
  "var(--tag-slate)",
]

const laneColour = (col: number) => LANES[col % LANES.length]

const x = (col: number) => PAD + col * COL
const y = (row: number) => row * ROW + ROW / 2

export function GraphPanel({
  repoPath,
  onClose,
  onSelect,
}: {
  repoPath: string
  onClose: () => void
  onSelect: (p: GitPreview) => void
}) {
  // A commit opens in the same column, replacing the graph — the same move
  // the history list makes, and the graph is one button away again.
  const show = (c: GitGraphCommit) => onSelect({ kind: "commit", sha: c.sha, subject: c.subject })

  const [search, setSearch] = useState("")
  const [term, setTerm] = useState("")
  const [ref, setRef] = useState("all")
  const [skip, setSkip] = useState(0)
  useEffect(() => {
    const timer = setTimeout(() => {
      setTerm(search.trim())
      setSkip(0)
    }, 300)
    return () => clearTimeout(timer)
  }, [search])
  const branches = usePoll(
    (signal) => get<GitBranch[]>("/git/branches", { path: repoPath }, signal),
    0,
    [repoPath],
  )

  const graph = usePoll(
    (signal) =>
      get<GitGraph>(
        "/git/graph",
        {
          path: repoPath,
          limit: 250,
          skip,
          search: term || undefined,
          ref: ref === "all" ? undefined : ref,
        },
        signal,
      ),
    0,
    [repoPath, skip, term, ref],
  )

  const rows = useMemo(() => graph.data?.commits ?? [], [graph.data])
  const index = useMemo(() => {
    const m = new Map<string, number>()
    rows.forEach((c, i) => m.set(c.sha, i))
    return m
  }, [rows])

  const gutter = x(Math.max(0, (graph.data?.lanes ?? 1) - 1)) + PAD

  const edges = useMemo(() => {
    const out: { d: string; colour: string; key: string }[] = []
    rows.forEach((c, i) => {
      for (const parent of c.parents ?? []) {
        const pj = index.get(parent)
        const px = pj === undefined ? x(c.col) : x(rows[pj].col)
        const py = pj === undefined ? (i + 1.5) * ROW : y(pj)
        // The edge takes the parent's colour: a line arriving in a lane belongs
        // to that lane, which is what makes a branch read as one continuous
        // colour from its tip down to where it forked.
        const colour = laneColour(pj === undefined ? c.col : rows[pj].col)
        const sx = x(c.col)
        const sy = y(i)
        const d =
          sx === px
            ? `M ${sx} ${sy} L ${px} ${py}`
            : `M ${sx} ${sy} C ${sx} ${sy + ROW * 0.45}, ${px} ${sy + ROW * 0.55}, ${px} ${Math.min(py, sy + ROW)} L ${px} ${py}`
        out.push({ d, colour, key: `${c.sha}-${parent}` })
      }
    })
    return out
  }, [rows, index])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PaneHeader className="gap-2 px-3">
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-medium">Branch graph</p>
          <p className="truncate text-hint text-muted-foreground">
            {rows.length} commit{rows.length === 1 ? "" : "s"} ·{" "}
            {ref === "all" ? "all branches and tags" : ref}
          </p>
        </div>
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
          <TooltipContent>Close the graph</TooltipContent>
        </Tooltip>
      </PaneHeader>

      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-3 py-2">
        <SearchInput
          dense
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          aria-label="Search graph commits"
          placeholder="Search messages…"
          containerClassName="min-w-0 flex-1 sm:w-auto"
        />
        <Select
          value={ref}
          onValueChange={(value) => {
            setRef(value)
            setSkip(0)
          }}
        >
          <SelectTrigger size="sm" aria-label="Graph branch" className="max-w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All branches and tags</SelectItem>
            {branches.data?.map((branch) => (
              <SelectItem key={branch.name} value={branch.name}>
                {branch.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {graph.error && <ErrorState error={graph.error} className="m-3" />}
      {graph.loading && !graph.data && <LoadingRows className="p-3" rows={10} />}

      {rows.length === 0 ? (
        <EmptyState className="m-3" icon={SourceFork} title="No commits yet" />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <div className="relative" style={{ minHeight: rows.length * ROW }}>
            <svg
              className="pointer-events-none absolute top-0 left-0"
              width={gutter}
              height={rows.length * ROW}
              aria-hidden
            >
              {edges.map((e) => (
                <path
                  key={e.key}
                  d={e.d}
                  fill="none"
                  stroke={e.colour}
                  strokeWidth={1.5}
                  opacity={0.8}
                />
              ))}
              {rows.map((c, i) => (
                <circle
                  key={c.sha}
                  cx={x(c.col)}
                  cy={y(i)}
                  r={c.isMerge ? 3 : 4}
                  fill={c.isMerge ? "var(--surface-header)" : laneColour(c.col)}
                  stroke={laneColour(c.col)}
                  strokeWidth={1.5}
                />
              ))}
            </svg>

            <div style={{ paddingLeft: gutter }}>
              {rows.map((c) => (
                <GraphRow key={c.sha} commit={c} onClick={() => show(c)} />
              ))}
            </div>
          </div>
        </div>
      )}
      <HistoryPaging
        start={skip}
        count={rows.length}
        hasMore={!!graph.data?.hasMore}
        busy={graph.loading}
        unit="commits"
        onPrevious={() => setSkip(Math.max(0, skip - 250))}
        onNext={() => setSkip(skip + 250)}
      />
    </div>
  )
}

function GraphRow({ commit, onClick }: { commit: GitGraphCommit; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="flex w-full items-center gap-2 pr-3 text-left focus-ring-inset transition-colors hover:bg-row-hover"
      style={{ height: ROW }}
    >
      <span className="flex min-w-0 flex-1 items-center gap-1.5">
        <RefTags refs={commit.refs} className="flex shrink-0 items-center gap-1" />
        <span className="truncate text-body">{commit.subject}</span>
      </span>
      <span className="shrink-0 font-mono text-hint text-muted-foreground">{commit.short}</span>
      <span className="hidden shrink-0 text-hint text-muted-foreground sm:block">
        {relativeTime(commit.at)}
      </span>
    </button>
  )
}
