/*
 * What every area shares: the capabilities a route is checked against, the
 * long-running job and its lines, and the audit log's entry. A type belongs
 * here only when no one area owns it; keep this file small.
 */

export type Capability =
  | "read"
  | "service.control"
  | "file.write"
  | "terminal"
  | "destructive"
  | "system.admin"
  | "metrics.read"

export type AuditEntry = {
  id: number
  ts: string
  userId: number
  username: string
  role: string
  ip: string
  actor: string
  action: string
  target: string
  method: string
  path: string
  status: number
  success: boolean
  detail: string
  /**
   * The API key a row was written by, when one made the request: an
   * ordinary key, an MCP key or a deploy key alike. Absent for a session.
   */
  keyId?: number
  keyName?: string
  /**
   * A session a Just Hub sign-in ticket opened, and the hub user whose grant opened it, on every
   * row that session wrote (actor `hub-session`, or `assistant` for the assistant acting in one).
   * The id is the one the `hub.signin` row that opened it records. Absent for every other row.
   */
  sessionId?: string
  hubUser?: string
}

/**
 * One long-running operation — a certificate issuance, a package upgrade, an
 * sshd apply.
 *
 * Unlike the compose runner's socket, a job is not owned by the console
 * watching it: closing the tab does not stop it, and reopening picks it up
 * where it is rather than starting it again.
 */
export type JobStatus = "running" | "succeeded" | "failed" | "cancelled"

export type Job = {
  id: string
  kind: string
  title: string
  target?: string
  status: JobStatus
  exitCode: number
  error?: string
  startedAt: string
  endedAt?: string
  startedBy?: string
  /** Every line produced, including any the buffer has dropped. */
  lines: number
  /** What stopping it takes: the capability the route that started it asks for. */
  capability?: Capability
}

export type JobLine = {
  /** Assigned by the job, and what a reconnecting console resumes from. */
  seq: number
  /** stdout, stderr, or status for the runner's own headings. */
  stream: string
  text: string
  at: string
}
