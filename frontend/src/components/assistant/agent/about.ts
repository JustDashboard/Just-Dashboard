import type { AgentAbout } from "@/components/assistant/agent/model"

/**
 * Ask from any page: the address that begins a chat about the whole server told
 * which page the person was on (`/assistant?about=…&title=…`), and the reading of
 * it back on the assistant's page. Kept free of React so `bun test` reads it.
 */

/** How long a page's address and title may be, as the server bounds them. */
export const ABOUT_PATH = 512
export const ABOUT_TITLE = 120

/** The address of a new server chat about the page at `path` (its query string included). */
export function askAboutHref(path: string, title: string): string {
  const about = boundAbout({ path, title })
  if (!about) return "/assistant"
  const query = new URLSearchParams({ about: about.path, title: about.title })
  return `/assistant?${query}`
}

/**
 * A page as the server takes it, or nothing for an address that is not one of
 * the dashboard's own: it is drawn as a link back, so `//elsewhere.example` or a
 * `javascript:` address would be a link out of it. A query string that makes it
 * too long goes before the page does; a title too long is cut.
 */
export function boundAbout(about: { path?: string | null; title?: string | null }) {
  let path = (about.path ?? "").trim()
  if (!/^\/(?![/\\])/.test(path) || /[\s\\]/.test(path.split("?")[0])) return undefined
  if (path.length > ABOUT_PATH) path = path.split("?")[0]
  if (path.length > ABOUT_PATH) return undefined
  const words = (about.title ?? "").replace(/\s+/g, " ").trim()
  const points = [...words]
  const title =
    points.length > ABOUT_TITLE ? `${points.slice(0, ABOUT_TITLE - 1).join("")}…` : words
  return { path, title: title || path.split("?")[0] } satisfies AgentAbout
}

/** The page `?about=` names, read from the assistant's address. */
export function readAbout(params: { get: (name: string) => string | null }) {
  const path = params.get("about")
  if (!path) return undefined
  return boundAbout({ path, title: params.get("title") })
}

/** The assistant's own pages, where asking about the page is asking it already. */
export function isAssistantPage(pathname: string): boolean {
  return pathname === "/assistant" || /^\/(deploy|databases)\/[^/]+\/assistant\/?$/.test(pathname)
}
