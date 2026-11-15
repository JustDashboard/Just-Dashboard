"use client"

import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { SessionInfo } from "@/lib/types"

/**
 * Whether this session was opened through Just Hub.
 *
 * Such a session is refused every route that makes, changes or hands out a
 * credential (hub-link-protocol §9.9, `httpx.RefuseHubSession`): adding or
 * editing a WireGuard peer, showing a peer's configuration, restoring a
 * tunnel, connecting a DNS engine or changing its connection. A page that
 * drew those controls anyway let the reader fill in a form for the server's
 * refusal to answer, so they are not drawn.
 *
 * The session read does not say how a session was made; the account's own
 * list of sessions does, so this asks it once and finds the one in use. Until
 * that answers, and if it fails, the answer is no: the control is drawn and
 * the server's sentence is what the reader gets, as before.
 */
export function useHubSession(enabled = true): boolean {
  const sessions = usePoll<SessionInfo[]>(
    (signal) => get("/account/sessions", undefined, signal),
    0,
    [],
    // One request for a page, however many of its parts ask.
    { enabled, key: "/account/sessions" },
  )
  return isHubSession(sessions.data)
}

/** The session in use is one a Just Hub sign-in ticket made. */
export function isHubSession(sessions: unknown): boolean {
  return (
    Array.isArray(sessions) &&
    sessions.some(
      (session: Partial<SessionInfo> | null) =>
        Boolean(session?.current) && session?.authMethod === "hub",
    )
  )
}
