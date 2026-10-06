export const SEARCH_SCOPES = [
  ["all", "Everything"],
  ["page", "Pages"],
  ["command", "Commands"],
  ["recent", "Recent"],
  ["project", "Projects"],
  ["site", "Domains & sites"],
  ["database", "Databases"],
  ["container", "Containers"],
  ["stack", "Stacks"],
  ["repo", "Repositories"],
  ["service", "Services"],
  ["app", "PM2 apps"],
  ["backup", "Backups"],
  ["board", "Boards"],
] as const

export type SearchScope = (typeof SEARCH_SCOPES)[number][0]
export type SearchKind = Exclude<SearchScope, "all">
export type SearchItem = {
  id: string
  kind: SearchKind
  title: string
  detail?: string
  keywords?: string[]
  href?: string
}

const aliases: Record<string, SearchScope> = {
  ...Object.fromEntries(
    SEARCH_SCOPES.flatMap(([id]) => [
      [id, id],
      [`${id}s`, id],
    ]),
  ),
  db: "database",
  domain: "site",
  domains: "site",
  repository: "repo",
  repositories: "repo",
  pm2: "app",
  action: "command",
  actions: "command",
}

export function parseSearch(query: string): { scope: SearchScope; text: string } {
  const match = query.match(/^\s*([a-z][a-z0-9]*):\s*(.*)$/i)
  const scope = match && aliases[match[1].toLowerCase()]
  return scope ? { scope, text: match[2] } : { scope: "all", text: query.trim() }
}

export function scopeQuery(query: string, scope: SearchScope): string {
  const { text } = parseSearch(query)
  return scope === "all" ? text : `${scope}: ${text}`
}

function normalise(value: string) {
  return value
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/https?:\/\//g, "")
    .trim()
}

function words(value: string) {
  return value.split(/[^\p{L}\p{N}]+/u).filter(Boolean)
}

function oneEdit(a: string, b: string): boolean {
  if (a.length < 4 || a.length > 64 || Math.abs(a.length - b.length) > 1) return false
  if (a.length === b.length) {
    const diff = [...a].flatMap((char, i) => (char === b[i] ? [] : [i]))
    return (
      diff.length === 1 ||
      (diff.length === 2 &&
        diff[1] === diff[0] + 1 &&
        a[diff[0]] === b[diff[1]] &&
        a[diff[1]] === b[diff[0]])
    )
  }
  const [short, long] = a.length < b.length ? [a, b] : [b, a]
  let i = 0
  while (i < short.length && short[i] === long[i]) i++
  return short.slice(i) === long.slice(i + 1)
}

export function searchScore(item: SearchItem, query: string): number {
  const needle = normalise(query)
  if (!needle) return 1
  const title = normalise(item.title)
  const detail = normalise(item.detail ?? "")
  const keywords = (item.keywords ?? []).map(normalise)
  if (title === needle) return 1000
  if (keywords.includes(needle) || detail === needle) return 900
  const tokens = words(needle)
  if (tokens.length === 0) return 0
  const titleWords = words(title)
  const otherWords = words([detail, ...keywords].join(" "))
  let score = 0
  for (const token of tokens) {
    if (titleWords.includes(token)) score += 240
    else if (titleWords.some((word) => word.startsWith(token))) score += 200
    else if (title.includes(token)) score += 160
    else if ([detail, ...keywords].some((value) => value.includes(token))) score += 100
    else if (titleWords.some((word) => oneEdit(token, word))) score += 40
    else if (otherWords.some((word) => oneEdit(token, word))) score += 20
    else return 0
  }
  // Multi-word queries should not outrank an exact name just by having more words.
  return Math.min(800, score / tokens.length + (title.startsWith(needle) ? 100 : 0))
}

export function searchItems<T extends SearchItem>(items: T[], query: string, limit = 60) {
  const { scope, text } = parseSearch(query)
  const ranked = items
    .filter((item) => scope === "all" || item.kind === scope)
    .filter((item) => text || scope !== "all" || ["recent", "command", "page"].includes(item.kind))
    .map((item, order) => ({ item, order, score: searchScore(item, text) }))
    .filter(({ score }) => score > 0)
    .sort((a, b) => b.score - a.score || a.order - b.order)
  return { items: ranked.slice(0, limit).map(({ item }) => item), total: ranked.length }
}

/**
 * The title cut into the runs the query matched and the runs it did not, so
 * the row says *why* it is a result. Case and accents are ignored as the
 * ranking ignores them. A token is marked where it starts a word, which is
 * how it ranked; failing that, its first occurrence only — `s` lit in every
 * letter of "shop-stacks" marks nothing anybody needed to see. A row that
 * matched on its detail or by a typo marks nothing in its title.
 */
export function highlight(title: string, query: string): { text: string; hit: boolean }[] {
  const tokens = words(normalise(parseSearch(query).text))
  const chars = [...title]
  const owner: number[] = []
  let folded = ""
  chars.forEach((char, index) => {
    const part = char
      .normalize("NFKD")
      .replace(/[\u0300-\u036f]/g, "")
      .toLowerCase()
    folded += part
    for (let i = 0; i < part.length; i++) owner.push(index)
  })
  const lit = chars.map(() => false)
  for (const token of tokens) {
    const found: number[] = []
    for (let at = folded.indexOf(token); at !== -1; at = folded.indexOf(token, at + 1)) {
      found.push(at)
    }
    const starts = found.filter((at) => at === 0 || !/[\p{L}\p{N}]/u.test(folded[at - 1]))
    for (const at of starts.length ? starts : found.slice(0, 1)) {
      for (let i = at; i < at + token.length; i++) lit[owner[i]] = true
    }
  }
  const runs: { text: string; hit: boolean }[] = []
  chars.forEach((char, i) => {
    const last = runs.at(-1)
    if (last && last.hit === lit[i]) last.text += char
    else runs.push({ text: char, hit: lit[i] })
  })
  return runs
}

export type RecentDestination = { href: string; title: string; detail?: string }

function destinationKey(href: string) {
  const url = new URL(href, "http://dashboard.invalid")
  const selection = new URLSearchParams()
  for (const key of ["site", "repo", "unit", "app", "package"]) {
    const value = url.searchParams.get(key)
    if (value) selection.set(key, value)
  }
  return `${url.pathname}?${selection}`
}

export function rememberDestination(history: RecentDestination[], destination: RecentDestination) {
  if (history[0]?.href === destination.href) return history
  const key = destinationKey(destination.href)
  if (history[0] && destinationKey(history[0].href) === key) {
    // Filters change the question at this place, not which place was visited last.
    return [
      { ...destination, title: history[0].title, detail: history[0].detail },
      ...history.slice(1),
    ]
  }
  return [destination, ...history.filter((entry) => destinationKey(entry.href) !== key)].slice(
    0,
    12,
  )
}

export function recentDestinations(history: RecentDestination[], currentHref: string) {
  return history.filter((entry) => destinationKey(entry.href) !== destinationKey(currentHref))
}

export function localDestination(href: string): boolean {
  return href.startsWith("/") && !href.startsWith("//") && !/[\\\r\n]/.test(href)
}

/** A URL handed to search is identity, never a place to index credentials or signed queries. */
export function addressIdentity(value: string | undefined): string {
  if (!value) return ""
  try {
    const url = new URL(value.includes("://") ? value : `https://${value}`)
    return `${url.host}${url.pathname === "/" ? "" : url.pathname}`
  } catch {
    return ""
  }
}
