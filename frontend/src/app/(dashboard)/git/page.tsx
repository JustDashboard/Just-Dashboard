"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { CloudDownload, GitHubMark, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useGitHubAccount } from "@/hooks/use-github"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { Page, PageHeader, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { AheadBehind } from "@/components/git/ahead-behind"
import { CloneDialog } from "@/components/git/clone-dialog"
import { GitHelp } from "@/components/git/help"
import { GitHubAccountControl } from "@/components/git/github-account"
import { RepoWorkspace } from "@/components/git/repo-workspace"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type Filter = "all" | "dirty" | "behind" | "ahead" | "detached"

const FILTER_LABEL: Record<Filter, string> = {
  all: "All",
  dirty: "Uncommitted",
  behind: "Behind",
  ahead: "Unpushed",
  detached: "Detached",
}

/**
 * The repositories on this server, and which of them need something.
 *
 * Drawn the way the host Overview is: four readings across the top — how
 * many checkouts, how many carry uncommitted work, how many have commits
 * waiting to pull and to push — then the list as a plain block with a title
 * and a hairline, its first column on the page's own edge. On a screen too
 * narrow for the table the same rows are drawn down the row rather than
 * across it, with nothing dropped.
 *
 * The chosen repository lives in the address bar (`?repo=`), so the browser's
 * back button leaves the workspace and a link into a checkout can be shared
 * — the compose stack page and the file manager both link here that way.
 */
export default function GitPage() {
  const { can } = useAuth()
  const [selected, select] = useQuerySelection("repo")
  const [filter, setFilter] = useSessionState("git.query", "")
  const [state, setState] = useSessionState<Filter>("git.state", "all")
  const [cloning, setCloning] = useState(false)

  const repos = usePoll(
    (signal) => get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, signal),
    60000,
  )
  const github = useGitHubAccount()

  const list = useMemo(() => repos.data?.repos ?? [], [repos.data])
  const counts = useMemo(
    () => ({
      all: list.length,
      dirty: list.filter((r) => r.dirty).length,
      behind: list.filter((r) => r.behind > 0).length,
      ahead: list.filter((r) => r.ahead > 0).length,
      detached: list.filter((r) => r.detached).length,
    }),
    [list],
  )

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return list.filter((r) => {
      if (state === "dirty" && !r.dirty) return false
      if (state === "behind" && r.behind === 0) return false
      if (state === "ahead" && r.ahead === 0) return false
      if (state === "detached" && !r.detached) return false
      if (!needle) return true
      return (
        r.name.toLowerCase().includes(needle) ||
        r.path.toLowerCase().includes(needle) ||
        r.branch.toLowerCase().includes(needle)
      )
    })
  }, [list, filter, state])

  // A selected repository takes the whole page: the working copy is a place to
  // work, not a panel to peek at, and it needs the room for the tree, the
  // changes and a diff side by side. Every hook above runs first so the branch
  // here never changes the hook order. Until the list arrives the workspace is
  // opened on what the address bar says, so a deep link never flashes the
  // list first.
  const active = selected ? list.find((r) => r.path === selected) : undefined
  if (selected && (active || !repos.data)) {
    return active ? (
      <RepoWorkspace repo={active} onBack={() => select(null)} onRepoChanged={repos.refresh} />
    ) : (
      <Page fill className="px-2 py-2 md:px-3 md:py-3">
        <LoadingPanel rows={8} />
      </Page>
    )
  }

  const narrowed = filter.trim().length > 0 || state !== "all"
  const canClone = can("service.control")

  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Workspace"
        title="Git"
        actions={
          <>
            {/* Who this dashboard is to GitHub, before a repository is chosen:
                the question "will my push be mine" is asked here as often as
                inside a checkout. */}
            <GitHubAccountControl status={github} />
            <GitHelp />
            {canClone && repos.data?.available && (
              <Button size="sm" variant="outline" onClick={() => setCloning(true)}>
                <CloudDownload className="size-4" />
                Add repository
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => repos.refresh()}>
              <RefreshClockwise className="size-4" />
              Rescan
            </Button>
          </>
        }
      />

      {selected && repos.data && !active && (
        <ErrorState
          error={
            new Error(
              `${selected} is not a repository the dashboard can see. It may be outside the configured git roots, or it may have been removed.`,
            )
          }
        />
      )}
      {repos.error && <ErrorState error={repos.error} />}
      {repos.loading && !repos.data && <LoadingPanel />}

      {repos.data && !repos.data.available && (
        <EmptyState
          icon={GitHubMark}
          title="git is not installed on this host"
          description="Install git to manage repositories from here."
        />
      )}

      {repos.data?.available && (
        <>
          {/* Four readings, no frame: a run of figures is read as a row, and
              the question this page is opened with — is anything waiting —
              is answered here before the list is read. */}
          <StatGrid columns={4}>
            <StatTile
              label="Repositories"
              value={<Figure key={list.length}>{list.length}</Figure>}
              hint={list.length === 0 ? "none found under the git roots" : "under the git roots"}
            />
            <StatTile
              label="Uncommitted"
              value={<Figure key={counts.dirty}>{counts.dirty}</Figure>}
              tone={counts.dirty > 0 ? "warning" : "default"}
              hint={
                counts.dirty > 0
                  ? plural(
                      list.reduce((n, r) => n + r.changes, 0),
                      "changed file",
                    )
                  : "every working tree is clean"
              }
            />
            <StatTile
              label="Behind"
              value={<Figure key={counts.behind}>{counts.behind}</Figure>}
              tone={counts.behind > 0 ? "warning" : "default"}
              hint={
                counts.behind > 0
                  ? plural(
                      list.reduce((n, r) => n + r.behind, 0),
                      "commit",
                    ) + " waiting to pull"
                  : "nothing to pull"
              }
            />
            <StatTile
              label="Unpushed"
              value={<Figure key={counts.ahead}>{counts.ahead}</Figure>}
              hint={
                counts.ahead > 0
                  ? plural(
                      list.reduce((n, r) => n + r.ahead, 0),
                      "commit",
                    ) + " waiting to push"
                  : "everything is pushed"
              }
            />
          </StatGrid>

          {list.length === 0 ? (
            <EmptyState
              icon={GitHubMark}
              title="No repositories found"
              description="Nothing under the configured git roots. Clone one here, or set JD_GIT_ROOTS to point at where your projects live."
              action={
                canClone && (
                  <Button size="sm" onClick={() => setCloning(true)}>
                    <CloudDownload className="size-4" />
                    Add repository
                  </Button>
                )
              }
            />
          ) : (
            /* Plain: the list is the whole of the page under the readings,
               and a title with a hairline marks it. */
            <Panel>
              <PanelHeader title="Repositories" />
              <PanelToolbar>
                <SearchInput
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  placeholder="Filter by name, path or branch"
                />
                {/* A state nothing is in does not get a chip: a filter that
                    can only ever return nothing is furniture. */}
                <div className="flex min-w-0 flex-wrap gap-1">
                  {(["all", "dirty", "behind", "ahead", "detached"] as const).map((key) =>
                    key === "all" || counts[key] > 0 ? (
                      <FilterChip
                        key={key}
                        selected={state === key}
                        onClick={() => setState(key)}
                        className={
                          key === "detached" && counts.detached > 0
                            ? "text-destructive hover:text-destructive"
                            : undefined
                        }
                      >
                        {FILTER_LABEL[key]}
                        <ChipCount>{counts[key]}</ChipCount>
                      </FilterChip>
                    ) : null,
                  )}
                </div>
              </PanelToolbar>
              <PanelBody flush>
                {visible.length === 0 ? (
                  <EmptyState
                    icon={GitHubMark}
                    title="No repository matches"
                    description={narrowed ? "Clear the filter, or pick another state." : undefined}
                    action={
                      narrowed && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setFilter("")
                            setState("all")
                          }}
                        >
                          Clear filters
                        </Button>
                      )
                    }
                  />
                ) : (
                  <>
                    {/* Below `lg` the table is replaced rather than squeezed:
                        five columns in the space a sidebar leaves at 1024 is a
                        table somebody is reading the remains of. */}
                    <ul className="animate-rise divide-y divide-hairline lg:hidden">
                      {visible.map((repo) => (
                        <RepoListItem key={repo.path} repo={repo} onOpen={() => select(repo.path)} />
                      ))}
                    </ul>
                    {/* The outer columns take the gutter from their own cell padding,
                        so the first column starts in the title's column; the `-mx`
                        bleed that does the same on a plain panel is gated to it (§2). */}
                    <div className="group-data-[plain]/panel:-mx-4 hidden min-w-0 animate-rise lg:block">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead className="w-full">Repository</TableHead>
                            <TableHead>Branch</TableHead>
                            <TableHead>Working tree</TableHead>
                            <TableHead>Upstream</TableHead>
                            <TableHead>Last commit</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {visible.map((repo) => (
                            <TableRow
                              key={repo.path}
                              className="group"
                              onActivate={() => select(repo.path)}
                            >
                              <TableCell>
                                <div className="max-w-[24rem] min-w-0">
                                  <RowLink onClick={() => select(repo.path)}>{repo.name}</RowLink>
                                  <p
                                    className="truncate font-mono text-hint text-muted-foreground"
                                    title={repo.path}
                                  >
                                    {repo.path}
                                  </p>
                                </div>
                              </TableCell>
                              <TableCell>
                                <BranchCell repo={repo} />
                              </TableCell>
                              <TableCell>
                                <WorkingTree repo={repo} />
                              </TableCell>
                              <TableCell>
                                <Upstream repo={repo} />
                              </TableCell>
                              <TableCell>
                                <div className="max-w-[20rem] min-w-0">
                                  <p className="truncate text-xs" title={repo.subject}>
                                    {repo.empty ? (
                                      <span className="text-muted-foreground">no commits yet</span>
                                    ) : (
                                      repo.subject || "—"
                                    )}
                                  </p>
                                  <p className="truncate text-hint text-muted-foreground">
                                    {repo.author}
                                    {repo.commitAt ? ` · ${relativeTime(repo.commitAt)}` : ""}
                                  </p>
                                </div>
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </div>
                  </>
                )}
              </PanelBody>
            </Panel>
          )}
        </>
      )}

      <CloneDialog
        open={cloning}
        onOpenChange={setCloning}
        github={github.data}
        onDone={(path) => {
          repos.refresh()
          select(path)
        }}
      />
    </Page>
  )
}

/** A figure that rises once when its value lands — keyed by the caller so it remounts. */
function Figure({ children }: { children: React.ReactNode }) {
  return <span className="inline-block animate-rise">{children}</span>
}

function BranchCell({ repo }: { repo: GitRepo }) {
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-[12rem] items-center gap-1.5 font-mono text-xs",
        repo.detached && "text-destructive",
      )}
      title={repo.detached ? "Detached HEAD" : repo.upstream ? `tracks ${repo.upstream}` : undefined}
    >
      <span className="truncate">{repo.branch || "—"}</span>
      {repo.gone && <Tag tone="danger">gone</Tag>}
    </span>
  )
}

/** The working tree's state as a reading: a count with its breakdown, or clean. */
function WorkingTree({ repo }: { repo: GitRepo }) {
  if (!repo.dirty) return <span className="text-xs text-muted-foreground">clean</span>
  const parts: string[] = []
  if (repo.staged > 0) parts.push(`${repo.staged} staged`)
  if (repo.untracked > 0) parts.push(`${repo.untracked} new`)
  if (repo.conflicts > 0) parts.push(`${repo.conflicts} conflicted`)
  return (
    <span className="min-w-0">
      <span
        className={cn(
          "numeric block text-xs",
          repo.conflicts > 0 ? "text-destructive" : "text-warning",
        )}
      >
        {plural(repo.changes, "change")}
      </span>
      {parts.length > 0 && (
        <span className="block truncate text-hint text-muted-foreground">{parts.join(" · ")}</span>
      )}
    </span>
  )
}

function Upstream({ repo }: { repo: GitRepo }) {
  if (repo.detached || repo.empty) return <span className="text-xs text-muted-foreground">—</span>
  if (!repo.upstream) return <span className="text-xs text-muted-foreground">not published</span>
  if (repo.gone) return <span className="text-xs text-destructive">upstream gone</span>
  return <AheadBehind ahead={repo.ahead} behind={repo.behind} showSynced />
}

/** The same repository, on a screen too narrow for five columns. */
function RepoListItem({ repo, onOpen }: { repo: GitRepo; onOpen: () => void }) {
  return (
    <li>
      <div
        onClick={(event) => {
          if ((event.target as HTMLElement).closest("a, button")) return
          onOpen()
        }}
        className={cn(
          "group min-w-0 space-y-1 px-4 py-3 transition-colors hover:bg-row-hover",
          ROW_BLEED,
        )}
      >
        <div className="flex min-w-0 items-start gap-2">
          <div className="min-w-0 flex-1">
            <button
              type="button"
              onClick={onOpen}
              className="block max-w-full truncate rounded-sm text-left text-body leading-tight font-medium focus-ring hover:underline"
            >
              {repo.name}
            </button>
            <p className="truncate font-mono text-hint text-muted-foreground" title={repo.path}>
              {repo.path}
            </p>
          </div>
          <Upstream repo={repo} />
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5">
          <BranchCell repo={repo} />
          <WorkingTree repo={repo} />
          <span className="min-w-0 truncate text-hint text-muted-foreground">
            {repo.empty ? "no commits yet" : repo.subject}
            {repo.commitAt ? ` · ${relativeTime(repo.commitAt)}` : ""}
          </span>
        </div>
      </div>
    </li>
  )
}
