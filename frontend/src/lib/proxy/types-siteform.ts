import type { ProxyDiagnostic, ProxyValidation } from "./types-engine"

/** A site as the dashboard describes it, not as nginx does. */
export type SiteLocation = {
  path: string
  upstream?: string
  /** A folder served at the path: /assets/app.css is <root>/app.css. */
  root?: string
  /**
   * "root" keeps nginx's own reading, read back from a file written with
   * `root`: the path is appended, so /assets/app.css is <root>/assets/app.css.
   */
  rootMode?: "root"
  webSockets: boolean
}

export type SiteSpec = {
  managedAcme?: boolean
  name: string
  domains: string[]
  kind: "proxy" | "static" | "redirect"
  upstream?: string
  root?: string
  /** A static site whose paths with no file of their own get index.html. */
  spa?: boolean
  redirectTo?: string
  permanent?: boolean
  tls: boolean
  certPath?: string
  keyPath?: string
  forceHttps: boolean
  hsts: boolean
  http2: boolean
  webSockets: boolean
  gzip: boolean
  blockExploits: boolean
  securityHeaders: boolean
  clientMaxBody?: string
  proxyTimeout?: number
  allowFrom: string[]
  denyFrom: string[]
  basicAuthFile?: string
  basicAuthRealm?: string
  accessLog: boolean
  /** Timed adds the request time traffic analytics reads latency from; empty is timed. */
  logFormat?: "timed" | "combined"
  locations: SiteLocation[]
  custom?: string
}

/**
 * A statement of a site file that saving the form in its place does not
 * write back: a line added by hand the form has no field for.
 */
export type DroppedLine = {
  /** Where it starts in the file, and how many lines `text` takes. */
  line: number
  lines: number
  /** Its own lines of the file, dedented, or the statement on one line where it shares one. */
  text: string
  /** Where it sits: "server", "location /api/", "server on port 80", "outside any server". */
  context: string
  /**
   * It sits directly in the server block the form writes, and the extra
   * configuration, which goes there, keeps it as it is.
   */
  movable: boolean
  /** Why a line of that server block cannot move. */
  reason?: string
}

/** What POST /proxy/sites/preview answers: the file a spec renders to, and where it goes. */
export type SitePreview = {
  content: string
  warnings: string[]
  /** The file a save writes; absent when the host has no site directory. */
  path?: string
  /** Whether a file is already there. */
  exists?: boolean
  /**
   * What holds the name's sites-enabled link when it is another file — a
   * site a new one of this name would unlink. A sentence.
   */
  enabledElsewhere?: string
  /**
   * Whether nginx reads the file: linked into sites-enabled under its own
   * name or another, or in conf.d with a name ending in .conf.
   */
  enabled?: boolean
  /**
   * sites-enabled/<name> is a file of its own, not a link: nginx serves that
   * file under the name, and a save of this one does not reach it.
   */
  servedCopy?: boolean
  /**
   * The host keeps its sites in conf.d: a file is on while its name ends in
   * .conf, and enabling one that does not is renaming it, which a save does
   * not do.
   */
  confd?: boolean
  /** The version of the file there now, when there is one. */
  digest?: string
  /**
   * What saving this spec over that file drops of what the form cannot
   * hold, less what the spec now writes itself; absent when the file is not
   * one the form can read.
   */
  dropped?: DroppedLine[]
}

/** What GET /proxy/sites/{name} answers: a site read back into the form. */
export type SiteRead = {
  spec: SiteSpec
  managed: boolean
  content: string
  warnings: string[]
  /** Whether nginx reads the site; absent when its name is not one the form saves. */
  enabled?: boolean
  /** The site is in conf.d, where it is enabled by renaming it. */
  confd?: boolean
  /** nginx serves sites-enabled/<name>, a file of its own, instead of this one. */
  servedCopy?: boolean
  /** The version of the file read, which a save sends back to be refused if it changed. */
  digest: string
  /** Whether a save of the file as read keeps every statement of it; absent when unknown. */
  lossless?: boolean
  /** What a save of the file as read drops; absent when the form cannot write it at all. */
  dropped?: DroppedLine[]
}

/** One of a site's names another server block also claims on the same address. */
export type ServerNameConflict = {
  domain: string
  /** The address as nginx names it: 0.0.0.0:80, [::]:443. */
  listen: string
  /** The listed site behind the other claim; absent for a block that is not a listed site. */
  site?: string
  /**
   * Which claim nginx answers from — it keeps the first block it reads:
   * `ignored`, the other site keeps the name; `takes`, this site took it;
   * `keeps`, this site already answered it. Absent when the order could not
   * be read.
   */
  effect?: "ignored" | "takes" | "keeps"
}

export type SiteResult = {
  name: string
  path: string
  content: string
  warnings: string[]
  validation?: ProxyValidation
  /** Names another server block also claims, saved anyway or kept. */
  conflicts?: ServerNameConflict[]
  /** nginx's test warnings placed in this site's own file. */
  testWarnings?: ProxyDiagnostic[]
  enabled: boolean
  /**
   * A site saved disabled, tested with its link in place for the test only:
   * `validation`, `conflicts` and `testWarnings` are what enabling it would
   * meet, and none of them refused the save.
   */
  testedAsEnabled?: boolean
  /**
   * nginx serves sites-enabled/<name>, a file of its own, and not this one:
   * the save changed nothing it serves, and was not tested.
   */
  servedCopy?: boolean
  /** Where the hand-written file the save replaced was kept. */
  backup?: string
  reloaded: boolean
  /** Why nginx did not reload a configuration that tested clean. */
  reloadError?: string
  output?: string
}
