"use client"

import { useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Check, GitBranch, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { AheadBehind } from "@/components/git/ahead-behind"
import { GitHelp } from "@/components/git/help"
import { GitHubAccountControl } from "@/components/git/github-account"
import { RepoWorkspace } from "@/components/git/repo-workspace"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export default function GitPage() {
  const [selected, setSelected] = useState<GitRepo | null>(null)
  const [filter, setFilter] = useState("")

  const repos = usePoll(
    (signal) => get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, signal),
    60000,
  )

  // Deep link: a compose stack whose directory is a checkout links straight
  // here. Derived rather than pushed into state, so the workspace opens on the
  // render the repository list arrives without a flash of the list first.
  const requested = useSearchParams().get("repo")
  const [dismissedDeepLink, setDismissedDeepLink] = useState(false)
  const deepLinked =
    requested && !dismissedDeepLink
      ? ((repos.data?.repos ?? []).find((r) => r.path === requested) ?? null)
      : null
  const active = selected ?? deepLinked

  const visible = useMemo(() => {
    const list = repos.data?.repos ?? []
    const needle = filter.trim().toLowerCase()
    if (!needle) return list
    return list.filter(
      (r) =>
        r.name.toLowerCase().includes(needle) ||
        r.path.toLowerCase().includes(needle) ||
        r.branch.toLowerCase().includes(needle),
    )
  }, [repos.data, filter])


  // A selected repository takes the whole page: the working copy is a place to
  // work, not a panel to peek at, and it needs the room for the tree, the
  // changes and a diff side by side. Every hook above runs first so the branch
  // here never changes the hook order.
  if (active) {
    return (
      <RepoWorkspace
        repo={active}
        onBack={() => {
          setSelected(null)
          setDismissedDeepLink(true)
        }}
        onRepoChanged={() => repos.refresh()}
      />
    )
  }

  return (
    <Page>
      <PageHeader
        eyebrow="Access"
        title="Git"
        actions={
          <>
            {/* Who this dashboard is to GitHub, before a repository is chosen:
                the question "will my push be mine" is asked here as often as
                inside a checkout, and the answer used to be a click away. */}
            <GitHubAccountControl />
            <GitHelp />
            <Button variant="outline" size="sm" onClick={() => repos.refresh()}>
              <RefreshClockwise className="size-4" />
              Rescan
            </Button>
          </>
        }
      />

      {repos.error && <ErrorState error={repos.error} />}
      {repos.loading && !repos.data && <LoadingPanel />}

      {repos.data && !repos.data.available && (
        <EmptyState
          icon={GitBranch}
          title="git is not installed on this host"
          description="Install git to manage repositories from here."
        />
      )}

      {repos.data?.available &&
        (repos.data.repos.length === 0 ? (
          <EmptyState
            icon={GitBranch}
            title="No repositories found"
            description="Nothing under the configured git roots. Set JD_GIT_ROOTS to point at where your projects live."
          />
        ) : (
          <Panel>
            <PanelHeader
              icon={GitBranch}
              title="Repositories"
            />
            <PanelToolbar>
              <SearchInput
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter by name, path or branch"
              />
            </PanelToolbar>
            <PanelBody flush>
              <Table containerClassName="max-h-[calc(100svh-20rem)]">
                <TableHeader className={stickyTableHeader}>
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
                      onActivate={() => setSelected(repo)}
                    >
                      <TableCell>
                        <div className="max-w-[22rem] min-w-0">
                          <RowLink onClick={() => setSelected(repo)}>{repo.name}</RowLink>
                          <p
                            className="truncate font-mono text-hint text-muted-foreground"
                            title={repo.path}
                          >
                            {repo.path}
                          </p>
                        </div>
                      </TableCell>
                      <TableCell>
                        <span
                          className={cn(
                            "inline-flex min-w-0 items-center gap-1.5 text-xs",
                            repo.detached && "text-destructive",
                          )}
                          title={repo.detached ? "Detached HEAD" : undefined}
                        >
                          <GitBranch
                            className={cn(
                              "size-3 shrink-0",
                              repo.detached ? "text-destructive" : "text-muted-foreground",
                            )}
                          />
                          <span className="truncate font-mono">{repo.branch || "—"}</span>
                        </span>
                      </TableCell>
                      <TableCell>
                        {repo.dirty ? (
                          <span className="numeric text-xs text-warning">
                            {repo.changes} change{repo.changes === 1 ? "" : "s"}
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                            <Check className="size-3" /> clean
                          </span>
                        )}
                      </TableCell>
                      <TableCell>
                        <AheadBehind ahead={repo.ahead} behind={repo.behind} showSynced />
                      </TableCell>
                      <TableCell>
                        <div className="max-w-[20rem] min-w-0">
                          <p className="truncate text-xs" title={repo.subject}>
                            {repo.subject || "—"}
                          </p>
                          <p className="truncate text-hint text-muted-foreground">
                            {repo.author}
                            {repo.commitAt ? ` · ${relativeTime(repo.commitAt)}` : ""}
                          </p>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                  {visible.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={5} className="p-0">
                        <EmptyState icon={GitBranch} title="No repository matches that filter" />
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </PanelBody>
          </Panel>
        ))}
    </Page>
  )
}
