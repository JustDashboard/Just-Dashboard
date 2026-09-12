"use client"

import { useMemo } from "react"
import { BranchPlus, Cross } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { GitGraph, GitGraphCommit } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { GitPreview } from "@/components/git/preview-panel"
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
  onSelectDiff,
}: {
  repoPath: string
  onClose: () => void
  onSelectDiff: (p: GitPreview) => void
}) {
  // A commit opens as a diff in the same column, replacing the graph — the same
  // move the history list makes, and the graph is one button away again.
  const show = async (c: GitGraphCommit) => {
    const subtitle = `${c.short} · ${c.author} · ${timestamp(c.at)}`
    onSelectDiff({
      kind: "diff",
      title: c.subject,
      subtitle: `${c.short} · loading…`,
      body: "Loading…",
    })
    try {
      const res = await get<{ diff: string }>("/git/diff", { path: repoPath, ref: c.sha })
      onSelectDiff({ kind: "diff", title: c.subject, subtitle, body: res.diff })
    } catch (err) {
      onSelectDiff({ kind: "diff", title: c.subject, subtitle: c.short, body: String(err) })
    }
  }

  const graph = usePoll(
    (signal) => get<GitGraph>("/git/graph", { path: repoPath, limit: 250 }, signal),
    0,
    [repoPath],
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

  if (graph.error) return <ErrorState error={graph.error} className="m-3" />
  if (graph.loading && !graph.data) return <LoadingRows className="p-3" rows={10} />

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PaneHeader className="gap-2 px-3">
        <BranchPlus className="size-3.5 shrink-0 text-primary" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-medium">Branch graph</p>
          <p className="truncate text-hint text-muted-foreground">
            {rows.length} commit{rows.length === 1 ? "" : "s"} across every local and remote branch
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

      {rows.length === 0 ? (
        <EmptyState className="m-3" icon={BranchPlus} title="No commits yet" />
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
    </div>
  )
}

function GraphRow({ commit, onClick }: { commit: GitGraphCommit; onClick: () => void }) {
  const refs = parseRefs(commit.refs)
  return (
    <button
      onClick={onClick}
      className="flex w-full items-center gap-2 pr-3 text-left hover:bg-row-hover"
      style={{ height: ROW }}
    >
      <span className="flex min-w-0 flex-1 items-center gap-1.5">
        {refs.map((r) => (
          <span
            key={r.label}
            className={
              r.kind === "tag"
                ? "shrink-0 rounded-sm bg-plot-warning px-1 text-micro font-medium text-warning"
                : r.kind === "head"
                  ? "shrink-0 rounded-sm bg-plot-success px-1 text-micro font-medium text-success"
                  : "shrink-0 rounded-sm bg-wash-primary px-1 text-micro font-medium text-primary"
            }
          >
            {r.label}
          </span>
        ))}
        <span className="truncate text-body">{commit.subject}</span>
      </span>
      <span className="shrink-0 font-mono text-hint text-muted-foreground">{commit.short}</span>
      <span className="hidden shrink-0 text-hint text-muted-foreground sm:block">
        {relativeTime(commit.at)}
      </span>
    </button>
  )
}

type Ref = { label: string; kind: "head" | "tag" | "branch" }

// git's %D reads "HEAD -> main, origin/main, tag: v1.0". The arrow marks the
// checked-out branch; "tag:" marks a tag; everything else is a branch tip.
function parseRefs(refs?: string): Ref[] {
  if (!refs) return []
  const out: Ref[] = []
  for (const raw of refs.split(", ")) {
    const entry = raw.trim()
    if (!entry) continue
    // origin/HEAD rides along with origin/main on the same commit — a symref,
    // not a branch worth its own chip.
    if (entry.endsWith("/HEAD")) continue
    if (entry.startsWith("HEAD -> ")) {
      out.unshift({ label: entry.slice("HEAD -> ".length), kind: "head" })
    } else if (entry === "HEAD") {
      out.unshift({ label: "HEAD", kind: "head" })
    } else if (entry.startsWith("tag: ")) {
      out.push({ label: entry.slice("tag: ".length), kind: "tag" })
    } else {
      out.push({ label: entry, kind: "branch" })
    }
  }
  return out
}
