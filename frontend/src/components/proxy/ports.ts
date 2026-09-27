import type { Listener } from "@/lib/types"

/**
 * Where a socket answers, as the words finish "answers on …": its one
 * address, every interface, or loopback.
 */
export function reachWhere(listener: Pick<Listener, "scope" | "address" | "exposed">): string {
  if (listener.scope === "interface") return listener.address
  return listener.exposed ? "every interface" : "loopback"
}

/**
 * The Exposed tile's hint. A socket on one address is exposed as surely as
 * one on 0.0.0.0, so the hint says which kind the count is made of rather
 * than calling all of it "every interface".
 */
export function exposedHint(listeners: Pick<Listener, "scope" | "exposed">[]): string {
  const every = listeners.filter((l) => l.exposed && l.scope !== "interface").length
  const one = listeners.filter((l) => l.exposed && l.scope === "interface").length
  if (every + one === 0) return "nothing off the machine"
  if (one === 0) return "bound to every interface"
  if (every === 0) return one === 1 ? "bound to one address" : "each bound to one address"
  return `${every} on every interface · ${one} on one address`
}
