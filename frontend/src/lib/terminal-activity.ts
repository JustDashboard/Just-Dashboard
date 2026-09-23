"use client"

import { useEffect } from "react"
import type { TerminalActivity, TerminalWindowSummary, TerminalWorkspace } from "@/lib/types"
import { useViewState } from "@/lib/view-state"

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
 * Whether something in the session is working, and when the last thing in it
 * finished: the listing's answer for every window, made fresher by the
 * current window's own socket.
 */
export function sessionActivity(
  session: TerminalWorkspace,
  activity: Record<string, TerminalActivity>,
): { working: boolean; finishedAt: number } {
  const live = session.current ? activity[session.current.id] : undefined
  return {
    working: Boolean(session.working) || Boolean(live?.working),
    finishedAt: Math.max(session.finishedAt ?? 0, live?.finishedAt ?? 0),
  }
}

/** How long the finished mark stays on the tab or row you are looking at. */
export const FINISHED_CUE_MS = 4000

/**
 * Whether to show that a window or session has finished working.
 *
 * The mark is a notification, and a notification is for the person who has
 * not seen it yet: on a tab you are not in it stays until you go there, the
 * way a browser tab keeps its badge. On the one you are looking at it shows for
 * a moment and then counts as seen, so the same finish is not announced again
 * on your next visit. "Seen" is kept in the browser with the rest of the
 * page's arrangement, keyed by session or `session/window`, and pruned with
 * the sessions it belongs to.
 */
export function useFinished(key: string, active: boolean, finishedAt: number | undefined): boolean {
  const [viewed, setViewed] = useViewState<Record<string, number>>("terminal.viewed", {})
  const at = finishedAt ?? 0
  const fresh = at > 0 && at > (viewed[key] ?? 0)
  useEffect(() => {
    if (!active || !fresh) return
    const timer = setTimeout(() => setViewed((seen) => ({ ...seen, [key]: at })), FINISHED_CUE_MS)
    return () => clearTimeout(timer)
  }, [active, fresh, at, key, setViewed])
  return fresh
}
