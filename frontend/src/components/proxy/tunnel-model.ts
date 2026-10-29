/*
 * The Cloudflare Tunnel page's vocabulary, kept free of React so `bun test`
 * reads it: what the connector's health is called, what a hostname's record
 * says, and what a pasted token is.
 */

import type { DotTone } from "@/components/status-dot"
import type { TunnelHostname, TunnelStatus } from "@/lib/proxy/types-tunnel"

/** The tunnel's health as one word and its tone, for the page and the overview. */
export function tunnelHealth(status: TunnelStatus | undefined): { tone: DotTone; label: string } {
  if (!status?.connected) return { tone: "unknown", label: "Not connected" }
  const state = status.connector.state
  if (state === "missing") return { tone: "danger", label: "Connector missing" }
  if (state === "restarting") return { tone: "danger", label: "Connector restarting" }
  if (state !== "running") return { tone: "danger", label: `Connector ${state}` }
  if (!status.checkedAt) return { tone: "notice", label: "Checking" }
  if (!status.ready) return { tone: "danger", label: "No edge connection" }
  return { tone: "running", label: "Connected" }
}

/** What a hostname's record at Cloudflare says. */
export function dnsWords(hostname: Pick<TunnelHostname, "dns">): { tone: DotTone; label: string } {
  switch (hostname.dns) {
    case "created":
      return { tone: "running", label: "Record created" }
    case "present":
      return { tone: "running", label: "Record in place" }
    case "updated":
      return { tone: "running", label: "Record moved to this tunnel" }
    case "manual":
      return { tone: "warning", label: "Record to add in Cloudflare" }
    case "error":
      return { tone: "danger", label: "Record refused" }
    default:
      return { tone: "unknown", label: "Not checked yet" }
  }
}

/** The name a tunnel's records point at. */
export function cnameTarget(tunnelId: string) {
  return `${tunnelId}.cfargotunnel.com`
}

/**
 * The token out of what was pasted. Cloudflare's dashboard shows it inside
 * a command — `cloudflared service install eyJ…`, `docker run … --token eyJ…`
 * — and pasting the whole command is the natural thing to do, so the token
 * is the last word of it that is one: base64 of a JSON object, which always
 * starts `eyJ`.
 */
export function tokenFromPaste(pasted: string): string {
  const words = pasted.trim().split(/\s+/).filter(Boolean)
  for (let index = words.length - 1; index >= 0; index--) {
    const word = words[index].replace(/^['"]|['"]$/g, "")
    if (/^eyJ[A-Za-z0-9+/_-]+=*$/.test(word)) return word
  }
  return pasted.trim()
}

/** What is wrong with a pasted token before it is sent, if anything visibly is. */
export function tokenProblem(pasted: string): string | undefined {
  const token = tokenFromPaste(pasted)
  if (!token) return "Paste the token Cloudflare shows for the tunnel."
  if (!token.startsWith("eyJ") || /\s/.test(token))
    return "A tunnel token is one long word starting eyJ, as Cloudflare shows it under the tunnel's install command."
  if (token.length > 4096) return "That is longer than any tunnel token."
  return undefined
}

/** What is wrong with a name for a tunnel to create, if anything. */
export function tunnelNameProblem(name: string): string | undefined {
  const trimmed = name.trim()
  if (!trimmed) return "Name the tunnel."
  if (trimmed.length > 64 || /[\r\n/\0]/.test(trimmed))
    return "A tunnel is named in up to 64 characters, with no slash."
  return undefined
}

/** The hostnames still waiting on something, for the page's headline. */
export function tunnelCounts(status: TunnelStatus | undefined) {
  const hostnames = status?.hostnames ?? []
  return {
    hostnames: hostnames.length,
    routed: hostnames.filter((hostname) => hostname.routed).length,
    records: hostnames.filter((hostname) =>
      ["created", "present", "updated"].includes(hostname.dns),
    ).length,
    problems: hostnames.filter((hostname) => hostname.dns === "error").length,
  }
}

/** The server's clause as a sentence: its first letter raised and a full stop at its end. */
export function sentence(text: string) {
  const trimmed = text.trim()
  if (!trimmed) return trimmed
  const raised = trimmed[0].toUpperCase() + trimmed.slice(1)
  return /[.!?]$/.test(raised) ? raised : `${raised}.`
}
