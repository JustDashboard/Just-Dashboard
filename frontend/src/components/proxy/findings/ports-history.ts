import type { SeenListener } from "@/lib/types"
import { type ProxyFinding } from "@/components/proxy/findings/shared"
import { dangerousPorts } from "@/components/proxy/ports"
import { portsHref } from "@/components/proxy/ports-list"
import { firstSeen, isNew, seenWords } from "@/components/proxy/ports-history"

/**
 * A database or control port that began answering off this machine in the
 * last day. The standing finding says such a port is exposed; this one says
 * it was not yesterday, which is what separates a long-accepted risk from a
 * change somebody may not have meant — or not have made. Levelled by the
 * posture's grade of the new sockets, as the standing finding is.
 */
export function newExposureFindings(ports: SeenListener[], now = Date.now()): ProxyFinding[] {
  const fresh = dangerousPorts(ports)
    .map((port) => ({
      ...port,
      sockets: (port.sockets as SeenListener[]).filter((socket) => isNew(socket, now)),
    }))
    .filter((port) => port.sockets.length > 0)
  if (fresh.length === 0) return []

  const seen = (sockets: SeenListener[]) =>
    sockets
      .map((socket) => firstSeen(socket) ?? "")
      .sort()
      .at(-1) ?? ""
  const where = (sockets: SeenListener[]) =>
    sockets.some((socket) => socket.scope !== "interface")
      ? "on every interface"
      : sockets.length === 1
        ? `on ${sockets[0].address}`
        : `on ${sockets.length} addresses`
  const first = fresh[0]
  return [
    {
      id: "ports.new-exposure",
      level: fresh.some((port) => port.sockets.some((socket) => socket.level === "critical"))
        ? "critical"
        : "warning",
      title:
        fresh.length === 1
          ? `${first.service} began answering ${where(first.sockets)} ${seenWords(seen(first.sockets), now)}`
          : `${fresh.length} database or control ports began answering off this machine in the last day`,
      detail: fresh
        .map(
          (port) =>
            `${port.port}/${port.protocol} ${port.sockets[0].process || "unknown"} ${where(port.sockets)}, first seen ${seenWords(seen(port.sockets), now)}`,
        )
        .join("; "),
      advice:
        "Nothing was listening there the sample before. If nobody meant to open it, stop the program or bind it to 127.0.0.1. The Changes list on the ports page shows when it opened and, with the search cleared, what else changed then.",
      meta: "ports",
      href: portsHref({ q: `port:${[...new Set(fresh.map((port) => port.port))].join(",")}` }),
    },
  ]
}
