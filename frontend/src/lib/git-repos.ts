import type { GitPullRequest, GitPullRequestSummary, GitRepo } from "@/lib/types"

/**
 * How the Git page arranges the checkouts on this host.
 *
 * One flat list of thirty cards answers "which of these needs me" by its
 * order and nothing else, and an operator reading it had to hold in their
 * head which three were the same product, which two were the same repository
 * checked out twice, and which were somebody else's. The list is shelved by
 * where the code lives — the account or group the remote belongs to — so the
 * product's checkouts sit together, and a repository cloned twice (a clone
 * and its worktree, say) draws its pull requests once.
 */

/** Where a checkout's remote lives: `github.com` / `Wayy01` / `api`. */
export type RemoteOrigin = {
  /** The forge, lowercased: `github.com`, `gitlab.example.com`. Empty for a path on this host. */
  host: string
  /** The account or group: `Wayy01`, `group/subgroup`. Empty when the remote names only a repository. */
  owner: string
  /** The repository, without `.git`. */
  name: string
}

/**
 * The three facts a remote URL carries, whichever way it is written: an
 * `https://` URL (with or without the credentials the server has already
 * scrubbed to `***`), an `ssh://` URL with or without a port, the scp shape
 * `git@github.com:owner/name.git`, or a path on this host.
 */
export function parseRemote(remote: string | undefined): RemoteOrigin | undefined {
  const raw = (remote ?? "").trim()
  if (!raw) return undefined

  let host = ""
  let path = raw
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(raw)) {
    try {
      const url = new URL(raw)
      host = url.hostname.toLowerCase()
      path = url.pathname
    } catch {
      return undefined
    }
  } else {
    // scp-style: everything before the first colon is the host, unless it is
    // a Windows drive or there is a slash before it (a path on this host).
    const colon = raw.indexOf(":")
    const slash = raw.indexOf("/")
    if (colon > 0 && (slash === -1 || colon < slash)) {
      host = raw.slice(0, colon).replace(/^[^@]*@/, "").toLowerCase()
      path = raw.slice(colon + 1)
    }
  }

  const segments = path
    .split("/")
    .filter(Boolean)
    .map((s) => decodeURIComponent(s))
  if (segments.length === 0) return undefined
  const last = segments[segments.length - 1].replace(/\.git$/i, "")
  const name = last || segments[segments.length - 1]
  const owner = segments.slice(0, -1).join("/")
  // A bare `file://` remote or a path: the directory it sits in is its owner,
  // drawn as a path, which is what it is.
  return { host, owner: host ? owner : owner && `/${owner}`, name }
}

/** A shelf on the Git page: every checkout whose remote belongs to one account. */
export type RepoShelf = {
  key: string
  /** The account or group, or the host when the remote names none, or "No remote". */
  label: string
  host: string
  owner: string
  repos: GitRepo[]
}

/** The shelf a checkout belongs on. Identical for every checkout of one account. */
export function shelfOf(repo: GitRepo): Omit<RepoShelf, "repos"> {
  const origin = parseRemote(repo.remote)
  if (!origin) return { key: "", label: "No remote", host: "", owner: "" }
  const owner = origin.owner || origin.host
  return {
    key: `${origin.host}/${owner}`.toLowerCase(),
    label: owner || "No remote",
    host: origin.host,
    owner: origin.owner,
  }
}

/** A repository with anything outstanding: work, commits either way, or a detached HEAD. */
export function waiting(repo: GitRepo): boolean {
  return repo.dirty || repo.ahead > 0 || repo.behind > 0 || repo.detached || Boolean(repo.gone)
}

/** How wrong a checkout is, lowest first: a conflict outranks everything. */
export function urgency(repo: GitRepo): number {
  if (repo.conflicts > 0) return 0
  if (repo.detached || repo.gone) return 1
  if (repo.dirty) return 2
  if (repo.behind > 0) return 3
  if (repo.ahead > 0) return 4
  return 5
}

/** Worst first, then most recently touched. The order *is* the page's answer. */
export function byUrgency(a: GitRepo, b: GitRepo): number {
  if (urgency(a) !== urgency(b)) return urgency(a) - urgency(b)
  return (b.commitAt ?? "").localeCompare(a.commitAt ?? "")
}

/**
 * The shelves, from a list already in the order the page wants its cards.
 *
 * A shelf takes the rank of its worst checkout, so the account with something
 * wrong in it comes first; between two that are equally wrong, the names
 * decide, with the shelf of remote-less checkouts last — a repository nobody
 * pushes anywhere is the one the reader is least likely to be looking for.
 */
export function shelve(repos: GitRepo[]): RepoShelf[] {
  const shelves = new Map<string, RepoShelf>()
  for (const repo of repos) {
    const head = shelfOf(repo)
    let shelf = shelves.get(head.key)
    if (!shelf) {
      shelf = { ...head, repos: [] }
      shelves.set(head.key, shelf)
    }
    shelf.repos.push(repo)
  }
  return [...shelves.values()].sort((a, b) => {
    const rank = (s: RepoShelf) => Math.min(...s.repos.map(urgency))
    if (rank(a) !== rank(b)) return rank(a) - rank(b)
    if (!a.key !== !b.key) return a.key ? -1 : 1
    return a.label.localeCompare(b.label, undefined, { sensitivity: "base" })
  })
}

export type RepoPulls = GitPullRequestSummary["repos"][number]

/**
 * Which card draws each pull request.
 *
 * gh answers for the repository on the forge, so two checkouts of one
 * repository — a clone and a worktree of it — are handed the same list, and
 * drawn on both the list said the request twice. Each is drawn once: on the
 * checkout that is *on* its branch, which is the checkout the request is
 * about, or else on the repository's first checkout by path. The rest of the
 * summary — who deploys the checkout, why gh could not answer — stays with
 * every checkout, since those are facts about the checkout itself.
 */
export function assignPulls(
  repos: GitRepo[],
  summary: RepoPulls[] | undefined,
): Record<string, RepoPulls> {
  const entries = new Map<string, RepoPulls>()
  for (const entry of summary ?? []) entries.set(entry.path, entry)

  const checkouts = new Map<string, GitRepo[]>()
  for (const repo of [...repos].sort((a, b) => a.path.localeCompare(b.path))) {
    const repository = entries.get(repo.path)?.repository
    if (!repository) continue
    checkouts.set(repository, [...(checkouts.get(repository) ?? []), repo])
  }

  const out: Record<string, RepoPulls> = {}
  for (const repo of repos) {
    const entry = entries.get(repo.path)
    if (!entry) continue
    const siblings = checkouts.get(entry.repository) ?? [repo]
    const drawnHere = (pull: GitPullRequest) => {
      const on = siblings.find((s) => !s.detached && s.branch === pull.head)
      return (on ?? siblings[0]).path === repo.path
    }
    out[repo.path] = { ...entry, pulls: entry.pulls.filter(drawnHere) }
  }
  return out
}
