/**
 * An account's own page, as an address: `/databases/<id>/access/<account>`.
 *
 * The segment is the account's name and nothing else. What tells two accounts
 * of one name apart — a MySQL host, the database a document user is kept in —
 * is `?host=`, the key the account routes of the API take it by, and the view
 * on screen is `?tab=`. A name is whatever the server allows: `ops@night`,
 * `50%`, an e-mail address. It is written into the path once
 * (`encodeURIComponent`) and read back once from the path as the address bar
 * spells it, so a name that itself holds `%40` comes back as it was.
 */

/** The views an account's page can have; each engine has the ones it supports. */
export const ACCOUNT_TABS = ["grants", "sessions", "settings"] as const
export type AccountTab = (typeof ACCOUNT_TABS)[number]

export function isAccountTab(value: string | null | undefined): value is AccountTab {
  return (ACCOUNT_TABS as readonly string[]).includes(value ?? "")
}

/** The list the account pages stand under. */
export function accessHref(id: number | string, show?: string): string {
  return show
    ? `/databases/${id}/access?show=${encodeURIComponent(show)}`
    : `/databases/${id}/access`
}

/**
 * The address of one account's page. `host` is left out when the engine's
 * accounts have none (it is `undefined`), and kept when it is the empty
 * string: MySQL has accounts whose host is empty. The first view is the page's
 * bare address, so `tab` is written only for the others.
 */
export function accountHref(
  id: number | string,
  account: { name: string; host?: string },
  tab?: AccountTab | "",
): string {
  const query = new URLSearchParams()
  if (account.host !== undefined) query.set("host", account.host)
  if (tab && tab !== ACCOUNT_TABS[0]) query.set("tab", tab)
  const text = query.toString()
  return `/databases/${id}/access/${encodeURIComponent(account.name)}${text ? `?${text}` : ""}`
}

/**
 * The account a path names, or `null` when the path is not an account's page.
 * Read from the path itself rather than from the router's params: those have
 * been handed out both encoded and decoded, and decoding a second time turns
 * `cron%40night` into another account.
 */
export function accountFromPath(pathname: string): string | null {
  const match = /^\/databases\/[^/]+\/access\/([^/]+)\/?$/.exec(pathname)
  if (!match) return null
  try {
    return decodeURIComponent(match[1])
  } catch {
    // A stray `%` typed by hand: the name is what was typed.
    return match[1]
  }
}

/**
 * Where an address that opened an account's sheet goes now:
 * `/access?account=reporting&host=%25` is `/access/reporting?host=%25`.
 * `null` when the address names no account.
 */
export function formerAccountHref(
  id: number | string,
  params: { get: (name: string) => string | null },
): string | null {
  const name = params.get("account")
  if (!name) return null
  const host = params.get("host")
  return accountHref(id, { name, host: host ?? undefined })
}
