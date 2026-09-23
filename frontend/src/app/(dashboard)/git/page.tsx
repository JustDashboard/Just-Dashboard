"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { CloudDownload, GitHubMark, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import type { GitRepo } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useGitHubAccount } from "@/hooks/use-github"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { Page, PageHeader, SearchInput, Toolbar } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import { GroupRule } from "@/components/flow"
import { CloneDialog } from "@/components/git/clone-dialog"
import { GitHelp } from "@/components/git/help"
import { GitHubAccountControl } from "@/components/git/github-account"
import { RepoRow } from "@/components/git/repo-row"
import { RepoWorkspace } from "@/components/git/repo-workspace"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"

type Filter = "all" | "dirty" | "behind" | "ahead" | "detached"

const FILTER_LABEL: Record<Filter, string> = {
  all: "All",
  dirty: "Uncommitted",
  behind: "Behind",
  ahead: "Unpushed",
  detached: "Detached",
}

/** A repository with anything outstanding: work, commits either way, or a detached HEAD. */
function waiting(repo: GitRepo): boolean {
  return repo.dirty || repo.ahead > 0 || repo.behind > 0 || repo.detached || Boolean(repo.gone)
}

/** Worst first, then most recently touched. The order *is* the page's answer. */
function byUrgency(a: GitRepo, b: GitRepo): number {
  const rank = (r: GitRepo) =>
    r.conflicts > 0
      ? 0
      : r.detached || r.gone
        ? 1
        : r.dirty
          ? 2
          : r.behind > 0
            ? 3
            : r.ahead > 0
              ? 4
              : 5
  if (rank(a) !== rank(b)) return rank(a) - rank(b)
  return (b.commitAt ?? "").localeCompare(a.commitAt ?? "")
}

/**
 * The repositories on this server, and which of them need something.
 *
 * **There are no tiles across the top, and that is deliberate.** §15 pass 2
 * puts a page's headline figures in a `StatGrid`, and this page had four —
 * repositories, uncommitted, behind, unpushed. They were removed on request,
 * and nothing was lost, because every one of those numbers is already on a
 * filter chip below: the chips answer "is anything waiting" *and* are the way
 * to act on the answer, where the tiles could only say it. What replaced the
 * tiles' other job — putting the urgent thing first — is the ordering and the
 * two groups: a repository with a conflict, a detached HEAD or uncommitted
 * work sorts above a clean one, and the rule over the first group says how
 * many need attention. Do not put the tiles back without reading this.
 *
 * The rows are cards rather than table cells, and the reason is in
 * `git/repo-row.tsx`. They are choices, not readings — every row is a
 * checkout to enter — so they carry the lit edge §16 gives to things you pick.
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
    return list
      .filter((r) => {
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
      .sort(byUrgency)
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

  // Grouped only where the grouping says something a chip has not: once a
  // filter is on, its name *is* the group, and a heading repeating it above a
  // single list is a rule with nothing on either side of it.
  const attention = visible.filter(waiting)
  const settled = visible.filter((r) => !waiting(r))
  const groups: { key: string; label: string; repos: GitRepo[] }[] =
    narrowed || attention.length === 0 || settled.length === 0
      ? [{ key: "all", label: "", repos: visible }]
      : [
          { key: "waiting", label: "Needs attention", repos: attention },
          { key: "settled", label: "Up to date", repos: settled },
        ]

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

      {repos.data?.available &&
        (list.length === 0 ? (
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
          <div className="flex min-w-0 flex-col gap-4">
            {/* The filters stand on the page rather than inside a panel
                header: with the readings gone there is no block above the list
                for them to belong to, and the list is the whole page. */}
            <Toolbar className="justify-between gap-x-4">
              <SearchInput
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter by name, path or branch"
              />
              {/* A state nothing is in does not get a chip: a filter that can
                  only ever return nothing is furniture. The counts on them are
                  what the four tiles used to say. */}
              <div className="flex min-w-0 flex-wrap items-center gap-1">
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
            </Toolbar>

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
              groups.map((group) => (
                <section key={group.key} className="flex min-w-0 flex-col gap-2">
                  {group.label && <GroupRule label={group.label} count={group.repos.length} />}
                  <ul aria-label={group.label || "Repositories"} className="min-w-0 space-y-2">
                    {group.repos.map((repo) => (
                      <RepoRow key={repo.path} repo={repo} onOpen={() => select(repo.path)} />
                    ))}
                  </ul>
                </section>
              ))
            )}
          </div>
        ))}

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
