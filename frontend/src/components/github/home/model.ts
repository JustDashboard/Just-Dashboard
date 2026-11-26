import { plural } from "@/lib/format"
import { githubRepoHref } from "@/lib/github"
import type {
  GitHubContributionDay,
  GitHubContributions,
  GitHubEvent,
  GitHubInboxFilter,
  GitHubInboxItem,
  GitHubRepository,
} from "@/lib/github-types"

/**
 * The GitHub page's home, in what needs no browser: how much of the
 * contribution calendar fits a width, where its month names go and what its
 * streaks are; which repositories lead the Overview; where a pull request or
 * issue from anywhere opens; and each activity event as a sentence. The
 * components in this folder draw these, and `model.test.js` holds them.
 */

/** The page's views, in the address as `?tab=`; the first is the bare address. */
export const HOME_TABS = ["overview", "repositories", "pulls", "issues"] as const
export type HomeTab = (typeof HOME_TABS)[number]

export const HOME_TAB_LABEL: Record<HomeTab, string> = {
  overview: "Overview",
  repositories: "Repositories",
  pulls: "Pull requests",
  issues: "Issues",
}

/** The calendar's cell and the gap between cells, in pixels: GitHub's own 10 + 3, a touch larger. */
export const CELL = 11
export const CELL_GAP = 3
/** The weekday names' column at the calendar's left. */
export const WEEKDAY_GUTTER = 28

/**
 * How many of the latest weeks fit `width`: the whole year on a desk, the
 * last few months on a phone, never a calendar that scrolls sideways (D12).
 * At least four weeks are drawn, so a narrow card still shows a month.
 */
export function weeksThatFit(width: number, total: number): number {
  if (width <= 0) return total
  const room = width - WEEKDAY_GUTTER
  const fit = Math.floor((room + CELL_GAP) / (CELL + CELL_GAP))
  return Math.max(Math.min(4, total), Math.min(total, fit))
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

/**
 * The month names over the calendar: one at the first week a month begins
 * in, as GitHub places them. A name closer than three columns to the one
 * before it is dropped, so two names never overlap at a window's edge.
 */
export function monthLabels(weeks: { days: GitHubContributionDay[] }[]) {
  const labels: { column: number; label: string }[] = []
  let last = -1
  weeks.forEach((week, column) => {
    const first = week.days[0]
    if (!first) return
    const month = Number(first.date.slice(5, 7)) - 1
    if (month === last) return
    last = month
    const previous = labels.at(-1)
    if (previous && column - previous.column < 3) return
    // A first column that is the tail of a month that began before the
    // calendar does would put a name over three days of it.
    if (column === 0 && Number(first.date.slice(8, 10)) > 7) return
    labels.push({ column, label: MONTHS[month] })
  })
  return labels
}

/**
 * The longest run of days with a contribution, and the run that ends today
 * (or yesterday: today is not over, and a streak does not break at
 * midnight). Days are read in calendar order.
 */
export function streaks(contributions: GitHubContributions | undefined) {
  const days = (contributions?.weeks ?? []).flatMap((week) => week.days)
  let longest = 0
  let run = 0
  for (const day of days) {
    run = day.count > 0 ? run + 1 : 0
    longest = Math.max(longest, run)
  }
  let current = 0
  for (let index = days.length - 1; index >= 0; index--) {
    const day = days[index]
    if (day.count > 0) current++
    else if (index === days.length - 1) continue
    else break
  }
  return { longest, current }
}

/** The busiest day in the calendar, when there was one. */
export function busiestDay(contributions: GitHubContributions | undefined) {
  let best: GitHubContributionDay | undefined
  for (const week of contributions?.weeks ?? []) {
    for (const day of week.days) if (day.count > (best?.count ?? 0)) best = day
  }
  return best
}

/** "3 Nov": a calendar day in the reader's words, without a timezone shift. */
export function dayWords(date: string): string {
  const [, month, day] = date.split("-").map(Number)
  return `${day} ${MONTHS[month - 1]}`
}

/** One cell's tooltip: "4 contributions on 3 Nov", "No contributions on 3 Nov". */
export function dayTitle(day: GitHubContributionDay): string {
  const count = day.count === 0 ? "No contributions" : plural(day.count, "contribution")
  return `${count} on ${dayWords(day.date)}`
}

/**
 * The repositories the Overview leads with: the latest pushed that are not
 * archived, as GitHub's own home lists them. Four by default, the most a
 * desk's row of cards holds.
 */
export function recentRepositories(repos: GitHubRepository[], count = 4): GitHubRepository[] {
  return repos
    .filter((repo) => !repo.archived)
    .sort((a, b) => stamp(b.pushedAt ?? b.updatedAt) - stamp(a.pushedAt ?? a.updatedAt))
    .slice(0, count)
}

function stamp(at: string | undefined) {
  return at ? Date.parse(at) : 0
}

/** The filters each list offers, in GitHub's own order and words. */
export const PULL_FILTERS: { key: GitHubInboxFilter; label: string }[] = [
  { key: "created", label: "Created" },
  { key: "review", label: "Review requests" },
  { key: "assigned", label: "Assigned" },
  { key: "mentioned", label: "Mentioned" },
]
export const ISSUE_FILTERS = PULL_FILTERS.filter((filter) => filter.key !== "review")

/**
 * The filter a listing's address names (`?filter=`), or Created when it
 * names none the listing has: Review requests is for pull requests only.
 */
export function inboxFilterOf(kind: "pull" | "issue", value: string | null): GitHubInboxFilter {
  const filters = kind === "pull" ? PULL_FILTERS : ISSUE_FILTERS
  return filters.find((filter) => filter.key === value)?.key ?? "created"
}

/** Open or closed, as a listing's address names it (`?state=`); open unless it says closed. */
export function inboxStateOf(value: string | null): "open" | "closed" {
  return value === "closed" ? "closed" : "open"
}

/**
 * When a row's request or issue last changed, in GitHub's own order of
 * words: "opened 2 days ago by mona" while it is open, "by mona was merged
 * 2 days ago" once it is not. The author is who opened it; search does not
 * say who closed or merged it, so the row never names them.
 */
export function inboxWhen(
  item: Pick<GitHubInboxItem, "state" | "createdAt" | "updatedAt" | "closedAt">,
): { open: boolean; verb: "opened" | "merged" | "closed"; at: string } {
  if (item.state === "open") return { open: true, verb: "opened", at: item.createdAt }
  return {
    open: false,
    verb: item.state === "merged" ? "merged" : "closed",
    at: item.closedAt ?? item.updatedAt,
  }
}

/**
 * Where a pull request or issue opens. One in a repository the page lists
 * opens in place, on that repository's own page with the request or issue
 * open (`?pull=`, `?issue=`), so the reader can act on it here. One anywhere
 * else — a contribution to somebody's public project — opens on GitHub.
 */
export function inboxHref(
  item: Pick<GitHubInboxItem, "repository" | "number" | "htmlUrl">,
  kind: "pull" | "issue",
  listed: Set<string>,
  connection: number,
): { href: string; external: boolean } {
  if (!listed.has(item.repository.toLowerCase())) return { href: item.htmlUrl, external: true }
  const page = githubRepoHref(item.repository, {
    account: connection,
    tab: kind === "pull" ? "pulls" : "issues",
  })
  return { href: `${page}&${kind}=${item.number}`, external: false }
}

/** An issue's state as GitHub colours it: open, closed as done, or closed as not planned. */
export type IssueState = "open" | "completed" | "not_planned"

export function issueStateOf(item: Pick<GitHubInboxItem, "state" | "stateReason">): IssueState {
  if (item.state === "open") return "open"
  return item.stateReason === "not_planned" ? "not_planned" : "completed"
}

export const ISSUE_STATE_WORD: Record<IssueState, string> = {
  open: "Open",
  completed: "Closed as completed",
  not_planned: "Closed as not planned",
}

/**
 * An activity event as the sentence GitHub's feed says it, split so the
 * page can set the object (a branch, a number, a title) apart from the verb.
 */
export function eventWords(event: GitHubEvent): { verb: string; object?: string } {
  const number = event.number ? `#${event.number}` : undefined
  const titled = [number, event.title].filter(Boolean).join(" ") || undefined
  switch (event.kind) {
    case "push":
      // GitHub's feed often leaves out how many commits a push carried.
      return {
        verb: event.commits ? `Pushed ${plural(event.commits, "commit")} to` : "Pushed to",
        object: event.ref,
      }
    case "pull":
      return { verb: `${capital(event.action ?? "opened")} pull request`, object: titled }
    case "issue":
      return { verb: `${capital(event.action ?? "opened")} issue`, object: titled }
    case "comment":
      return { verb: "Commented on", object: titled }
    case "review":
      return {
        verb: event.action === "approved" ? "Approved" : "Reviewed",
        object: titled,
      }
    case "create":
      return event.refType === "repository"
        ? { verb: "Created the repository" }
        : { verb: `Created ${event.refType ?? "branch"}`, object: event.ref }
    case "delete":
      return { verb: `Deleted ${event.refType ?? "branch"}`, object: event.ref }
    case "star":
      return { verb: "Starred" }
    case "fork":
      return { verb: "Forked" }
    case "release":
      return { verb: "Published release", object: event.title || event.ref }
    case "public":
      return { verb: "Made public" }
    case "member":
      return { verb: `${capital(event.action ?? "added")} a collaborator on` }
  }
}

function capital(word: string) {
  return word.charAt(0).toUpperCase() + word.slice(1)
}

/**
 * The website on a profile as a link: GitHub keeps it as typed, so
 * `mira.dev` gains its scheme and anything that is not http(s) is not a link.
 */
export function blogHref(blog: string | undefined): string | undefined {
  const value = blog?.trim()
  if (!value) return undefined
  const withScheme = /^[a-z][a-z0-9+.-]*:/i.test(value) ? value : `https://${value}`
  try {
    const url = new URL(withScheme)
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : undefined
  } catch {
    return undefined
  }
}

/** The website as it reads: without its scheme or a trailing slash. */
export function blogWords(href: string): string {
  return href.replace(/^https?:\/\//, "").replace(/\/$/, "")
}
