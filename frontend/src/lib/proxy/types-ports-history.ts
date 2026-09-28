import type { Listener } from "./types-ports"

/** A listening socket as GET /ports lists it, dated by the ports history. */
export type SeenListener = Listener & {
  /**
   * When the history first saw it listening. Absent for a socket already
   * listening when recording began, and for one opened since the last
   * sample, a minute at most.
   */
  firstSeen?: string
}

/**
 * A socket opening or closing, as GET /ports/history reads it: the socket as
 * it was, placed and graded as GET /ports places one listening now.
 */
export type PortEvent = Listener & {
  kind: "opened" | "closed"
  /**
   * The sample that saw the change, and the one before it: the socket opened
   * or closed between the two. A minute apart while the dashboard runs, and
   * further across a restart or a sample that failed.
   */
  at: string
  after: string
  /** When the socket was first seen listening. */
  since: string
  /** It was already listening when recording began, so it opened before `since`. */
  baseline?: boolean
}

/** GET /ports/history?hours=: what opened and closed in a window. */
export type PortHistory = {
  /** The first sample ever taken, and the latest; null before the first. */
  recordingSince: string | null
  lastSample: string | null
  /**
   * The latest sample is more than three intervals old: the dashboard
   * answered, so its samples are failing, and nothing after `lastSample` is
   * recorded yet.
   */
  stalled: boolean
  intervalSeconds: number
  retentionDays: number
  /** Where the window starts. */
  since: string
  /** Newest first; within one sample by port, a closing before an opening. */
  events: PortEvent[]
  /** The window held more events than one answer carries. */
  truncated: boolean
}
