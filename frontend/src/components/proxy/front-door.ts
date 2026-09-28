import type { Listener, VHost } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"

/** The two ports a visitor's browser knocks on. */
const FRONT_PORTS = [80, 443] as const

export type FrontDoor = {
  port: number
  /** Every program answering on the port off loopback, by name; "" is one `ss` could not name. */
  holders: { process: string; addresses: string[] }[]
}

function isLoopback(address: string) {
  return address.startsWith("127.") || address === "::1" || address === "[::1]"
}

/**
 * Who answers on :80 and :443 from outside this machine. A loopback bind is
 * not a front door — a visitor never reaches it — so it is left out.
 */
export function frontDoors(ports: Listener[]): FrontDoor[] {
  return FRONT_PORTS.map((port) => {
    // nginx's workers each show the master's socket, so one address can
    // appear several times under one name.
    const byProcess = new Map<string, Set<string>>()
    for (const l of ports) {
      if (l.port !== port || !l.protocol.startsWith("tcp") || isLoopback(l.address)) continue
      byProcess.set(l.process, (byProcess.get(l.process) ?? new Set()).add(l.address))
    }
    return {
      port,
      holders: [...byProcess].map(([process, addresses]) => ({
        process,
        addresses: [...addresses],
      })),
    }
  })
}

/** ":80 nginx · :443 docker-proxy", with a closed port said as such. */
export function frontDoorLine(doors: FrontDoor[]): string {
  return doors
    .map(
      (door) =>
        `:${door.port} ${
          door.holders.length === 0
            ? "closed"
            : door.holders.map((h) => h.process || "unknown").join(" + ")
        }`,
    )
    .join(" · ")
}

/**
 * The port a `listen` value binds: "443 ssl", "[::]:443 ssl http2",
 * "127.0.0.1:8080". A unix socket binds none.
 */
export function listenPort(listen: string): number | undefined {
  const address = listen.trim().split(/\s+/)[0] ?? ""
  if (address.startsWith("unix:")) return undefined
  const match = /(?:^|:)(\d+)$/.exec(address)
  return match ? Number(match[1]) : undefined
}

export type FrontDoorFindingInput = { ports?: Listener[]; vhosts?: VHost[] }

/**
 * The two ways the front door goes wrong without nginx -t noticing: two
 * programs answering on one of the ports, so IPv4 and IPv6 visitors can land
 * on different ones, and a port nginx's enabled sites listen on held by
 * something else, so those sites never get a request. A holder `ss` could
 * not name is no evidence either way and judges nothing.
 */
export function frontDoorFindings({ ports, vhosts }: FrontDoorFindingInput): ProxyFinding[] {
  if (!ports) return []
  const out: ProxyFinding[] = []
  const wanted = new Set(
    (vhosts ?? [])
      .filter((v) => v.kind === "nginx" && v.enabled)
      .flatMap((v) => v.listen.map(listenPort)),
  )
  for (const door of frontDoors(ports)) {
    if (door.holders.some((h) => !h.process)) continue
    const names = door.holders.map((h) => h.process)
    if (door.holders.length > 1) {
      out.push({
        id: `frontdoor.shared.${door.port}`,
        level: "warning",
        title: `Port ${door.port} answers from ${names.length} programs: ${names.join(", ")}`,
        detail: door.holders.map((h) => `${h.process} on ${h.addresses.join(", ")}`).join("; "),
        advice:
          "A visitor gets whichever holds the address they arrive on, so IPv4 and IPv6 can reach different programs. Stop one, or move it to another port.",
        fingerprint: `${door.port} ${[...names].sort().join(" ")}`,
        meta: "ports",
        href: "/proxy/ports",
      })
      continue
    }
    const holder = names[0]
    if (holder && holder !== "nginx" && wanted.has(door.port)) {
      out.push({
        id: `frontdoor.taken.${door.port}`,
        level: "critical",
        title: `nginx sites listen on ${door.port}, but ${holder} holds it`,
        detail: `${holder} answers on ${door.holders[0].addresses.join(", ")}:${door.port}, so no request reaches nginx's sites there.`,
        advice: `Stop ${holder} or move it off ${door.port}, then restart nginx so it can bind the port.`,
        fingerprint: `${door.port} ${holder}`,
        meta: "ports",
        href: "/proxy/ports",
      })
    }
  }
  return out
}
