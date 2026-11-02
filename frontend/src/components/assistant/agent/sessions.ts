"use client"

import { useCallback, useSyncExternalStore } from "react"
import { ApiError, errorMessage, fetchResponse } from "@/lib/api"
import type { AssistantMode } from "@/components/assistant/settings"
import {
  EMPTY_AGENT,
  answering,
  applyEvent,
  beginTurn,
  decideAll,
  decideStep,
  endTurn,
  parseEvent,
  readLines,
  readyDecisions,
  sendDecisions,
  type AgentEvent,
  type AgentTranscript,
} from "@/components/assistant/agent/model"

/**
 * The conversations going on in this tab, kept outside any page.
 *
 * Following a shortcut to what the assistant changed must not hang up on it, so a
 * conversation is not the page's: it goes on streaming wherever the person is, and
 * the dock in the shell says how it is doing and goes back to it. Remote Desktop's
 * sessions outlive their page the same way.
 *
 * What was drawn is held in memory for the tab, never in Web Storage: an answer
 * quotes the application's own log lines. Signing out ends every one
 * (`endAgentSessions`), and so does a reload, as the server's own copy ends with
 * its process.
 */

/** What a conversation is about, and where its page is. */
export type AgentScope =
  | {
      kind: "deployment"
      projectId: number
      environmentId: number
      /** `?environment=` for staging; production's is the default. */
      environment?: number
      name: string
      environmentName: string
    }
  | { kind: "draft"; draftId: string; name: string }

export interface AgentSession {
  key: string
  scope: AgentScope
  transcript: AgentTranscript
  /** The mode chosen in this conversation, over the install's default. */
  mode?: AssistantMode
  /** Left out of the dock until something new happens in it. */
  dismissed: boolean
  flight: AbortController | null
  /**
   * How many of its calls have written something, for a page to read itself again
   * on: a preflight changes a draft's revision without being a change to show.
   */
  changes: number
}

export const agentScopeKey = (scope: AgentScope) =>
  scope.kind === "draft" ? `draft.${scope.draftId}` : `deploy.${scope.environmentId}`

export const agentPageOf = (scope: AgentScope) =>
  // The new-project page opens exactly where it was left; naming the draft would ask
  // it to start that draft over.
  scope.kind === "draft" ? "/deploy/new" : `/deploy/${scope.projectId}/assistant`

const sessions = new Map<string, AgentSession>()
let version = 0
let snapshot: AgentSession[] = []
const listeners = new Set<() => void>()

function publish() {
  version += 1
  snapshot = [...sessions.values()]
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

function sessionFor(scope: AgentScope): AgentSession {
  const key = agentScopeKey(scope)
  let session = sessions.get(key)
  if (!session) {
    session = {
      key,
      scope,
      transcript: EMPTY_AGENT,
      dismissed: false,
      flight: null,
      changes: 0,
    }
    sessions.set(key, session)
    snapshot = [...sessions.values()]
  } else {
    session.scope = scope
  }
  return session
}

function update(session: AgentSession, change: (s: AgentTranscript) => AgentTranscript) {
  session.transcript = change(session.transcript)
  publish()
}

/** Whether a call that just finished was one that writes. */
function wrote(transcript: AgentTranscript, id: string) {
  return transcript.turns.some((turn) =>
    turn.parts.some(
      (part) =>
        part.kind === "step" &&
        part.call.id === id &&
        (part.call.kind === "change" || part.call.kind === "destructive"),
    ),
  )
}

async function stream(session: AgentSession, turn: string, body: Record<string, unknown>) {
  const controller = new AbortController()
  session.flight = controller
  session.dismissed = false
  publish()
  const { scope } = session
  const path =
    scope.kind === "draft"
      ? `/deploy/drafts/${encodeURIComponent(scope.draftId)}/assistant`
      : `/deploy/${scope.projectId}/assistant`
  const query =
    scope.kind === "deployment" && scope.environment
      ? { environment: scope.environment }
      : undefined
  let ended = false
  const apply = (event: AgentEvent) => {
    if (event.type === "called" && event.ok && wrote(session.transcript, event.id)) {
      session.changes += 1
    }
    update(session, (t) => applyEvent(t, turn, event))
  }
  try {
    const response = await fetchResponse(
      path,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(session.mode ? { ...body, mode: session.mode } : body),
        signal: controller.signal,
      },
      query,
    )
    if (!response.body) throw new Error("The answer arrived with nothing in it")
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let held = ""
    const take = (lines: string[]) => {
      for (const line of lines) {
        const event = parseEvent(line)
        if (!event) continue
        if (event.type === "done" || event.type === "error") ended = true
        apply(event)
      }
    }
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      const read = readLines(held, decoder.decode(value, { stream: true }))
      held = read.rest
      take(read.lines)
    }
    take(readLines(held, "\n").lines)
    if (!ended) {
      update(session, (t) => endTurn(t, turn, "failed", "The answer stopped before it finished."))
    }
  } catch (err) {
    if (controller.signal.aborted) {
      update(session, (t) => endTurn(t, turn, "stopped"))
    } else if (
      err instanceof ApiError &&
      ["conversation_gone", "conversation_ended"].includes(err.code)
    ) {
      // The next question begins a conversation of its own.
      update(session, (t) => ({ ...endTurn(t, turn, "failed", err.message), conversation: "" }))
    } else if (!ended) {
      update(session, (t) => endTurn(t, turn, "failed", errorMessage(err)))
    }
  } finally {
    if (session.flight === controller) session.flight = null
    publish()
  }
}

export function askAgent(session: AgentSession, question: string) {
  if (session.flight) return
  const turn = crypto.randomUUID()
  session.transcript = beginTurn(session.transcript, turn, question)
  publish()
  void stream(session, turn, { conversation: session.transcript.conversation, question })
}

/**
 * One call answered; once every call the answer waits on has an answer, they go to
 * the server together.
 */
export function decideAgent(session: AgentSession, id: string | "all", allow: boolean) {
  const last = session.transcript.turns.at(-1)
  if (session.flight || !last || last.phase !== "asking") return
  const next =
    id === "all" ? decideAll(session.transcript, allow) : decideStep(session.transcript, id, allow)
  const decisions = readyDecisions(next)
  if (!decisions) {
    session.transcript = next
    publish()
    return
  }
  session.transcript = sendDecisions(next)
  publish()
  void stream(session, last.id, { conversation: session.transcript.conversation, decisions })
}

export function stopAgent(session: AgentSession) {
  session.flight?.abort()
}

export function resetAgent(session: AgentSession) {
  session.flight?.abort()
  session.flight = null
  session.transcript = EMPTY_AGENT
  session.changes = 0
  publish()
}

export function setAgentMode(session: AgentSession, mode: AssistantMode) {
  session.mode = mode
  publish()
}

/** The provider changed: what it says next it says from nothing. */
export function agentModelChanged(label: string) {
  for (const session of sessions.values()) {
    if (session.transcript.turns.length === 0) continue
    session.transcript = {
      ...session.transcript,
      conversation: "",
      freshNote: `Now answered by ${label}. It starts from here, without what was said above.`,
    }
  }
  publish()
}

export function dismissAgent(session: AgentSession) {
  session.dismissed = true
  publish()
}

/** Signing out ends every conversation, and what was drawn of them. */
export function endAgentSessions() {
  for (const session of sessions.values()) session.flight?.abort()
  sessions.clear()
  publish()
}

/** One conversation, for the page that shows it. */
export function useAgentSession(scope: AgentScope) {
  const key = agentScopeKey(scope)
  const read = useCallback(() => {
    void version
    return sessionFor(scope)
    // A scope is read for its key; the rest of it follows from the same subject.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  const session = useSyncExternalStore(subscribe, read, read)
  useSyncExternalStore(
    subscribe,
    () => version,
    () => 0,
  )
  return session
}

/** Whether a conversation is answering right now. */
export const agentBusy = (session: AgentSession) =>
  session.flight !== null || answering(session.transcript.turns.at(-1))

const NONE: AgentSession[] = []

/** Every conversation in this tab, for the dock. */
export function useAgentSessions(): AgentSession[] {
  const list = useSyncExternalStore(
    subscribe,
    () => snapshot,
    () => NONE,
  )
  useSyncExternalStore(
    subscribe,
    () => version,
    () => 0,
  )
  return list
}
