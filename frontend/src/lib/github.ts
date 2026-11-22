import { parseRemote } from "@/lib/git-repos"
import type { GitRepo } from "@/lib/types"
import type { GitHubSetup } from "@/lib/github-types"

/**
 * The GitHub page's addresses, and how a repository there is matched to a
 * checkout on this server.
 *
 * Two address spaces meet here. The page's own routes are
 * `/github/<owner>/<name>?account=<connection>&tab=<view>` and the API's are
 * `/github/connections/<connection>/repos/<owner>/<name>/…` under `/api/v1`.
 * Both are built only here, so an owner or name is escaped once.
 */

/** Where GitHub sends the browser back after a sign-in or creating the App. */
export const GITHUB_CALLBACK_PATH = "/github/callback"

/** The callback address for the dashboard as this browser reaches it. */
export function githubCallbackUrl(origin: string): string {
  return origin.replace(/\/+$/, "") + GITHUB_CALLBACK_PATH
}

/**
 * Whether GitHub would send a sign-in started from `origin` back here.
 * GitHub only redirects to a callback address registered on the App, so a
 * dashboard opened at another address (an IP, a tailnet name) cannot use the
 * web sign-in until that address is added on GitHub; the code sign-in works
 * from anywhere.
 */
export function githubCallbackRegistered(setup: GitHubSetup | undefined, origin: string): boolean {
  const wanted = githubCallbackUrl(origin).toLowerCase()
  return (setup?.app?.callbackUrls ?? []).some((url) => url.toLowerCase() === wanted)
}

function splitFullName(fullName: string): [string, string] {
  const slash = fullName.indexOf("/")
  if (slash <= 0 || slash === fullName.length - 1) throw new Error(`${fullName} is not owner/name`)
  return [fullName.slice(0, slash), fullName.slice(slash + 1)]
}

/** The API path of one repository through one connection, with `suffix` appended. */
export function githubRepoApi(connection: number, fullName: string, suffix = ""): string {
  const [owner, name] = splitFullName(fullName)
  return `/github/connections/${connection}/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}${suffix}`
}

/** The page of one repository. The first view and a missing account are the bare address. */
export function githubRepoHref(
  fullName: string,
  options: { account?: number; tab?: string } = {},
): string {
  const [owner, name] = splitFullName(fullName)
  const query = new URLSearchParams()
  if (options.account) query.set("account", String(options.account))
  if (options.tab && options.tab !== "overview") query.set("tab", options.tab)
  const search = query.toString()
  return `/github/${encodeURIComponent(owner)}/${encodeURIComponent(name)}${search ? `?${search}` : ""}`
}

const GITHUB_HOSTS = new Set(["github.com", "www.github.com", "ssh.github.com"])

/**
 * The repository a remote points at on github.com, as `owner/name` in lower
 * case, whichever way the remote is written (https, ssh, scp, with or without
 * `.git` and scrubbed credentials). Anything else — another forge, a path on
 * this host, a remote with no owner — is undefined. GitHub's names are case
 * insensitive, so the key is too.
 */
export function githubRemoteKey(remote: string | undefined): string | undefined {
  const origin = parseRemote(remote)
  if (!origin || !GITHUB_HOSTS.has(origin.host) || !origin.owner || origin.owner.includes("/")) {
    return undefined
  }
  return `${origin.owner}/${origin.name}`.toLowerCase()
}

/**
 * The checkouts on this server of one GitHub repository, matched by remote
 * rather than by folder name: a folder called `api` is not `acme/api` unless
 * its remote says so. Main checkouts come before linked worktrees.
 */
export function localCheckoutsOf(repos: GitRepo[] | undefined, fullName: string): GitRepo[] {
  const key = fullName.toLowerCase()
  return (repos ?? [])
    .filter((repo) => githubRemoteKey(repo.remote) === key)
    .sort(
      (a, b) =>
        Number(Boolean(a.worktree)) - Number(Boolean(b.worktree)) || a.path.localeCompare(b.path),
    )
}

/** The Local Git page opened on one checkout. */
export function localGitHref(path: string): string {
  return `/git?repo=${encodeURIComponent(path)}`
}
