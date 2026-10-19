import type { BoardResource } from "@/lib/boards"

/**
 * What a drawing may embed. Excalidraw's own list includes tweets, gists and
 * Reddit, which it draws with the provider's script inside the frame; this
 * page's `script-src` takes nonce-bearing scripts alone, so those frames were
 * always blank. What is left are the players whose frames `frame-src` in
 * `proxy.ts` names — keep the two lists in step.
 */
const EMBED_HOSTS = new Set([
  "youtube.com",
  "youtu.be",
  "vimeo.com",
  "player.vimeo.com",
  "figma.com",
])

/** The frame sources the hosts above are drawn from, for the page's CSP. */
export const EMBED_FRAME_SOURCES = [
  "https://www.youtube.com",
  "https://player.vimeo.com",
  "https://www.figma.com",
]

/**
 * The thing on this server a link names, when it names one: the dashboard
 * itself, a project or a database. A drawing embeds these as live cards
 * (`renderEmbeddable`), never as a frame — the dashboard refuses to be framed.
 */
export function dashboardResource(link: string | null | undefined): BoardResource | null {
  if (!link) return null
  if (link === "/") return { kind: "server" }
  const match = link.match(/^\/(deploy|databases)\/(\d+)$/)
  if (!match) return null
  return { kind: match[1] === "deploy" ? "project" : "database", id: Number(match[2]) }
}

/** Excalidraw's `validateEmbeddable`: whether a link may become an embed at all. */
export function embeddable(link: string): boolean {
  if (dashboardResource(link)) return true
  try {
    const url = new URL(link)
    return url.protocol === "https:" && EMBED_HOSTS.has(url.hostname.replace(/^www\./, ""))
  } catch {
    return false
  }
}
