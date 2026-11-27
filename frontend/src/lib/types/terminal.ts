/*
 * The terminal: sessions and their workspaces, windows, and what each shell
 * is doing.
 */

/** `GET /terminal/`: who a new shell signs in as, and every session open on the server. */
export type TerminalList = {
  enabled: boolean
  /** Whether new sessions can be protected across dashboard restarts. */
  persistent: boolean
  persistenceError?: string
  login: { user: string; home: string; shell: string; error?: string }
  sessions: TerminalWorkspace[]
}

/** One session: a named workspace containing one or more direct PTYs. */
export type TerminalWorkspace = {
  id: string
  title: string
  /** Whether the operator chose the title. A default one follows the shell. */
  named?: boolean
  folder?: string
  favourite: boolean
  live: boolean
  cwd?: string
  windows: number
  createdAt: string
  attached: number
  user?: string
  shell?: string
  owner?: string
  /** Whether any window has a program in the foreground. */
  busy?: boolean
  /** Whether any window is working, and when the last one finished (ms since the epoch). */
  working?: boolean
  finishedAt?: number
  /** The window that stands for the session: the one last shown, else the newest. */
  current?: TerminalWindowSummary
}

/**
 * What a direct PTY is doing, as the backend reads it off the terminal: the
 * title the foreground program set, whether anything but the prompt holds the
 * terminal, and what that is. Polled with the window lists and pushed over
 * the attach socket as a `state` frame whenever it changes.
 */
export type TerminalActivity = {
  /** The OSC title, when it was set by whatever is in the foreground now. */
  title?: string
  /** A program has held the terminal for longer than a blink. */
  busy: boolean
  /** The foreground command, while busy. */
  process?: string
  /**
   * Something is actually happening: output has been arriving for a second
   * and still is, or the job is burning CPU. An editor or an agent waiting at
   * its prompt is busy but not working.
   */
  working?: boolean
  /** When the last stretch of work ended, in ms since the epoch, until work starts again. */
  finishedAt?: number
}

export type TerminalWindowSummary = {
  id: string
  name: string
  /** Whether the operator named the window rather than it carrying a default. */
  named?: boolean
} & Partial<TerminalActivity>

/** An independent direct PTY shown as a window tab inside one session. */
export type TerminalWindow = TerminalWindowSummary & {
  index: number
  cwd?: string
}
