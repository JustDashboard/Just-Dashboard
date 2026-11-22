"use client"

import { useMemo } from "react"
import { useSearchParams } from "next/navigation"
import { Archive, LockClosed } from "@/components/icons"
import { ForgeFace } from "@/components/git/marks"
import { Rail, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import { githubRepoHref } from "@/lib/github"
import {
  GITHUB_LIST_DEFAULTS,
  GITHUB_LIST_FILTERS_KEY,
  GITHUB_SORT_LABEL,
  isNarrowed,
  narrowRepositories,
  type GitHubListFilters,
} from "@/lib/github-list"
import type { GitHubRepository, GitHubRepositoryList } from "@/lib/github-types"
import { useSessionState } from "@/lib/view-state"

/** The list's key for a repository: GitHub's names are case-insensitive. */
export const repoKey = (repo: Pick<GitHubRepository, "fullName">) => repo.fullName.toLowerCase()

/** The repositories one connection lists, read as the list page reads them, once a minute. */
export function useRepositoryList(connection: number | null) {
  const path = connection === null ? "" : `/github/connections/${connection}/repositories`
  return usePoll((signal) => get<GitHubRepositoryList>(path, undefined, signal), 60_000, [path], {
    enabled: connection !== null,
    key: connection === null ? undefined : path,
  })
}

/**
 * The list page's list for this account, read for the rail: the same
 * filters and order the list was left on (the session key both keep), so
 * the next repository is the one the list showed under this one.
 */
export function useRepoRail(connection: number | null) {
  const list = useRepositoryList(connection)
  const [filters, setFilters] = useSessionState<GitHubListFilters>(
    GITHUB_LIST_FILTERS_KEY,
    GITHUB_LIST_DEFAULTS,
  )
  const all = list.data?.repositories
  const rows = useMemo(() => narrowRepositories(all ?? [], filters), [all, filters])
  const narrowed = isNarrowed(filters)
  const state = filters.state !== "all" ? filters.state : ""
  const caption = !list.data
    ? "Reading the repositories…"
    : [
        narrowed
          ? `${rows.length} of ${all?.length ?? 0}`
          : plural(all?.length ?? 0, "repository", "repositories"),
        state || filters.owner || filters.language || GITHUB_SORT_LABEL[filters.sort].toLowerCase(),
      ].join(" · ")
  return {
    list,
    rows,
    keys: rows.map(repoKey),
    loading: list.loading && !list.data,
    error: list.data ? undefined : list.error,
    retry: list.refresh,
    query: filters.query,
    setQuery: (query: string) => setFilters({ ...filters, query }),
    filtered: narrowed,
    clearFilters: () =>
      setFilters({ ...GITHUB_LIST_DEFAULTS, sort: filters.sort }),
    caption,
  }
}

export function RepoRail({
  rail,
  connection,
  current,
  pinned,
}: {
  rail: ReturnType<typeof useRepoRail>
  connection: number | null
  current: string
  pinned?: GitHubRepository
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  return (
    <Rail
      slot="github-repo-rail"
      label="Repositories"
      search={{
        placeholder: "Name, description or topic",
        query: rail.query,
        onQuery: rail.setQuery,
      }}
      rows={rail.rows}
      keyOf={repoKey}
      current={(row) => repoKey(row) === current}
      pinned={pinned}
      row={(row, on) => (
        <RepoRailRow
          repo={row}
          current={on}
          href={githubRepoHref(row.fullName, { account: connection ?? undefined, tab })}
        />
      )}
      loading={rail.loading}
      loadingRows={8}
      error={rail.error}
      onRetry={rail.retry}
      empty="No repository matches."
      filtered={rail.filtered}
      onClearFilters={rail.clearFilters}
      caption={rail.caption}
    />
  )
}

function RepoRailRow({
  repo,
  current,
  href,
}: {
  repo: GitHubRepository
  current: boolean
  href: string
}) {
  return (
    <RailRow
      item={repoKey(repo)}
      name={repo.fullName}
      current={current}
      href={href}
      quiet={repo.archived}
      mark={<ForgeFace login={repo.owner.login} provider="github" className="size-7 rounded-md" />}
      end={
        repo.archived ? (
          <RailFigure mono={false} title="Archived">
            <Archive aria-label="Archived" className="size-3.5" />
          </RailFigure>
        ) : repo.private ? (
          <RailFigure mono={false} title={repo.visibility}>
            <LockClosed aria-label={repo.visibility} className="size-3.5" />
          </RailFigure>
        ) : undefined
      }
    >
      <RailName current={current}>{repo.name}</RailName>
      <RailLine>
        {[repo.owner.login, repo.language].filter(Boolean).join(" · ")}
      </RailLine>
    </RailRow>
  )
}
