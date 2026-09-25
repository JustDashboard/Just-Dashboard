"use client"

import { useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import { CloudDownload, GitHubMark, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import { assignPulls, byUrgency, shelve, type RepoShelf } from "@/lib/git-repos"
import type { GitPullRequest, GitPullRequestSummary, GitRepo } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useGitHubAccount } from "@/hooks/use-github"
import { closeRepo, openRepoPull, useQuerySelection } from "@/hooks/use-query-selection"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import { GroupRule } from "@/components/flow"
import { CloneDialog } from "@/components/git/clone-dialog"
import { GitHelp } from "@/components/git/help"
import { GitHubAccountControl } from "@/components/git/github-account"
import { ForgeFace } from "@/components/git/marks"
import { REPO_GRID, RepoCard } from "@/components/git/repo-card"
import { RepoWorkspace } from "@/components/git/repo-workspace"
import { ProductGlyph, hostProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"

type Filter = "all" | "dirty" | "behind" | "ahead" | "detached" | "pulls"

const FILTER_LABEL: Record<Filter, string> = {
  all: "All",
  dirty: "Uncommitted",
  behind: "Behind",
  ahead: "Unpushed",
  detached: "Detached",
  pulls: "Pull requests",
}

/** The search box matches a pull request by its number ("#12", "12") or by a word of its title. */
function matchesPull(pull: GitPullRequest, needle: string): boolean {
  const digits = needle.replace(/^#/, "")
  if (/^\d+$/.test(digits)) return String(pull.number).startsWith(digits)
  return pull.title.toLowerCase().includes(needle)
}

/**
 * The account a shelf belongs to, drawn as itself: its picture on github.com,
 * the forge's own mark elsewhere — the way the deploy chooser draws the
 * identity a repository is cloned through. A host that is no product's gets
 * nothing here and its name in the rule's detail instead.
 */
function ShelfMark({ shelf }: { shelf: RepoShelf }) {
  if (!shelf.host) return null
  if (shelf.host === "github.com" && shelf.owner) {
    return <ForgeFace login={shelf.owner} provider="github" size="xs" />
  }
  const product = hostProduct(shelf.host)
  return product ? <ProductGlyph id={product} /> : null
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
 * tiles' other job — putting the urgent thing first — is the order: on a
 * shelf, a repository with a conflict, a detached HEAD or uncommitted work
 * sorts above a clean one, and a shelf with something wrong on it sorts above
 * one with nothing. Do not put the tiles back without reading this.
 *
 * The checkouts are **shelved by where their code lives** — the account or
 * group their remote belongs to (`lib/git-repos.ts`), drawn as itself on the
 * rule — rather than listed worst-first under *Needs attention* and *Up to
 * date*, which is what shipped first. Two rules over one column of identical
 * rows said only whether each row was tidy: a reader looking for a product's
 * three checkouts found them split across the two groups, a repository
 * checked out twice (a clone and its worktree) drew the same pull request
 * twice, and a row the width of the page spent most of it on nothing. A
 * shelf's cards are a grid — `git/repo-card.tsx` says what a card is — and
 * each pull request is drawn once, on the checkout that is on its branch.
 * The shelves stay under a filter: they say *where*, which a chip does not.
 *
 * The cards are choices, not readings — every one is a checkout to enter —
 * so they carry the lit edge §16 gives to things you pick.
 *
 * The chosen repository lives in the address bar (`?repo=`), so the browser's
 * back button leaves the workspace and a link into a checkout can be shared
 * — the compose stack page and the file manager both link here that way. A
 * pull request opened from a card travels the same way (`?pull=`), written
 * in the same history entry as the checkout it belongs to.
 *
 * What GitHub says is waiting on each checkout comes from a second poll,
 * answered by each checkout's own owner through gh: it is switched on by the
 * server having git at all, not by the dashboard's own GitHub sign-in, since
 * the account that answers for `/srv/app` is whoever owns `/srv/app`.
 */
export default function GitPage() {
  const { can } = useAuth()
  const [selected, select] = useQuerySelection("repo")
  const openedPull = useSearchParams().get("pull") ?? ""
  const [filter, setFilter] = useSessionState("git.query", "")
  const [state, setState] = useSessionState<Filter>("git.state", "all")
  const [cloning, setCloning] = useState(false)

  const repos = usePoll(
    (signal) => get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, signal),
    60000,
  )
  const github = useGitHubAccount()
  const summary = usePoll(
    (signal) => get<GitPullRequestSummary>("/git/pull-requests", undefined, signal),
    60000,
    [],
    { enabled: Boolean(repos.data?.available) },
  )

  const list = useMemo(() => repos.data?.repos ?? [], [repos.data])
  // What each card draws: the summary with every request on one card only.
  const pullsByPath = useMemo(() => assignPulls(list, summary.data?.repos), [list, summary.data])
  const counts = useMemo(
    () => ({
      all: list.length,
      dirty: list.filter((r) => r.dirty).length,
      behind: list.filter((r) => r.behind > 0).length,
      ahead: list.filter((r) => r.ahead > 0).length,
      detached: list.filter((r) => r.detached).length,
      pulls: list.filter((r) => (pullsByPath[r.path]?.pulls.length ?? 0) > 0).length,
    }),
    [list, pullsByPath],
  )

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const openPulls = (r: GitRepo) => pullsByPath[r.path]?.pulls ?? []
    return list
      .filter((r) => {
        if (state === "dirty" && !r.dirty) return false
        if (state === "behind" && r.behind === 0) return false
        if (state === "ahead" && r.ahead === 0) return false
        if (state === "detached" && !r.detached) return false
        if (state === "pulls" && openPulls(r).length === 0) return false
        if (!needle) return true
        return (
          r.name.toLowerCase().includes(needle) ||
          r.path.toLowerCase().includes(needle) ||
          r.branch.toLowerCase().includes(needle) ||
          openPulls(r).some((p) => matchesPull(p, needle))
        )
      })
      .sort(byUrgency)
  }, [list, filter, state, pullsByPath])
  const shelves = useMemo(() => shelve(visible), [visible])

  // A selected repository takes the whole page: the working copy is a place to
  // work, not a panel to peek at, and it needs the room for the tree, the
  // changes and a diff side by side. Every hook above runs first so the branch
  // here never changes the hook order. Until the list arrives the workspace is
  // opened on what the address bar says, so a deep link never flashes the
  // list first.
  const active = selected ? list.find((r) => r.path === selected) : undefined
  if (selected && (active || !repos.data)) {
    return active ? (
      <RepoWorkspace
        // A different pull request in the address is a different view of
        // the workspace: it opens on that request, and remounting is how
        // the preview column follows a back or forward between two of them.
        key={`${active.path}:${openedPull}`}
        repo={active}
        onBack={closeRepo}
        onRepoChanged={() => {
          repos.refresh()
          summary.refresh()
        }}
      />
    ) : (
      <Page fill className="px-2 py-2 md:px-3 md:py-3">
        <LoadingPanel rows={8} />
      </Page>
    )
  }

  const narrowed = filter.trim().length > 0 || state !== "all"
  const canClone = can("service.control")

  const controls = (
    <span className="ml-auto flex max-w-full flex-wrap items-center gap-2">
      {/* The connected identity matters before a repository is chosen too. */}
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
    </span>
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Workspace" title="Git" />

      {selected && repos.data && !active && (
        <ErrorState
          error={
            new Error(
              `${selected} is not a repository the dashboard can see. It may be outside the configured git roots, or it may have been removed.`,
            )
          }
        />
      )}
      {repos.error && <ErrorState error={repos.error} onRetry={repos.refresh} />}
      {repos.loading && !repos.data && <LoadingPanel />}

      {repos.data && !repos.data.available && (
        <EmptyState
          icon={GitHubMark}
          title="git is not installed on this host"
          description="Install git to manage repositories from here."
          action={controls}
        />
      )}

      {repos.data?.available &&
        (list.length === 0 ? (
          <EmptyState
            icon={GitHubMark}
            title="No repositories found"
            description="Nothing under the configured git roots. Clone one here, or set JD_GIT_ROOTS to point at where your projects live."
            action={controls}
          />
        ) : (
          <div className="flex min-w-0 flex-col gap-5">
            {/* The filters stand on the page rather than inside a panel
                header: with the readings gone there is no block above the
                shelves for them to belong to, and the shelves are the whole
                page. */}
            <Toolbar className="justify-between gap-x-4">
              <SearchInput
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter by name, path, branch or pull request"
              />
              {/* A state nothing is in does not get a chip: a filter that can
                  only ever return nothing is furniture. The counts on them are
                  what the four tiles used to say. */}
              <div className="flex min-w-0 flex-wrap items-center gap-1">
                {(["all", "dirty", "behind", "ahead", "detached", "pulls"] as const).map((key) =>
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
              {controls}
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
              shelves.map((shelf) => (
                <section key={shelf.key || "local"} className="flex min-w-0 flex-col gap-2.5">
                  <GroupRule
                    label={shelf.label}
                    count={shelf.repos.length}
                    leading={<ShelfMark shelf={shelf} />}
                    // The host is said once: by the picture or the mark
                    // where it is a product's, in words where it is not.
                    detail={
                      shelf.owner && shelf.host && !hostProduct(shelf.host)
                        ? shelf.host
                        : undefined
                    }
                  />
                  <ul aria-label={shelf.label} className={REPO_GRID}>
                    {shelf.repos.map((repo, index) => (
                      <RepoCard
                        key={repo.path}
                        index={index}
                        repo={repo}
                        pulls={pullsByPath[repo.path]}
                        onOpen={() => select(repo.path)}
                        onOpenPull={(number) => openRepoPull(repo.path, number)}
                        onPullsChanged={summary.refresh}
                      />
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
