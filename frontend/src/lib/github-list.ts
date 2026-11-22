import type { GitHubRepository } from "@/lib/github-types"

/**
 * How the GitHub page narrows and orders one account's repositories. The
 * list page and the repository page's rail read the same filters from the
 * same session key, so the rail is the list the reader left, in its order.
 */

export type GitHubListState = "all" | "public" | "private" | "archived"
export type GitHubListSort = "updated" | "name" | "created" | "stars" | "visibility"

export type GitHubListFilters = {
  query: string
  state: GitHubListState
  /** An owner's login, or "" for every owner. */
  owner: string
  /** A language, or "" for every language. */
  language: string
  sort: GitHubListSort
}

/** The `useSessionState` key both the list and the rail keep the filters under. */
export const GITHUB_LIST_FILTERS_KEY = "github.list.filters"

export const GITHUB_LIST_DEFAULTS: GitHubListFilters = {
  query: "",
  state: "all",
  owner: "",
  language: "",
  sort: "updated",
}

export const GITHUB_SORT_LABEL: Record<GitHubListSort, string> = {
  updated: "Recently updated",
  name: "Name",
  created: "Newest",
  stars: "Most stars",
  visibility: "Visibility",
}

/** Whether any filter narrows the list (the sort does not). */
export function isNarrowed(filters: GitHubListFilters): boolean {
  return (
    filters.query.trim() !== "" ||
    filters.state !== "all" ||
    filters.owner !== "" ||
    filters.language !== ""
  )
}

function inState(repo: GitHubRepository, state: GitHubListState): boolean {
  switch (state) {
    case "public":
      return repo.visibility === "public"
    case "private":
      return repo.visibility !== "public"
    case "archived":
      return repo.archived
    default:
      return true
  }
}

function matches(repo: GitHubRepository, needle: string): boolean {
  if (!needle) return true
  return [repo.fullName, repo.description ?? "", repo.language ?? "", ...(repo.topics ?? [])].some(
    (text) => text.toLowerCase().includes(needle),
  )
}

const time = (value: string | undefined) => (value ? Date.parse(value) || 0 : 0)
const byName = (a: GitHubRepository, b: GitHubRepository) =>
  a.fullName.localeCompare(b.fullName, undefined, { sensitivity: "base" })

const ORDER: Record<GitHubListSort, (a: GitHubRepository, b: GitHubRepository) => number> = {
  updated: (a, b) =>
    Math.max(time(b.pushedAt), time(b.updatedAt)) - Math.max(time(a.pushedAt), time(a.updatedAt)) ||
    byName(a, b),
  name: byName,
  created: (a, b) => time(b.createdAt) - time(a.createdAt) || byName(a, b),
  stars: (a, b) => b.stars - a.stars || byName(a, b),
  visibility: (a, b) => Number(a.private) - Number(b.private) || byName(a, b),
}

/** The repositories the filters keep, in the chosen order. Never mutates its input. */
export function narrowRepositories(
  repos: GitHubRepository[],
  filters: GitHubListFilters,
): GitHubRepository[] {
  const needle = filters.query.trim().toLowerCase()
  const owner = filters.owner.toLowerCase()
  return repos
    .filter(
      (repo) =>
        inState(repo, filters.state) &&
        (!owner || repo.owner.login.toLowerCase() === owner) &&
        (!filters.language || repo.language === filters.language) &&
        matches(repo, needle),
    )
    .sort(ORDER[filters.sort] ?? ORDER.updated)
}

/** What each state chip counts, over every repository (not the narrowed list). */
export function repositoryCounts(repos: GitHubRepository[]): Record<GitHubListState, number> {
  return {
    all: repos.length,
    public: repos.filter((r) => inState(r, "public")).length,
    private: repos.filter((r) => inState(r, "private")).length,
    archived: repos.filter((r) => r.archived).length,
  }
}

/** The owners in the list, the connected account's own first, then by count. */
export function ownersOf(
  repos: GitHubRepository[],
  login?: string,
): { login: string; type: string; count: number }[] {
  const counts = new Map<string, { login: string; type: string; count: number }>()
  for (const repo of repos) {
    const key = repo.owner.login.toLowerCase()
    const entry = counts.get(key) ?? { login: repo.owner.login, type: repo.owner.type, count: 0 }
    entry.count++
    counts.set(key, entry)
  }
  const self = login?.toLowerCase()
  return [...counts.values()].sort(
    (a, b) =>
      Number(b.login.toLowerCase() === self) - Number(a.login.toLowerCase() === self) ||
      b.count - a.count ||
      a.login.localeCompare(b.login),
  )
}

/** The languages in the list, most used first. */
export function languagesOf(repos: GitHubRepository[]): { name: string; count: number }[] {
  const counts = new Map<string, number>()
  for (const repo of repos) {
    if (repo.language) counts.set(repo.language, (counts.get(repo.language) ?? 0) + 1)
  }
  return [...counts.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
}
