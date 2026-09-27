import type { Listener } from "@/lib/types"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"

/**
 * The Exposed tile's hint. A socket on one address is exposed as surely as
 * one on 0.0.0.0, so the hint says which kind the count is made of rather
 * than calling all of it "every interface". The mixed form has to fit half a
 * phone's width, where the tile truncates it.
 */
export function exposedHint(listeners: Pick<Listener, "scope" | "exposed">[]): string {
  const every = listeners.filter((l) => l.exposed && l.scope !== "interface").length
  const one = listeners.filter((l) => l.exposed && l.scope === "interface").length
  if (every + one === 0) return "nothing off the machine"
  if (one === 0) return "bound to every interface"
  if (every === 0) return one === 1 ? "bound to one address" : "each bound to one address"
  return `${every} on all · ${one} on one IP each`
}

/** Every interface or a public address: a reach the internet shares. */
export function internetFacing(listener: Pick<Listener, "reach">): boolean {
  return listener.reach === "all" || listener.reach === "public"
}

/**
 * A database or control port answering off the machine, once per protocol
 * and port however many addresses it is bound to — Postgres on 0.0.0.0 and
 * on :: is one database, as the posture's finding for it is one finding.
 */
export type DangerousPort = {
  protocol: string
  port: number
  service: string
  /** Every exposed socket on the port, in listing order. */
  sockets: Listener[]
  /** Any of them faces the internet, which the posture calls critical. */
  internet: boolean
}

export function dangerousPorts(listeners: Listener[]): DangerousPort[] {
  const byPort = new Map<string, DangerousPort>()
  for (const l of listeners) {
    const service = DANGEROUS_PORTS[l.port]
    if (!l.exposed || !service) continue
    const key = `${l.protocol}/${l.port}`
    const entry = byPort.get(key) ?? {
      protocol: l.protocol,
      port: l.port,
      service,
      sockets: [],
      internet: false,
    }
    entry.sockets.push(l)
    entry.internet ||= internetFacing(l)
    byPort.set(key, entry)
  }
  return [...byPort.values()]
}

/**
 * The verdict a listening socket is drawn with, graded by who can connect as
 * the posture grades it: a database or control port the internet can reach
 * is critical, the same port on a bridge, tailnet or private address a
 * warning, and any other socket off the machine a warning. Loopback has none.
 */
export function reachVerdict(
  listener: Pick<Listener, "port" | "exposed" | "reach">,
): "critical" | "warning" | undefined {
  if (!listener.exposed) return undefined
  return DANGEROUS_PORTS[listener.port] && internetFacing(listener) ? "critical" : "warning"
}
