import type { StreamEntry, StreamModule, StreamSpec, StreamStatus } from "@/lib/types"
import { DANGEROUS_PORTS } from "@/components/proxy/findings/shared"

/**
 * The spec fields of a listed stream: what the form edits and the API takes
 * back. The listing's own fields — path, managed, open — would be refused as
 * unknown by the save.
 */
export function streamSpecOf(stream: StreamSpec): StreamSpec {
  const {
    name,
    listen,
    address,
    protocol,
    udpMode,
    upstream,
    proxyProtocol,
    timeout,
    connectTimeout,
    allowFrom,
  } = stream
  return {
    name,
    listen,
    address,
    protocol,
    udpMode,
    upstream,
    proxyProtocol,
    timeout,
    connectTimeout,
    allowFrom,
  }
}

/** What a preview or a save posts: the allow list as typed, split, and a UDP mode only for UDP. */
export function streamBody(spec: StreamSpec, allow: string): StreamSpec {
  return {
    ...streamSpecOf(spec),
    udpMode: spec.protocol === "udp" ? (spec.udpMode ?? "session") : undefined,
    allowFrom: allow.split(/[\s,]+/).filter(Boolean),
  }
}

/** The module is known to be missing — not merely unknown because nginx could not be asked. */
export function moduleMissing(module: StreamModule): boolean {
  return !module.usable && module.state !== "unknown"
}

/**
 * nginx reads the stream directory and can: what is saved there forwards at
 * the next reload. A module nginx could not be asked about is not held
 * against it.
 */
export function streamsLive(status: StreamStatus): boolean {
  return status.included && !moduleMissing(status.module)
}

/** What gets the module in, in one sentence, for the state that lacks it. */
export function moduleRemedy(module: StreamModule): string {
  switch (module.state) {
    case "not-installed":
      return module.package
        ? `Install ${module.package}, the package with nginx's stream module.`
        : "Install your distribution's package for nginx's stream module."
    case "not-loaded":
      return `Load it: add load_module ${module.path ?? "ngx_stream_module.so"}; at the top of nginx.conf.`
    case "absent":
      return "This nginx was built without it, so streams need an nginx build that has it, such as nginx.org's own packages."
  }
  return ""
}

/** Where a stream listens, the way it would be dialled. */
export function listenLabel(stream: Pick<StreamSpec, "address" | "listen">): string {
  if (!stream.address) return String(stream.listen)
  return stream.address.includes(":")
    ? `[${stream.address}]:${stream.listen}`
    : `${stream.address}:${stream.listen}`
}

/** A file that cannot be read first, then streams open to anyone — a database port first among those. */
function rank(stream: StreamEntry): number {
  if (stream.error) return 0
  if (!stream.open) return 3
  return DANGEROUS_PORTS[stream.listen] ? 1 : 2
}

/** Worst first, then by port. The order is the page's answer to "which of these needs me". */
export function byUrgency(a: StreamEntry, b: StreamEntry): number {
  return rank(a) - rank(b) || a.listen - b.listen || a.name.localeCompare(b.name)
}
