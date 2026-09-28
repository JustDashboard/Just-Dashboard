/**
 * One leveled line of the engine's config test, placed where it names a file
 * and line. nginx's words (emerg … info), or Caddy's (error, warn, panic,
 * fatal).
 */
export type ProxyDiagnostic = {
  level: "emerg" | "alert" | "crit" | "error" | "warn" | "notice" | "info" | "panic" | "fatal"
  message: string
  file?: string
  line?: number
  /**
   * Where a warning nginx gives no file for is claimed: the server blocks
   * that name the server name it says is conflicting, in the order nginx
   * read them. Given only when every such block could be found.
   */
  claims?: ProxyNameClaim[]
}

/**
 * A server block claiming a server name. The first nginx read serves the name
 * on that address; `ignored` is each one after it. `file` and `line` are the
 * block's server_name line, or its `server` line when the name comes from a
 * file it includes — a snippet several sites share, named by `nameFile` and
 * `nameLine`, whose one line could not say which site is which.
 */
export type ProxyNameClaim = {
  file: string
  line: number
  nameFile?: string
  nameLine?: number
  ignored: boolean
}

/**
 * The server's own config test. `note` qualifies a verdict nginx could not
 * actually give — a file outside its include tree passes `nginx -t` without
 * being read. `warnings` counts the warn-level diagnostics, because nginx
 * passes a config it is quietly ignoring part of.
 */
export type ProxyValidation = {
  valid: boolean
  output: string
  command: string
  note?: string
  diagnostics?: ProxyDiagnostic[]
  warnings?: number
}

/**
 * What POST /proxy/reload answers: the config test it ran first, and whether
 * the engine took the signal. A reload the test refuses is a 422 whose body
 * carries the test beside the error.
 */
export type ProxyReloadResult = { validation: ProxyValidation; reloaded: boolean; output: string }

/**
 * The engine's most recent test of the files on disk, whichever command ran
 * it — Test config, a reload, a start or restart, a config editor save — as
 * GET /proxy/test/last answers it. Kept in the dashboard's memory, so there
 * is none after it restarts until the next test.
 */
export type ProxyTestRecord = {
  kind: "nginx" | "caddy" | "caddy-ingress"
  /** When the test began, which is when the engine read the files. */
  checkedAt: string
  validation: ProxyValidation
}

/**
 * What POST /proxy/engine/{action} does to the engine's own service. The
 * server picks the unit; start and restart run the config test first.
 */
export type EngineAction = "start" | "restart" | "stop" | "enable" | "reset-failed"

/** What the engine's service route answers: the verb, the unit the server chose, systemctl's words. */
export type EngineControlResult = { action: EngineAction; unit: string; output: string }

/**
 * One file under the nginx directory, as GET /proxy/files lists it. Whether
 * nginx reads it is worked out from the files on disk, following nginx.conf's
 * includes the way nginx does, so it answers for a configuration that fails
 * its test too.
 */
export type ProxyConfigEntry = {
  path: string
  /** nginx.conf, a symlink (a sites-enabled entry, a module), a password file, or any other file. */
  kind: "main" | "link" | "password" | "file"
  size: number
  modified: string
  /** nginx reads it: an include reaches it, or reaches the link that points at it. */
  included: boolean
  /** The include that first reaches it; `via` is the link nginx opens it through. */
  includedBy?: { file: string; line: number; via?: string }
  /** The dashboard wrote it: a site or stream with its marker, or one of its password files. */
  managed: boolean
  /** A password file: listed, never read. */
  protected: boolean
  /** An editor's or package manager's backup that nginx nevertheless reads. */
  backup?: boolean
  /** Where a link points, every link resolved. */
  target?: string
  /** A link to a file outside the proxy's directories, which the editor does not open. */
  outside?: boolean
  /** A link to nothing, which nginx refuses to start over. */
  missing?: boolean
}

/** Why the includes could not all be followed, placed where it can be. */
export type ProxyConfigProblem = { message: string; file?: string; line?: number }

/** GET /proxy/files: the nginx directory, three folders deep. */
export type ProxyConfigFiles = {
  root: string
  main: string
  files: ProxyConfigEntry[]
  /** False when a file nginx reads does not parse: a file not marked read may still be. */
  includesKnown: boolean
  problem?: ProxyConfigProblem
  truncated: boolean
}

/** One file nginx loads, as `nginx -T` printed it; `target` is the file a link resolves to. */
export type ProxyEffectiveFile = { path: string; target?: string; content: string }

/**
 * One statement of the configuration nginx loads, placed: its file and line
 * as nginx printed them, and the blocks it sits in — "http", "server
 * app.example.com", "location /api".
 */
export type ProxyPlacedDirective = {
  name: string
  args: string[]
  file: string
  line: number
  within: string[]
  opens: boolean
}

/**
 * GET /proxy/effective: what nginx loads, in its reading order, and when it
 * printed it. `directives` is null when the reader could not build the tree,
 * with `treeError` saying why; a configuration nginx refuses is a 422 whose
 * body carries its test.
 */
export type ProxyEffectiveConfig = {
  files: ProxyEffectiveFile[]
  directives: ProxyPlacedDirective[] | null
  treeError?: string
  checkedAt: string
}

/**
 * One kept state of a configuration file. `action` is what the dashboard did
 * — write, delete, enable, disable, rename, restore — or `outside` for a
 * state found on disk that the dashboard did not write, and `baseline` for
 * the file as the first recorded change found it. `existed` false is the
 * file being removed.
 */
export type ProxyRevision = {
  id: number
  path: string
  sha256: string
  size: number
  existed: boolean
  action: string
  actor: string
  /** Unix seconds. */
  createdAt: number
}

/** A recorded file as it is now; `drift` when it no longer holds its newest revision. */
export type ProxyRevisionDisk = {
  exists: boolean
  sha256?: string
  drift: boolean
  unreadable?: string
}

/** GET /proxy/history/files: every file with a history, newest change first. */
export type ProxyHistoryFiles = {
  files: { path: string; revisions: number; latest: ProxyRevision; current: ProxyRevisionDisk }[]
}

/** GET /proxy/history?path=: one file's revisions, newest first. */
export type ProxyHistory = {
  path: string
  revisions: ProxyRevision[]
  current: ProxyRevisionDisk
}

/** GET /proxy/history/{id}: a revision beside the one before it and the file now. */
export type ProxyRevisionDetail = {
  revision: ProxyRevision
  content: string
  previous: { revision: ProxyRevision; content: string } | null
  current: ProxyRevisionDisk
  currentContent: string
}

/** GET /proxy/settings: one of nginx's server-wide directives as nginx loads it. */
export type ProxySetting = {
  name: string
  context: "http" | "events"
  value: string
  /** False when no file sets it and `value` is nginx's default. */
  set: boolean
  file?: string
  line?: number
  /** Server and location blocks that set it again, where this value does not apply. */
  overrides: number
  default: string
  level: "ok" | "notice" | "warning"
  advice: string
  /** The value Apply would write; absent when there is no one value to recommend. */
  recommended?: string
  /** The values the directive takes, for a closed choice. */
  choices?: string[]
}

export type ProxySettings = {
  settings: ProxySetting[]
  /** Descriptors each worker may hold; 0 when it could not be read. */
  openFiles: number
  openFilesFrom?: string
  /** The file a directive no file sets is added to. */
  target: string
}

/** POST /proxy/settings/preview and PUT /proxy/settings: what a change does to one file. */
export type ProxySettingEdit = {
  name: string
  value: string
  file: string
  line: number
  /** The directive's lines before; empty when the change adds it. */
  before: string
  after: string
  created: boolean
  /** The nginx package owns this file as a dpkg conffile. */
  conffile: boolean
}

/** GET /proxy/lint: one legal-but-probably-unmeant thing in what nginx loads. */
export type ProxyLintFinding = {
  id: string
  rule: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  file: string
  line: number
}

export type ProxyLint = { findings: ProxyLintFinding[]; checkedAt: string }
