import type { Container, Listener } from "@/lib/types"

/**
 * What a site's upstream can be pointed at on this machine: the sockets
 * listening on loopback or every interface, from /ports, and the running
 * containers' published ports, from Docker. nginx runs on the host, so a
 * container is reached through the port it publishes; one it does not
 * publish is listed as something to fix rather than offered by its bridge
 * address, which changes whenever the container is recreated.
 */
export type UpstreamOption = {
  /** Unique within the list. */
  key: string
  /** What picking it puts in the field; empty when it cannot be picked. */
  url: string
  /** Where nginx connects, as the field would say it: 127.0.0.1:3000. */
  address: string
  /** The process or container behind it; empty when the host did not say. */
  name: string
  source: "listener" | "container"
  /** The listening process, for its logo. */
  process?: string
  /** The container's image, for its logo. */
  image?: string
  /** The container's own port, which the published one forwards to. */
  containerPort?: number
  /** Why nginx cannot reach it. */
  unreachable?: string
}

/**
 * Ports that answer something other than HTTP. A domain in front of
 * Postgres, SSH or Docker's API is a mistake, and in the last case a
 * dangerous one, so none of them is offered.
 */
const NOT_HTTP = new Set([
  22, 25, 53, 110, 111, 143, 465, 587, 993, 995, 2375, 2376, 3306, 5432, 5672, 6379, 11211, 27017,
])

/**
 * The host nginx dials for a socket bound to address: loopback for a
 * wildcard, the address itself on loopback, nothing for a socket bound to one
 * particular interface. `::` is dialled as [::1], since whether it also takes
 * IPv4 is a socket option /ports cannot see.
 */
function loopbackHost(address: string): string | undefined {
  if (address === "" || address === "*" || address === "0.0.0.0") return "127.0.0.1"
  if (address === "::" || address === "::1") return "[::1]"
  if (/^127\.\d+\.\d+\.\d+$/.test(address)) return address
  return undefined
}

/**
 * The URL nginx forwards to: https for the ports TLS is served on, where
 * plain HTTP would be answered with a TLS alert or a 400, http otherwise.
 */
function upstreamUrl(address: string, port: number) {
  return `${port === 443 || port === 8443 ? "https" : "http"}://${address}`
}

/** A published port's address: loopback for a wildcard, the address it is bound to otherwise. */
function publishedHost(ip: string | undefined): string {
  if (!ip || ip === "0.0.0.0" || ip === "::") return "127.0.0.1"
  return ip.includes(":") ? `[${ip}]` : ip
}

function containerOptions(containers: Container[]): UpstreamOption[] {
  const out: UpstreamOption[] = []
  for (const container of containers) {
    if (container.state !== "running") continue
    const tcp = container.ports.filter((p) => p.type === "tcp")
    const published = new Map<number, UpstreamOption>()
    for (const port of tcp) {
      if (!port.publicPort || published.has(port.publicPort)) continue
      const address = `${publishedHost(port.ip)}:${port.publicPort}`
      published.set(port.publicPort, {
        key: `container:${container.id}:${port.publicPort}`,
        url: upstreamUrl(address, port.publicPort),
        address,
        name: container.name,
        source: "container",
        image: container.image,
        containerPort: port.privatePort,
      })
    }
    const reached = new Set([...published.values()].map((o) => o.containerPort))
    const unpublished = [...new Set(tcp.map((p) => p.privatePort))]
      .filter((port) => !reached.has(port))
      .map<UpstreamOption>((port) => ({
        key: `container:${container.id}:private:${port}`,
        url: "",
        address: String(port),
        name: container.name,
        source: "container",
        image: container.image,
        containerPort: port,
        unreachable: "Not published. Publish it on 127.0.0.1 to reach it from nginx.",
      }))
    out.push(...published.values(), ...unpublished)
  }
  return out
}

/**
 * Everything the upstream can be pointed at, containers first and each list
 * by port. A listener is left out when it is nginx itself (a site pointed at
 * nginx is a loop), when a container option already covers its port (the
 * container says more than docker-proxy does), and when its port answers
 * something other than HTTP. A port listening on both IPv4 and IPv6 is one
 * option, dialled over IPv4.
 */
export function upstreamOptions(
  listeners: Listener[] | undefined,
  containers: Container[] | undefined,
): UpstreamOption[] {
  const fromContainers = containerOptions(containers ?? [])
  const covered = new Set(
    fromContainers.filter((o) => o.url).map((o) => Number(o.address.split(":").pop())),
  )
  const byPort = new Map<string, UpstreamOption>()
  for (const listener of listeners ?? []) {
    if (listener.protocol !== "tcp" || listener.process === "nginx") continue
    if (NOT_HTTP.has(listener.port) || covered.has(listener.port)) continue
    const host = loopbackHost(listener.address)
    if (!host) continue
    // One option per port and host: a socket on 0.0.0.0 and another on ::
    // for the same port are one server, and IPv4 is the address to dial.
    const slot = host === "[::1]" ? "v6" : host
    const key = `listener:${slot}:${listener.port}`
    const v4 = byPort.get(`listener:127.0.0.1:${listener.port}`)
    if (slot === "v6" && v4) continue
    if (slot === "127.0.0.1") byPort.delete(`listener:v6:${listener.port}`)
    if (byPort.has(key)) continue
    const address = `${host}:${listener.port}`
    byPort.set(key, {
      key,
      url: upstreamUrl(address, listener.port),
      address,
      name: listener.process,
      source: "listener",
      process: listener.process || undefined,
    })
  }
  const port = (o: UpstreamOption) => Number(o.address.split(":").pop())
  const sorted = (list: UpstreamOption[]) =>
    [...list].sort(
      (a, b) =>
        Number(Boolean(a.unreachable)) - Number(Boolean(b.unreachable)) || port(a) - port(b),
    )
  return [...sorted(fromContainers), ...sorted([...byPort.values()])]
}

/** The loopback host and port an upstream names, or nothing for anything else. */
export function loopbackTarget(upstream: string): { host: string; port: number } | undefined {
  const match = /^(https?):\/\/(\[[^\]]*\]|[^/:?#]+)(?::(\d+))?(?:[/?#]|$)/i.exec(upstream.trim())
  if (!match) return undefined
  const host = match[2].toLowerCase().replace(/^\[|\]$/g, "")
  if (host !== "localhost" && host !== "::1" && !/^127\.\d+\.\d+\.\d+$/.test(host)) {
    return undefined
  }
  const port = match[3] ? Number(match[3]) : match[1].toLowerCase() === "https" ? 443 : 80
  return port >= 1 && port <= 65535 ? { host, port } : undefined
}

/** Whether a socket bound to address takes a connection to host. */
function accepts(address: string, host: string): boolean {
  if (host === "localhost") return address === "" || address === "*" || !!loopbackHost(address)
  const v6 = host === "::1"
  // `::` takes IPv4 too unless the socket said otherwise, which is the
  // default on Linux and the way every runtime binds it.
  if (address === "" || address === "*" || address === "::") return true
  if (address === "0.0.0.0") return !v6
  return address === host
}

/**
 * Said when an upstream on loopback has nothing listening behind it: nginx
 * then answers every request with 502. Nothing when the upstream is not on
 * loopback, or when the listeners have not been read yet.
 */
export function nothingListening(
  upstream: string,
  listeners: Listener[] | undefined,
  containers: Container[] | undefined,
): string | undefined {
  const target = loopbackTarget(upstream)
  if (!target || !listeners) return undefined
  const { host, port } = target
  const listening =
    listeners.some((l) => l.protocol === "tcp" && l.port === port && accepts(l.address, host)) ||
    (containers ?? []).some(
      (c) =>
        c.state === "running" &&
        c.ports.some(
          (p) =>
            p.type === "tcp" &&
            p.publicPort === port &&
            (host === "localhost" || accepts(p.ip ?? "", host) || p.ip === host),
        ),
    )
  if (listening) return undefined
  const shown = host.includes(":") ? `[${host}]` : host
  return `Nothing is listening on ${shown}:${port} right now, so nginx answers 502 until something does.`
}
