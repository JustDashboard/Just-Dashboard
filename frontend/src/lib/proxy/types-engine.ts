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
 * What POST /proxy/engine/{action} does to the engine's own service. The
 * server picks the unit; start and restart run the config test first.
 */
export type EngineAction = "start" | "restart" | "stop" | "enable" | "reset-failed"

/** What the engine's service route answers: the verb, the unit the server chose, systemctl's words. */
export type EngineControlResult = { action: EngineAction; unit: string; output: string }
