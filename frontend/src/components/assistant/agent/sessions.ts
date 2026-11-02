"use client"

import { useCallback, useSyncExternalStore } from "react"
import { ApiError, errorMessage, fetchResponse, get } from "@/lib/api"
import type { AssistantMode } from "@/components/assistant/settings"
import {
  EMPTY_AGENT,
  answering,
  applyEvent,
  beginTurn,
  decideAll,
  decideStep,
  endTurn,
  holdTurn,
  parseEvent,
  readLines,
  readyDecisions,
  replayLines,
  sendDecisions,
  takeOverTurn,
  type AgentEvent,
  type AgentTranscript,
} from "@/components/assistant/agent/model"
import type { AgentChatSummary } from "@/components/assistant/agent/chats"

/**
 * The conversations going on in this tab, kept outside any page.
 *
 * Following a shortcut to what the assistant changed must not hang up on it, so a
 * conversation is not the page's: it goes on streaming wherever the person is, and
 * the dock in the shell says how it is doing and goes back to it. Remote Desktop's
 * sessions outlive their page the same way.
 *
 * Each subject — a deployment's environment, a draft, the server — has one chat in
 * front at a time; another opened from its history, or a new one, takes its place
 * while the one before goes on answering. A chat is the server's and is saved
 * there, so one opened again is read back from it (`openAgentChat`), and one another
 * tab is answering is followed until it ends or the person takes it over. What was
 * drawn is held in memory for the tab, never in Web Storage: an answer quotes the
 * application's own log lines. Signing out ends every one (`endAgentSessions`).
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
  | { kind: "server"; name: string }

export interface AgentSession {
  /** This tab's own name for it, which never changes. */
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
  /** The saved chat it was opened from, before any line of it is read. */
  opened?: string
  /** Being read back from the server. */
  loading: boolean
  loadError?: string
  /** A press another tab's answer kept from going, sent again on Take over. */
  held?: { turn: string; body: Record<string, unknown> }
  /** Another tab is answering it; it is followed until that ends. */
  elsewhere: boolean
  /** Another tab took it over, so this one stopped. */
  moved: boolean
}

export const agentScopeKey = (scope: AgentScope) =>
  scope.kind === "draft"
    ? `draft.${scope.draftId}`
    : scope.kind === "server"
      ? "server"
      : `deploy.${scope.environmentId}`

export const agentPageOf = (scope: AgentScope) =>
  // The new-project page opens exactly where it was left; naming the draft would ask
  // it to start that draft over.
  scope.kind === "draft"
    ? "/deploy/new"
    : scope.kind === "server"
      ? "/assistant"
      : `/deploy/${scope.projectId}/assistant`

/** The saved chat a session is, once the server has named it. */
export const agentChatId = (session: AgentSession) =>
  session.transcript.conversation || session.transcript.candidate || session.opened || ""

/** Where a saved chat is read and carried on, from its summary. */
export function agentChatHref(chat: Pick<AgentChatSummary, "id" | "scope">) {
  const { scope } = chat
  if (scope.kind === "deployment" && scope.deployment)
    return `/deploy/${scope.deployment}/assistant?chat=${encodeURIComponent(chat.id)}`
  if (scope.kind === "draft" && scope.draft)
    return `/deploy/new?draft=${encodeURIComponent(scope.draft)}`
  return `/assistant?chat=${encodeURIComponent(chat.id)}`
}

const sessions = new Map<string, AgentSession>()
/** The chat in front for each subject. */
const current = new Map<string, string>()
let version = 0
/** Bumped when a chat is begun, ends a press, or is renamed or deleted. */
let chatsEpoch = 0
let snapshot: AgentSession[] = []
const listeners = new Set<() => void>()
const chatListeners = new Set<() => void>()
const following = new Map<string, number>()

function publish() {
  version += 1
  snapshot = [...sessions.values()]
  for (const listener of listeners) listener()
}

/** The saved chats changed: every list of them reads itself again. */
function chatsChanged() {
  chatsEpoch += 1
  for (const listener of chatListeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** Called whenever this tab begins, finishes, renames or deletes a saved chat. */
export function onAgentChatsChanged(listener: () => void) {
  chatListeners.add(listener)
  return () => {
    chatListeners.delete(listener)
  }
}

function blank(scope: AgentScope, mode?: AssistantMode): AgentSession {
  return {
    key: crypto.randomUUID(),
    scope,
    transcript: EMPTY_AGENT,
    mode,
    dismissed: false,
    flight: null,
    changes: 0,
    loading: false,
    elsewhere: false,
    moved: false,
  }
}

function install(session: AgentSession) {
  sessions.set(session.key, session)
  current.set(agentScopeKey(session.scope), session.key)
  snapshot = [...sessions.values()]
}

/** The chat in front for a subject, begun empty the first time it is asked for. */
function currentFor(scope: AgentScope): AgentSession {
  const key = current.get(agentScopeKey(scope))
  const session = key ? sessions.get(key) : undefined
  if (session) {
    session.scope = scope
    return session
  }
  const fresh = blank(scope)
  install(fresh)
  return fresh
}

/** Whether a session is the one in front for its subject. */
export const isCurrentAgent = (session: AgentSession) =>
  current.get(agentScopeKey(session.scope)) === session.key

/** Where the dock's way back goes: the page, naming the chat when it is not in front. */
export function agentHref(session: AgentSession) {
  const page = agentPageOf(session.scope)
  const id = agentChatId(session)
  if (session.scope.kind === "draft" || !id || isCurrentAgent(session)) return page
  return `${page}?chat=${encodeURIComponent(id)}`
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

function turnPath(scope: AgentScope) {
  if (scope.kind === "draft") return `/deploy/drafts/${encodeURIComponent(scope.draftId)}/assistant`
  if (scope.kind === "server") return "/assistant/chat"
  return `/deploy/${scope.projectId}/assistant`
}

/** The chat is answering a press in another tab: the server's word for it. */
const BUSY = ["chat_busy", "conversation_busy"]

async function stream(session: AgentSession, turn: string, body: Record<string, unknown>) {
  const controller = new AbortController()
  session.flight = controller
  session.dismissed = false
  session.moved = false
  session.elsewhere = false
  stopFollowing(session)
  publish()
  const { scope } = session
  const query =
    scope.kind === "deployment" && scope.environment
      ? { environment: scope.environment }
      : undefined
  const sent = session.mode ? { ...body, mode: session.mode } : body
  let ended = false
  let begun = false
  const apply = (event: AgentEvent) => {
    if (event.type === "called" && event.ok && wrote(session.transcript, event.id)) {
      session.changes += 1
    }
    if (event.type === "error" && event.error?.code === "taken_over") session.moved = true
    update(session, (t) => applyEvent(t, turn, event))
    if (event.type === "start" && !begun) {
      begun = true
      // A new chat is in the history from its first line, with its dot lit.
      chatsChanged()
    }
  }
  try {
    const response = await fetchResponse(
      turnPath(scope),
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(sent),
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
    } else if (err instanceof ApiError && BUSY.includes(err.code)) {
      // Nothing was sent: the press waits for the person to take the chat over.
      session.held = { turn, body }
      session.elsewhere = true
      update(session, (t) => holdTurn(t, turn))
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
    chatsChanged()
  }
}

export function askAgent(session: AgentSession, question: string) {
  if (session.flight) return
  // Asking something else lets go of a press that was waiting to take over.
  session.held = undefined
  const turn = crypto.randomUUID()
  session.transcript = beginTurn(session.transcript, turn, question)
  publish()
  void stream(session, turn, { conversation: session.transcript.conversation, question })
}

/**
 * Carrying the chat on here while another tab answers it: the press that was kept
 * from going goes now, and the server stops the other one. With nothing held, it is
 * asked to carry on from where it was.
 */
export function takeOverAgent(session: AgentSession) {
  if (session.flight) return
  const held = session.held
  session.held = undefined
  if (held) {
    const waiting = session.transcript.turns.at(-1)?.phase === "asking"
    session.transcript = waiting
      ? sendDecisions(session.transcript)
      : takeOverTurn(session.transcript, held.turn)
    publish()
    void stream(session, held.turn, { ...held.body, takeOver: true })
    return
  }
  // Nothing of their own to ask: the last turn, which the other tab was answering,
  // is carried on here from where the server put it down.
  const last = session.transcript.turns.at(-1)
  if (!last) return
  session.transcript = takeOverTurn(session.transcript, last.id)
  publish()
  void stream(session, last.id, {
    conversation: session.transcript.conversation || agentChatId(session),
    takeOver: true,
  })
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

/**
 * A new chat in front for the subject. The one before goes on if it is answering,
 * and stays in the history either way.
 */
export function newAgentChat(scope: AgentScope): AgentSession {
  const before = currentFor(scope)
  if (before.transcript.turns.length === 0 && !before.loading && !before.opened) return before
  const fresh = blank(scope, before.mode)
  install(fresh)
  publish()
  return fresh
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

function stopFollowing(session: AgentSession) {
  const timer = following.get(session.key)
  if (timer !== undefined) window.clearTimeout(timer)
  following.delete(session.key)
}

/** How often a chat another tab is answering is read again. */
const FOLLOW_EVERY = 2500

/**
 * A chat another tab is answering, read again until that ends, so what is drawn here
 * keeps up with it. Nothing here is replaced while this tab has a press of its own.
 */
function follow(session: AgentSession) {
  stopFollowing(session)
  const id = agentChatId(session)
  if (!id) return
  const timer = window.setTimeout(async () => {
    following.delete(session.key)
    if (sessions.get(session.key) !== session || !session.elsewhere) return
    if (session.flight || session.held) return
    try {
      const read = await get<{ chat: AgentChatSummary; lines: unknown[] }>(
        `/assistant/chats/${encodeURIComponent(id)}`,
      )
      if (!session.elsewhere || session.flight || session.held) return
      session.transcript = readBack(read, id)
      session.elsewhere = read.chat.running
      publish()
      if (!read.chat.running) chatsChanged()
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        session.elsewhere = false
        publish()
        return
      }
    }
    if (session.elsewhere) follow(session)
  }, FOLLOW_EVERY)
  following.set(session.key, timer)
}

function readBack(read: { chat: AgentChatSummary; lines: unknown[] }, id: string) {
  const transcript = replayLines(read.lines ?? [], { running: read.chat.running })
  return { ...transcript, conversation: id, title: read.chat.title || transcript.title }
}

/**
 * A saved chat put in front for its subject: the one this tab already holds, or one
 * read back from the server. A deployment's chat from its other environment is not
 * opened here; the page is told which environment it belongs to instead.
 */
export async function openAgentChat(
  scope: AgentScope,
  id: string,
): Promise<{ environment?: number } | undefined> {
  const subject = agentScopeKey(scope)
  const known = [...sessions.values()].find(
    (session) => agentScopeKey(session.scope) === subject && agentChatId(session) === id,
  )
  if (known) {
    current.set(subject, known.key)
    known.scope = scope
    publish()
    // What this tab holds of it may be behind another tab's, unless it is its own.
    if (known.flight || known.held || known.loading) return
    await reread(known, scope)
    return
  }
  const before = currentFor(scope)
  const session: AgentSession = { ...blank(scope, before.mode), opened: id, loading: true }
  install(session)
  publish()
  const other = await reread(session, scope)
  if (other !== undefined) {
    // Not this environment's: the chat in front stays what it was.
    sessions.delete(session.key)
    if (current.get(subject) === session.key) current.set(subject, before.key)
    publish()
    return { environment: other }
  }
  return
}

/**
 * Reads a chat back from the server into a session. Answers the environment it
 * belongs to when that is not the scope's, and reads nothing into it then.
 */
async function reread(session: AgentSession, scope: AgentScope): Promise<number | undefined> {
  const id = agentChatId(session)
  const fresh = session.transcript.turns.length === 0
  if (fresh) {
    session.loading = true
    session.loadError = undefined
    publish()
  }
  try {
    const read = await get<{ chat: AgentChatSummary; lines: unknown[] }>(
      `/assistant/chats/${encodeURIComponent(id)}`,
    )
    const environment = read.chat.scope?.environment
    if (scope.kind === "deployment" && environment && environment !== scope.environmentId) {
      return environment
    }
    if (session.flight || session.held) return
    session.transcript = readBack(read, id)
    session.elsewhere = read.chat.running
    session.moved = false
    session.loading = false
    publish()
    if (session.elsewhere) follow(session)
  } catch (err) {
    session.loading = false
    if (fresh) session.loadError = errorMessage(err)
    publish()
  }
  return undefined
}

/** A chat renamed: what this tab holds of it says so too. */
export function agentChatRenamed(id: string, title: string) {
  for (const session of sessions.values()) {
    if (agentChatId(session) === id) session.transcript = { ...session.transcript, title }
  }
  publish()
  chatsChanged()
}

/** A chat deleted: one in front gives way to a new one. */
export function agentChatDeleted(id: string) {
  for (const session of [...sessions.values()]) {
    if (agentChatId(session) !== id) continue
    stopFollowing(session)
    const front = isCurrentAgent(session)
    sessions.delete(session.key)
    if (front) {
      current.delete(agentScopeKey(session.scope))
      install(blank(session.scope, session.mode))
    }
  }
  publish()
  chatsChanged()
}

/** Signing out ends every conversation, and what was drawn of them. */
export function endAgentSessions() {
  for (const session of sessions.values()) {
    session.flight?.abort()
    stopFollowing(session)
  }
  sessions.clear()
  current.clear()
  publish()
}

/** The chat in front for a subject, for the page that shows it. */
export function useAgentSession(scope: AgentScope) {
  const key = agentScopeKey(scope)
  const read = useCallback(() => {
    void version
    return currentFor(scope)
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

/** Whether a conversation is answering in this tab right now. */
export const agentBusy = (session: AgentSession) =>
  session.flight !== null || (!session.elsewhere && answering(session.transcript.turns.at(-1)))

/** Whether it is answering anywhere: here, or in the tab it is followed from. */
export const agentWorking = (session: AgentSession) =>
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

/** The number that moves when the saved chats do. */
export function useAgentChatsEpoch() {
  return useSyncExternalStore(
    onAgentChatsChanged,
    () => chatsEpoch,
    () => 0,
  )
}
