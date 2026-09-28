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
