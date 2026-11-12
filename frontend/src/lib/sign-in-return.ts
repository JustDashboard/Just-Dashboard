/**
 * Where a browser the shell sent to sign in comes back to afterwards: the page it asked for.
 *
 * Until 0.8.6 only one address was kept, the hub's Connect door, "so a `next=` someone else wrote
 * cannot send a fresh sign-in anywhere but Settings → Hub". That rule cost more than it bought.
 * Backups, host alerts, proxy alerts and deploy notifications all send links into the dashboard,
 * and a session that had expired overnight turned every one of them into the Overview and a
 * search for the page the message named; while anyone still signed in followed the very same
 * link straight to its page, so dropping it protected nobody.
 *
 * What the rule was right about is kept. A `next=` is somebody else's text, so nothing in it is
 * used as it came. The address is rebuilt from parts that each passed a check:
 *
 *  - the path is this origin's, plain (no scheme, no `//`, no backslash, no dot segment, nothing
 *    encoded that would be a separator once decoded), and its first segment is one of the pages
 *    behind the sign-in, so it can never be `/api/…`, a download, `/login` itself or another host;
 *  - of the query, only the keys in `KEPT_QUERY` survive, each only on the pages where it chooses
 *    what is shown and nothing else: a tab anywhere, a log source on Logs, a system on the server
 *    map. Every key that fills a field in, opens a dialog or seeds a command (`sql`, `url`,
 *    `create`, `path`, and `source` on Backups, where it opens a job form on a path) is dropped,
 *    so a link cannot put its own text in front of someone who has just typed a password;
 *  - the hub's Connect door keeps `connect`, as before, where it is still only a suggestion
 *    (`connectTarget` in `lib/hub-link.ts`, protocol §8.1);
 *  - the fragment is dropped.
 *
 * An address that fails a check is not repaired: the sign-in goes to the Overview, as it always
 * did.
 */

const SAME_ORIGIN = "http://dashboard.invalid"

/**
 * The first path segment of every page behind the sign-in: the folders of `app/(dashboard)`.
 * `sign-in-return.test.js` reads that folder and fails when one is added without being listed
 * here, so a new section's links survive a sign-in from its first release.
 */
export const SIGNED_IN_SECTIONS: readonly string[] = [
  "account",
  "assistant",
  "audit",
  "backups",
  "dashboard",
  "databases",
  "deploy",
  "docker",
  "excalidraw",
  "files",
  "git",
  "logs",
  "map",
  "metrics",
  "network",
  "packages",
  "processes",
  "proxy",
  "remote-desktop",
  "security",
  "system-users",
  "terminal",
]

/**
 * The query keys carried through a sign-in, and where. Each selects a view of the page it is on
 * and is never written into a field. The same key can mean something else on another page
 * (`source` opens a prefilled job form on Backups), so a key is kept by page, not by name alone.
 */
export const KEPT_QUERY: Record<string, (pathname: string, section: string) => boolean> = {
  tab: () => true,
  source: (_, section) => section === "logs",
  system: (_, section) => section === "map",
  connect: (pathname) => pathname === "/dashboard/hub",
}

const MAX_PATH = 512
const MAX_VALUE = 300

/** One path segment as a person would read it, or nothing when it is not a plain name. */
function plainSegment(segment: string): string | undefined {
  let text: string
  try {
    text = decodeURIComponent(segment)
  } catch {
    return undefined
  }
  if (!text || text === "." || text === "..") return undefined
  // A separator or a control character has no place inside one segment, however it was spelled.
  if (/[/\\\u0000-\u001f\u007f]/.test(text)) return undefined
  return text
}

export function returnAfterSignIn(path: string | null | undefined): string | undefined {
  if (!path?.startsWith("/") || path.length > MAX_PATH * 2) return undefined
  // The URL parser reads a backslash as a slash, so `/\host` is another origin; say no before it
  // gets the chance to be clever about any spelling of one.
  if (path.includes("\\")) return undefined
  let url: URL
  try {
    url = new URL(path, SAME_ORIGIN)
  } catch {
    return undefined
  }
  if (url.origin !== SAME_ORIGIN || url.username || url.password) return undefined

  const pathname = url.pathname.replace(/\/+$/, "")
  if (!pathname || pathname.length > MAX_PATH) return undefined
  const segments = pathname.split("/").slice(1)
  if (!SIGNED_IN_SECTIONS.includes(segments[0])) return undefined
  if (!segments.every((segment) => plainSegment(segment) !== undefined)) return undefined

  const kept: string[] = []
  for (const [key, here] of Object.entries(KEPT_QUERY)) {
    if (!here(pathname, segments[0])) continue
    const value = url.searchParams.get(key)?.trim()
    // Spelled as `encodeURIComponent` spells it, so a value reads the same in the address bar
    // after the round trip as before it.
    if (value && value.length <= MAX_VALUE) kept.push(`${key}=${encodeURIComponent(value)}`)
  }
  return kept.length > 0 ? `${pathname}?${kept.join("&")}` : pathname
}

/**
 * The sign-in page for a signed-out browser at `path`, carrying `returnAfterSignIn`'s address when
 * there is one.
 *
 * A session that ends under the reader is not sent through here: it goes to `/login?reason=session`
 * and then the Overview, as before. Whoever signs in after an expiry may be a different account,
 * and the page the last one was on is theirs to have been on.
 */
export function signInAddress(path: string): string {
  const back = returnAfterSignIn(path)
  return back ? `/login?next=${encodeURIComponent(back)}` : "/login"
}
