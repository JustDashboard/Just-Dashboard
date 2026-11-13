import type { Listener } from "@/lib/types"

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

/**
 * ":80 open · :443 docker-proxy", with a closed port said as such. A port the
 * engine itself holds — `self`, the process the line hangs under — is "open":
 * ":80 nginx" under the nginx tile named it a third time.
 */
export function frontDoorLine(doors: FrontDoor[], self?: string): string {
  return doors
    .map((door) => {
      const names = door.holders.map((h) => h.process || "unknown")
      const state =
        names.length === 0
          ? "closed"
          : self && names.every((name) => name === self)
            ? "open"
            : names.join(" + ")
      return `:${door.port} ${state}`
    })
    .join(" · ")
}
