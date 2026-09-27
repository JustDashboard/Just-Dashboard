import type { ProxyValidation } from "./types-engine"

/** One forwarded port for something that does not speak HTTP. */
export type StreamSpec = {
  name: string
  listen: number
  /** The one address to listen on. Absent is every address of both families. */
  address?: string
  /** One port taking TCP, UDP, or both to the same upstream, as DNS does. */
  protocol: "tcp" | "udp" | "both"
  /**
   * How nginx ends a UDP session: kept per client until it goes quiet, or
   * ended at the first reply. It leaves TCP alone, and is absent for TCP only.
   */
  udpMode?: "session" | "request"
  /** host:port, or unix:/path for a local socket. */
  upstream: string
  proxyProtocol: boolean
  /** The idle timeout: seconds a connection may sit silent. Absent is nginx's ten minutes. */
  timeout?: number
  /** Seconds to wait for the upstream to accept. Absent is nginx's minute. */
  connectTimeout?: number
  allowFrom: string[]
}

/** A file in the stream directory, as the page lists it. */
export type StreamEntry = StreamSpec & {
  path: string
  /** The dashboard wrote it. */
  managed: boolean
  /** Anyone may connect: no access rules, or rules that admit everyone. */
  open: boolean
  /** What the file does that the form cannot express; the form will not save over it. */
  unsupported: string[]
  /** Why the file could not be read. */
  error?: string
  /** Where the file points when it is a symbolic link; a delete removes the link, not that. */
  link?: string
}

/**
 * Whether nginx can read a stream block at all. `static` and `loaded` can;
 * `unknown` means nginx could not be asked, never that the module is missing.
 */
export type StreamModule = {
  state: "static" | "loaded" | "not-loaded" | "not-installed" | "absent" | "unknown"
  usable: boolean
  /** The shared object a dynamic build loads. */
  path?: string
  /** What provides the module on this host's package manager. */
  package?: string
  /** nginx's own words when it could not be asked. */
  detail?: string
}

export type StreamStatus = {
  /** Whether a top-level stream block reads the directory. Without it the files are ignored. */
  included: boolean
  /** Where the directory is included instead, when that is not a stream block. */
  includedIn?: string
  /** Why nginx.conf could not be read to tell. */
  includeError?: string
  module: StreamModule
  snippet: string
  dir: string
  streams: StreamEntry[]
}

/** What a save did. */
export type StreamResult = {
  name: string
  path: string
  content: string
  warnings: string[]
  validation?: ProxyValidation
  /** The name the stream had before this save renamed it; its file is kept as <old>.conf.bak. */
  renamed?: string
  reloaded: boolean
  /** Why nginx did not reload after the file passed its test. The file stays. */
  reloadError?: string
  output?: string
}

/** What a delete did: nginx reloads only for a stream it was reading. */
export type StreamDeleteResult = {
  name: string
  reloaded: boolean
  reloadError?: string
  /** The .bak the content was kept in. */
  backup?: string
  /** Where a removed symbolic link pointed; that file is untouched. */
  link?: string
  /** Why a file's content could not be kept; the file is removed all the same. */
  unread?: string
}
