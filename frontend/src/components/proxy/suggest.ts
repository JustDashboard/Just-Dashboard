import type { Listener, StreamStatus, VHost } from "@/lib/types"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"

/** An app on loopback that nothing routes to yet, as the site form's upstream. */
export type ProxySuggestion = {
  port: number
  process: string
  upstream: string
}

/**
 * Programs that answer on loopback as part of running the machine, not as an
 * app somebody would put a domain in front of.
 */
const INFRASTRUCTURE = new Set([
  "systemd-resolve",
  "systemd-resolved",
  "sshd",
  "containerd",
  "dockerd",
  "cupsd",
  "master",
  "exim4",
  "chronyd",
  "tailscaled",
  "nginx",
  "caddy",
])

function isLoopbackHost(host: string) {
  const bare = host.replace(/^\[|\]$/g, "")
  return bare === "localhost" || bare === "::1" || bare.startsWith("127.")
}

/**
 * The loopback port an upstream reaches: "http://127.0.0.1:3000/",
 * "localhost:8080", "[::1]:9000". Anything else — a container name, a unix
 * socket, a remote host — is not one of this machine's loopback apps.
 */
export function loopbackPort(upstream: string): number | undefined {
  const match = /^(?:[a-z]+:\/\/)?(\[[^\]]+\]|[^/:\s]+):(\d+)/i.exec(upstream.trim())
  if (!match || !isLoopbackHost(match[1])) return undefined
  return Number(match[2])
}

/**
 * Apps listening only on loopback that no site or stream reaches yet. A
 * database or control port is left out on purpose: those are the ports the
 * findings warn about reaching from outside, and a domain in front of one is
 * that same mistake. Ephemeral ports are a program's own plumbing.
 */
export function unproxiedApps({
  ports,
  vhosts,
  streams,
}: {
  ports: Listener[]
  vhosts: VHost[]
  streams?: StreamStatus
}): ProxySuggestion[] {
  const routed = new Set(
    [...vhosts.flatMap((v) => v.upstreams), ...(streams?.streams ?? []).map((s) => s.upstream)].map(
      loopbackPort,
    ),
  )
  const byPort = new Map<number, Listener[]>()
  for (const l of ports) {
    if (l.protocol !== "tcp") continue
    byPort.set(l.port, [...(byPort.get(l.port) ?? []), l])
  }
  const out: ProxySuggestion[] = []
  for (const [port, listeners] of byPort) {
    if (routed.has(port) || DANGEROUS_PORTS[port] || port < 1024 || port >= 32768) continue
    if (!listeners.every((l) => isLoopbackHost(l.address))) continue
    const process = listeners[0].process
    if (INFRASTRUCTURE.has(process)) continue
    const v4 = listeners.find((l) => l.address.startsWith("127."))
    out.push({
      port,
      process,
      upstream: v4 ? `http://${v4.address}:${port}` : `http://[::1]:${port}`,
    })
  }
  return out.sort((a, b) => a.port - b.port)
}
