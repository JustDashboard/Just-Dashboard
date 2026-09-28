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
  /** An allow list: these sources, and everyone else turned away. */
  allowFrom: string[]
  /**
   * The ordered access list, for what an allow list cannot say: the first
   * rule a client matches decides, and `defaultAllow` is everyone no rule
   * matches. The server folds rules that only allow, with everyone else
   * turned away, into `allowFrom`, so a stream reads back one way.
   */
  rules?: StreamRule[]
  defaultAllow?: boolean
  /** Connections (UDP sessions) open at once from one client address. Absent is no cap. */
  maxConnPerIp?: number
  /** Connections (UDP sessions) open at once in all. Absent is no cap. */
  maxConnTotal?: number
  /** Each connection's speed from the client, in KiB/s. Absent is no limit. */
  uploadRate?: number
  /** Each connection's speed to the client, in KiB/s. Absent is no limit. */
  downloadRate?: number
}

/** One line of a stream's ordered access list. */
export type StreamRule = { action: "allow" | "deny"; source: string }

/** The form's access list: every rule in order, then what happens to everyone else. */
export type StreamAccess = { rules: StreamRule[]; defaultAllow: boolean }

/**
 * What nginx does with a stream now: holds its port (`live`), reads it and
 * holds no socket for it (`not-listening`), gives its port to a stream read
 * first (`shadowed`), does not read it as a stream (`not-read`), could not
 * be asked (`unknown`), or it is kept out of nginx's include (`paused`).
 */
export type StreamState = "live" | "not-listening" | "shadowed" | "not-read" | "unknown" | "paused"

/** What holds a port a stream asks for. */
export type PortOwner = {
  port: number
  proto: "tcp" | "udp"
  kind: "stream" | "site" | "program"
  /** The stream's name, the site's server name, or the program's; absent when nothing names it. */
  name?: string
  /** The site's name on the Sites page, for a link. */
  site?: string
  /** The file of a stream or a site written into another file. */
  file?: string
  pid?: number
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
  state: StreamState
  /** Why, in a sentence, for every state but a plain live one. */
  stateReason?: string
  /** What has the stream's port: the stream read first, a site, or the program holding it. */
  blocker?: PortOwner
  /** The last bind() failure nginx logged for one of its sockets, with its time. */
  bindError?: string
  /** Kept in paused/, which nginx does not read; the form edits it once resumed. */
  paused?: boolean
}

/** A port a save would be refused for, as the preview reports it. */
export type PortConflict = PortOwner & {
  /** The next port up that nothing holds. */
  suggest?: number
  /** The refusal the save would give, as a sentence. */
  message: string
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

/**
 * How the dashboard connects the stream directory: a file of its own in a
 * directory nginx.conf includes at its top level, a block appended to
 * nginx.conf, or one line in the stream block already there.
 */
export type StreamIncludeMode = "dropin" | "nginx.conf" | "stream-block"

/** The include the dashboard added, which it can take out again. */
export type StreamConnection = { mode: StreamIncludeMode; path: string }

export type StreamStatus = {
  /** Whether a top-level stream block reads the directory. Without it the files are ignored. */
  included: boolean
  /** Where the directory is included instead, when that is not a stream block. */
  includedIn?: string
  /** Why nginx.conf could not be read to tell. */
  includeError?: string
  module: StreamModule
  /** The dashboard's own include, when it is what reads the directory. */
  connection?: StreamConnection
  /**
   * The file holding a top-level stream block that does not include the
   * directory; the snippet is then the include line that goes inside it.
   */
  streamBlock?: string
  snippet: string
  dir: string
  streams: StreamEntry[]
  /**
   * The streams kept in paused/, apart from `streams` so nothing counting what
   * nginx reads counts them. Absent from a server older than pausing.
   */
  paused?: StreamEntry[]
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
  /**
   * Why nginx did not reload after the file passed its test — the command
   * failing, or nginx refusing the reload for another stream's or a site's
   * port. The file stays.
   */
  reloadError?: string
  output?: string
  /**
   * Whether nginx held every socket the stream asks for once it took the
   * reload up, watched for three seconds. Absent when nothing was watched.
   */
  listening?: boolean
  /** Why `listening` is absent, or what nginx had not done when the wait ran out. */
  listenNote?: string
}

/** The preview of a stream: its nginx, its warnings, and a port a save would be refused for. */
export type StreamPreview = {
  content: string
  warnings: string[]
  conflict?: PortConflict
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

/** The change that connects the stream directory, shown before it is made. */
/** Why a connect edits nginx.conf rather than adding a drop-in. */
export type StreamAppendReason =
  "no-directory" | "load-module-after" | "directory-elsewhere" | "name-taken"

export type StreamIncludePlan = {
  mode: StreamIncludeMode
  /** The file the change is made in. */
  path: string
  /** False for a drop-in, a file the change creates. */
  exists: boolean
  /** Why the change is an edit to nginx.conf, for that mode. */
  reason?: StreamAppendReason
  /** Where the drop-in would have gone, for every reason but no-directory. */
  dropIn?: string
  /** A copy of the file as it was is kept beside it; not where nginx would read the copy. */
  keepsCopy: boolean
  before: string
  after: string
  /** What the change adds, as it reads in the file. */
  added: string
  /** Where the added text starts in `after`, from 1. */
  line: number
  /** The stream files nginx starts reading. */
  streams: string[]
  /** Their sockets something already holds; the connect is refused while there are any. */
  conflicts: string[]
  warnings: string[]
}

/** What connecting or disconnecting did. */
export type StreamIncludeResult = {
  mode: StreamIncludeMode
  path: string
  /** The copy kept of a file the change edited. */
  backup?: string
  validation: ProxyValidation
  /** Stream files nginx starts or stops reading. */
  streams: number
  reloaded: boolean
  /** Why nginx did not reload after the change passed its test; the change stays. */
  reloadError?: string
  output?: string
  warnings: string[]
}

/** One module this nginx was built with (`GET /proxy/modules`). */
export type NginxModule = {
  /** As configure names it: `http_v2_module`, `stream`. */
  name: string
  /** `unknown` when the configuration could not be read to tell loaded from not. */
  state: "static" | "loaded" | "not-loaded" | "not-installed" | "unknown"
  /** The shared object a dynamic module loads from. */
  path?: string
  /** What installs a module that is not installed, named only where the package manager has it. */
  package?: string
}

/** What this nginx was built with and what of it the configuration loads. 503 without nginx. */
export type NginxModules = {
  /** nginx's own "nginx/1.26.3 (Ubuntu)". */
  version: string
  openssl?: string
  modulesPath: string
  modules: NginxModule[]
  /** Why a dynamic module's state could not be read. */
  detail?: string
}

/** One `POST /proxy/streams/test`: dialled from the host, inside five seconds. */
export type StreamTestRequest = {
  target: string
  port: number
  protocol: "tcp" | "udp"
  /** The forward's target, or the stream's own port through nginx. */
  mode: "upstream" | "nginx"
  /** A real query to send; absent picks DNS for 53 and NTP for 123. */
  query?: "dns" | "ntp"
}

/**
 * What a test saw. `closed` is a connection accepted and dropped at once;
 * `silent` is a UDP port that is not DNS or NTP, which is not dialled.
 */
export type StreamTestOutcome =
  | "connected"
  | "answered"
  | "closed"
  | "silent"
  | "refused"
  | "timeout"
  | "dns"
  | "unreachable"
  | "error"

export type StreamTestResult = {
  mode: "upstream" | "nginx"
  protocol: "tcp" | "udp"
  /** What was dialled, a name resolved to its address. */
  address: string
  outcome: StreamTestOutcome
  ok: boolean
  /** The connect (TCP) or the reply (UDP), in milliseconds. */
  ms: number
  /** What the service sent first, unprompted. */
  banner?: string
  /** A DNS or NTP reply, summarised. */
  answer?: string
  detail: string
  warnings: string[]
  error?: string
}
