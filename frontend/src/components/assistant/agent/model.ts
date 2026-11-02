import type { AssistantMode } from "@/components/assistant/settings"

/**
 * A conversation with the assistant, as data: the questions asked, the answers as
 * they streamed in, each step it took with what became of it, and the calls waiting
 * on the person.
 *
 * The conversation itself is the server's; this is only the page's reading of it,
 * kept free of React so `bun test` reads it without a DOM.
 */

/** What a call does, which decides whether the mode lets it run. */
export type AgentKind = "free" | "read" | "change" | "destructive"

export interface AgentCall {
  id: string
  tool: string
  /** The dashboard operation it runs, when it runs one. */
  operation?: string
  /** The route, which the mark on its row is read from. */
  route?: string
  /** In the dashboard's words: "Read the last 120 lines the live containers wrote". */
  label: string
  kind: AgentKind
  /** The assistant's own short sentence on why. */
  reason?: string
  input?: unknown
}

export interface AgentDiffLine {
  path: string
  from?: string
  to?: string
}

/** What a change did and the shortcut to it. */
export interface AgentChange {
  label: string
  href: string
  diff?: AgentDiffLine[]
}

export interface AgentLink {
  label: string
  href: string
  note?: string
}

export interface AgentUsage {
  input: number
  output: number
  cacheRead?: number
  cacheWrite?: number
}

export type AgentStop = "end" | "tool" | "length" | "refusal"

/** One line of a streamed answer. */
export type AgentEvent =
  | {
      type: "start"
      conversation: string
      provider: string
      model: string
      modelLabel: string
      tools: boolean
      mode: AssistantMode
    }
  | { type: "thinking" }
  | { type: "text"; text: string }
  | { type: "call"; call: AgentCall }
  | {
      type: "called"
      id: string
      tool: string
      ok: boolean
      declined?: boolean
      said?: string
      output?: string
      change?: AgentChange
    }
  | { type: "link"; link: AgentLink }
  | {
      type: "done"
      stop: AgentStop
      usage: AgentUsage
      model: string
      kept: boolean
      pending?: AgentCall[]
      paused?: boolean
    }
  | { type: "error"; error: { code: string; message: string } }

export type AgentStepState = "running" | "waiting" | "done" | "failed" | "declined"

export interface AgentStep {
  kind: "step"
  call: AgentCall
  state: AgentStepState
  /** The person's answer, once they have given one, before it is sent. */
  decision?: boolean
  said?: string
  output?: string
  change?: AgentChange
}

/** A piece of an answer, in the order it arrived. */
export type AgentPart =
  { kind: "text"; text: string } | AgentStep | { kind: "link"; link: AgentLink }

export type AgentPhase =
  | "waiting"
  | "thinking"
  | "writing"
  | "working"
  | "asking"
  | "done"
  | "stopped"
  | "failed"
  | "refused"
  | "cut"

export interface AgentTurn {
  id: string
  question: string
  parts: AgentPart[]
  phase: AgentPhase
  error?: string
  model?: string
  usage?: AgentUsage
  /** It reached its steps for one press: what it asks next waits, whatever the mode. */
  paused?: boolean
  /** Said above the turn: the model it is asking changed, so it starts fresh. */
  fresh?: string
}

export interface AgentTranscript {
  /** The server's id for the conversation, once a turn of it has been kept. */
  conversation: string
  /** The id the last answer began under, kept only if its turn is. */
  candidate: string
  modelLabel?: string
  provider?: string
  /** Whether the provider answering can be offered tools. */
  tools?: boolean
  turns: AgentTurn[]
  /** What the next turn says above itself. */
  freshNote?: string
}

export const EMPTY_AGENT: AgentTranscript = { conversation: "", candidate: "", turns: [] }

/** The question the Diagnose entry asks when a conversation begins. */
export const DIAGNOSE_QUESTION =
  "Diagnose this deployment: is anything wrong with it or the server under it, what is the likely cause, and what should I do?"

/** Asked when a new deployment's setup is handed to the assistant. */
export const SETUP_QUESTION =
  "Configure this deployment for me: choose the build, the port, the health check and sensible limits, set it up as far as you can, and tell me what you chose and what still needs me before I press Deploy."

/**
 * Splits what has arrived into whole lines, keeping the unfinished last one for the
 * next chunk: a line of JSON is often cut across two reads.
 */
export function readLines(held: string, chunk: string): { lines: string[]; rest: string } {
  const parts = (held + chunk).split("\n")
  const rest = parts.pop() ?? ""
  return { lines: parts.filter((line) => line.trim() !== ""), rest }
}

/** One line as an event, or null for a line this page does not know. */
export function parseEvent(line: string): AgentEvent | null {
  try {
    const event = JSON.parse(line) as AgentEvent
    return event && typeof event === "object" && typeof event.type === "string" ? event : null
  } catch {
    return null
  }
}

function withTurn(
  t: AgentTranscript,
  id: string,
  change: (turn: AgentTurn) => AgentTurn,
): AgentTranscript {
  return { ...t, turns: t.turns.map((turn) => (turn.id === id ? change(turn) : turn)) }
}

const isStep = (part: AgentPart): part is AgentStep => part.kind === "step"

function withStep(turn: AgentTurn, id: string, change: (step: AgentStep) => AgentStep): AgentTurn {
  return {
    ...turn,
    parts: turn.parts.map((part) => (isStep(part) && part.call.id === id ? change(part) : part)),
  }
}

/** The calls the last answer is waiting on, if it is waiting. */
export function pendingSteps(t: AgentTranscript): AgentStep[] {
  const last = t.turns.at(-1)
  if (!last || last.phase !== "asking") return []
  return last.parts.filter(isStep).filter((step) => step.state === "waiting")
}

/**
 * The person's answer to one call. Nothing is sent until every call the answer is
 * waiting on has one, so a call is never declined by not being looked at.
 */
export function decideStep(t: AgentTranscript, id: string, allow: boolean): AgentTranscript {
  const last = t.turns.at(-1)
  if (!last || last.phase !== "asking") return t
  return withTurn(t, last.id, (turn) =>
    withStep(turn, id, (step) => (step.state === "waiting" ? { ...step, decision: allow } : step)),
  )
}

/** Every waiting call answered alike: "Allow all" and "Decline all". */
export function decideAll(t: AgentTranscript, allow: boolean): AgentTranscript {
  return pendingSteps(t).reduce((next, step) => decideStep(next, step.call.id, allow), t)
}

/** The decisions to send, once every waiting call has one; otherwise null. */
export function readyDecisions(t: AgentTranscript): Record<string, boolean> | null {
  const steps = pendingSteps(t)
  if (steps.length === 0) return null
  const decisions: Record<string, boolean> = {}
  for (const step of steps) {
    if (step.decision === undefined) return null
    decisions[step.call.id] = step.decision
  }
  return decisions
}

/** The decisions sent: the turn waits for the rest of its answer. */
export function sendDecisions(t: AgentTranscript): AgentTranscript {
  const last = t.turns.at(-1)
  if (!last) return t
  return withTurn(t, last.id, (turn) => ({
    ...turn,
    phase: "waiting",
    paused: false,
    error: undefined,
  }))
}

/** A new turn, waiting for its first line. */
export function beginTurn(t: AgentTranscript, id: string, question: string): AgentTranscript {
  // Asking something else declines the calls the last answer waited on, as the
  // server does.
  const last = t.turns.at(-1)
  let settled = t
  if (last && last.phase === "asking") {
    settled = withTurn(t, last.id, (turn) => ({
      ...turn,
      phase: "done",
      parts: turn.parts.map((part) =>
        isStep(part) && part.state === "waiting" ? { ...part, state: "declined" as const } : part,
      ),
    }))
  }
  return {
    ...settled,
    candidate: "",
    freshNote: undefined,
    turns: [...settled.turns, { id, question, parts: [], phase: "waiting", fresh: t.freshNote }],
  }
}

function addUsage(a: AgentUsage | undefined, b: AgentUsage): AgentUsage {
  return {
    input: (a?.input ?? 0) + (b.input ?? 0),
    output: (a?.output ?? 0) + (b.output ?? 0),
    cacheRead: (a?.cacheRead ?? 0) + (b.cacheRead ?? 0),
    cacheWrite: (a?.cacheWrite ?? 0) + (b.cacheWrite ?? 0),
  }
}

/** Folds one line of the answer into the turn it belongs to. */
export function applyEvent(t: AgentTranscript, id: string, event: AgentEvent): AgentTranscript {
  switch (event.type) {
    case "start":
      return withTurn(
        {
          ...t,
          candidate: event.conversation,
          modelLabel: event.modelLabel,
          provider: event.provider,
          tools: event.tools,
        },
        id,
        (turn) => ({ ...turn, model: event.modelLabel }),
      )
    case "thinking":
      return withTurn(t, id, (turn) =>
        turn.parts.at(-1)?.kind === "text" ? turn : { ...turn, phase: "thinking" },
      )
    case "text":
      return withTurn(t, id, (turn) => {
        const last = turn.parts.at(-1)
        const parts: AgentPart[] =
          last?.kind === "text"
            ? [...turn.parts.slice(0, -1), { kind: "text", text: last.text + event.text }]
            : [...turn.parts, { kind: "text", text: event.text }]
        return { ...turn, parts, phase: "writing" }
      })
    case "call":
      return withTurn(t, id, (turn) => {
        const known = turn.parts.some((part) => isStep(part) && part.call.id === event.call.id)
        const running: AgentStep = { kind: "step", call: event.call, state: "running" }
        return {
          ...turn,
          phase: "working",
          parts: known
            ? turn.parts.map((part) =>
                isStep(part) && part.call.id === event.call.id ? running : part,
              )
            : [...turn.parts, running],
        }
      })
    case "called":
      return withTurn(t, id, (turn) =>
        withStep(turn, event.id, (step) => ({
          ...step,
          state: event.declined ? "declined" : event.ok ? "done" : "failed",
          decision: undefined,
          said: event.said,
          output: event.output,
          change: event.change,
        })),
      )
    case "link":
      return withTurn(t, id, (turn) => ({
        ...turn,
        parts: [...turn.parts, { kind: "link", link: event.link }],
      }))
    case "done": {
      const waiting = event.pending && event.pending.length > 0
      const phase: AgentPhase =
        event.stop === "length"
          ? "cut"
          : event.stop === "refusal"
            ? "refused"
            : waiting
              ? "asking"
              : "done"
      const kept = event.kept ? { conversation: t.candidate || t.conversation } : {}
      return withTurn({ ...t, ...kept }, id, (turn) => ({
        ...turn,
        phase,
        usage: addUsage(turn.usage, event.usage),
        paused: Boolean(event.paused),
        parts: waiting
          ? [
              ...turn.parts,
              ...event.pending!.map((call): AgentStep => ({
                kind: "step",
                call,
                state: "waiting",
              })),
            ]
          : turn.parts,
      }))
    }
    case "error":
      return endTurn(t, id, "failed", event.error.message)
  }
}

/** A turn that ended without its answer: stopped by the person, or failed. */
export function endTurn(
  t: AgentTranscript,
  id: string,
  phase: "stopped" | "failed",
  error?: string,
): AgentTranscript {
  return withTurn(t, id, (turn) => {
    // A press that failed before it ran what it answered leaves the calls waiting on
    // the server too, so the person is asked again rather than left with a word
    // saying "Allowed" over something that never ran.
    const waiting = turn.parts.some((part) => isStep(part) && part.state === "waiting")
    return {
      ...turn,
      phase: waiting ? "asking" : phase,
      error,
      parts: turn.parts.map((part) => {
        if (!isStep(part)) return part
        // A call under way when the answer ended is not known to have finished.
        if (part.state === "running") {
          return { ...part, state: "failed" as const, said: "Stopped" }
        }
        return part.state === "waiting" ? { ...part, decision: undefined } : part
      }),
    }
  })
}

/** Whether a turn is still coming in. */
export const answering = (turn: AgentTurn | undefined) =>
  turn !== undefined && ["waiting", "thinking", "writing", "working"].includes(turn.phase)

/** The steps a person reads: looking up a route's parameters is not one. */
export const shownSteps = (turn: AgentTurn) =>
  turn.parts.filter(isStep).filter((step) => step.call.kind !== "free")

/** Everything it changed, newest first, for the rail and the dock. */
export function changesOf(t: AgentTranscript): AgentStep[] {
  return t.turns
    .flatMap((turn) => turn.parts.filter(isStep))
    .filter((step) => step.change)
    .reverse()
}

/** The tokens the whole conversation has cost so far. */
export function totalUsage(t: AgentTranscript): AgentUsage {
  return t.turns.reduce<AgentUsage>((sum, turn) => (turn.usage ? addUsage(sum, turn.usage) : sum), {
    input: 0,
    output: 0,
  })
}

const INPUT_LINES = 12

/**
 * What a call would send, a line a value: `runtime.port 3000`, not a block of JSON
 * with six lines of braces around it. The wrapper the route's body comes in says
 * nothing to a person, so it goes.
 */
export function inputLines(input: unknown): {
  lines: { path: string; value: string }[]
  more: number
} {
  const lines: { path: string; value: string }[] = []
  const walk = (value: unknown, path: string) => {
    if (Array.isArray(value)) {
      if (value.length === 0) lines.push({ path, value: "[]" })
      value.forEach((item, at) => walk(item, `${path}[${at}]`))
    } else if (value && typeof value === "object") {
      const entries = Object.entries(value)
      if (entries.length === 0 && path) lines.push({ path, value: "{}" })
      for (const [key, item] of entries) {
        walk(item, path ? `${path}.${key}` : key === "body" ? "" : key)
      }
    } else if (value !== undefined && path) {
      lines.push({ path, value: String(value) })
    }
  }
  walk(input, "")
  return { lines: lines.slice(0, INPUT_LINES), more: Math.max(0, lines.length - INPUT_LINES) }
}

/** The usage line under an answer. */
export function usageLine(turn: AgentTurn): string | undefined {
  const usage = turn.usage
  if (!usage || (usage.input === 0 && usage.output === 0)) return undefined
  return [
    turn.model,
    `${usage.input.toLocaleString("en-US")} tokens in`,
    `${usage.output.toLocaleString("en-US")} out`,
  ]
    .filter(Boolean)
    .join(" · ")
}

/** What it is doing right now, in a few words, for the dock and the page's head. */
export interface AgentStatus {
  tone: "working" | "asking" | "done" | "failed" | "idle"
  text: string
}

export function agentStatus(t: AgentTranscript): AgentStatus {
  const last = t.turns.at(-1)
  if (!last) return { tone: "idle", text: "Ready" }
  switch (last.phase) {
    case "asking": {
      const count = pendingSteps(t).length
      return {
        tone: "asking",
        text: count === 1 ? "Waiting for your answer" : `${count} calls wait for your answer`,
      }
    }
    case "waiting":
    case "thinking":
      return { tone: "working", text: "Thinking…" }
    case "writing":
      return { tone: "working", text: "Writing…" }
    case "working": {
      const running = last.parts.filter(isStep).findLast((step) => step.state === "running")
      return { tone: "working", text: running ? `${running.call.label}…` : "Working…" }
    }
    case "failed":
      return { tone: "failed", text: last.error ?? "It did not answer" }
    case "stopped":
      return { tone: "idle", text: "Stopped" }
    case "refused":
    case "cut":
      return { tone: "failed", text: "It did not finish" }
    case "done": {
      const change = changesOf({ ...t, turns: [last] })
      return {
        tone: "done",
        text:
          change.length === 1
            ? `Changed ${change[0].change!.label}`
            : change.length > 1
              ? `Made ${change.length} changes`
              : "Finished",
      }
    }
  }
}
