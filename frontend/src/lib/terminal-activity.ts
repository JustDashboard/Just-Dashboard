"use client"

import type { TerminalActivity, TerminalWindowSummary, TerminalWorkspace } from "@/lib/types"

/**
 * What to call a window.
 *
 * A name somebody typed is shown as typed. Otherwise the tab follows the
 * shell, the way a desktop terminal's title bar does: the title the running
 * program set, or the program's name when it set none, or the window's default
 * name when nothing is running and the shell has said nothing — which, with
 * the bundled prompts naming every prompt after its directory, only happens
 * before the first prompt is drawn. The server announces a program only once
 * it has lasted a second, so `ls` never gets as far as the tab.
 *
 * `live` is the state the window's own socket last pushed, which is newer than
 * anything a poll carried; a window without a socket in this browser answers
 * from the polled fields on the window itself.
 */
export function windowLabel(window: TerminalWindowSummary, live?: TerminalActivity): string {
  if (window.named) return window.name
  const activity = live ?? window
  return plainTitle(activity.title) || (activity.busy && activity.process) || window.name
}

/**
 * A program's title without the glyph it animates in front of its name.
 * Claude Code and Codex spin an asterisk or a half-moon through their title
 * as their own "I am working"; here that is the activity mark's job, and a
 * label that carried both said it twice — in two vocabularies. Only the
 * leading symbols go; `~`, a path or a word is the title itself.
 */
export function plainTitle(title: string | undefined): string {
  if (!title) return ""
  return title.replace(/^(?:[\p{So}\u00B7\u2022\u2219\u22C5]\s*)+/u, "") || title
}

/** The program a window is running, while one holds it; nothing at the prompt. */
export function windowProgram(window: Partial<TerminalActivity>, live?: TerminalActivity) {
  const state = live ?? window
  return state.busy ? state.process : undefined
}

/** The program a session's current window is running, as its row names it. */
export function sessionProgram(
  session: TerminalWorkspace,
  activity: Record<string, TerminalActivity>,
) {
  return session.current ? windowProgram(session.current, activity[session.current.id]) : undefined
}

/** The window's state, from its socket when it has one here and the poll otherwise. */
export function windowActivity(
  window: Partial<TerminalActivity>,
  live?: TerminalActivity,
): TerminalActivity {
  return live ?? { busy: false, ...window }
}

/**
 * The rail's word for a session: a title somebody typed, else whatever its
 * current window is called — the window last on screen, or the newest.
 */
export function sessionLabel(
  session: TerminalWorkspace,
  activity: Record<string, TerminalActivity>,
): string {
  if (session.named || !session.current) return session.title
  return windowLabel(session.current, activity[session.current.id])
}

/**
 * Whether something in the session is working: the listing's answer for every
 * window, made fresher by the current window's own socket.
 */
export function sessionWorking(
  session: TerminalWorkspace,
  activity: Record<string, TerminalActivity>,
): boolean {
  const live = session.current ? activity[session.current.id] : undefined
  return Boolean(session.working) || Boolean(live?.working)
}
