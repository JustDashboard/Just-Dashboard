import type { AssistantMode } from "@/components/assistant/settings"

/**
 * A conversation with the assistant, as data: the questions asked, the answers as
 * they streamed in, each step it took with what became of it, and the calls waiting
 * on the person.
 *
 * The conversation itself is the server's; this is only the page's reading of it,
 * kept free of React so `bun test` reads it without a DOM.
 */

/**
 * What a call does, which decides whether the mode lets it run. `server` reaches what
 * the whole server shares rather than this deployment, and waits in every mode.
 */
export type AgentKind = "free" | "read" | "change" | "destructive" | "server"

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

/**
 * Every token the model read. Anthropic counts what it read from its cache, and
 * what it wrote there, apart from `input`, which is then only what was new: a
 * long chat read almost wholly from the cache would say "6 tokens in". An
 * OpenAI-compatible server counts the cache inside `input` and sends neither.
 */
export function inputTokens(usage: AgentUsage): number {
  return usage.input + (usage.cacheRead ?? 0) + (usage.cacheWrite ?? 0)
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
      /** The chat's title: its first question, until it is renamed. */
      title?: string
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
      terminal?: AgentTerminal
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
  /**
   * The person's own acts, which only a saved chat's lines carry: each question
   * asked and each Allow or Decline pressed, in order among what was streamed.
   */
  | {
      type: "question"
      text: string
      images?: AgentSavedImage[]
      /** What the person attached to it, by its label: its text is never kept to draw. */
      attached?: string[]
      at?: string
    }
  | { type: "decisions"; decisions: Record<string, boolean>; at?: string }

export type AgentStepState = "running" | "waiting" | "done" | "failed" | "declined"

/**
 * The chat's terminal window, under a call that typed into it or read it: which
 * window (`/terminal?session=` opens it), what it is doing now, and what it asked.
 */
export interface AgentTerminal {
  session: string
  state: "finished" | "running" | "password" | "input" | "closed"
  prompt?: string
}

/** The terminal's tools, which work in the chat's own window. */
export const TERMINAL_TOOLS = ["terminal_run", "terminal_send", "terminal_read"]

/** The newest call in a chat that left its terminal window in a state worth saying. */
export function latestTerminal(transcript: AgentTranscript): AgentStep | undefined {
  for (let at = transcript.turns.length - 1; at >= 0; at--) {
    const parts = transcript.turns[at].parts
    for (let i = parts.length - 1; i >= 0; i--) {
      const part = parts[i]
      if (part.kind === "step" && TERMINAL_TOOLS.includes(part.call.tool) && part.terminal) {
        return part
      }
    }
  }
}

export interface AgentStep {
  kind: "step"
  call: AgentCall
  state: AgentStepState
  /** The person's answer, once they have given one, before it is sent. */
  decision?: boolean
  said?: string
  output?: string
  change?: AgentChange
  terminal?: AgentTerminal
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
  /** Not sent: the chat is answering in another tab, and waits to be taken over. */
  | "held"
  /** Taken over: another tab carries the chat on, so this one stopped. */
  | "moved"

/**
 * An image as a saved chat's question keeps it: its thumbnail, and the id the
 * whole image is kept under, with its size, for it to be opened large.
 */
export interface AgentSavedImage {
  mediaType: string
  data: string
  id?: string
  width?: number
  height?: number
}

/** An image asked with a question, as the chat draws it and opens it. */
export interface AgentImage {
  /** The thumbnail, as an address an `<img>` draws. */
  thumb: string
  /** The image whole, when this tab has it or the server keeps it. */
  full?: string
  width?: number
  height?: number
}

export interface AgentTurn {
  id: string
  question: string
  /** The images asked with it. */
  images?: AgentImage[]
  /** The labels of what it carried — "statement", "result · 20 rows" — never their text. */
  attached?: string[]
  parts: AgentPart[]
  phase: AgentPhase
  error?: string
  model?: string
  usage?: AgentUsage
  /** It reached its steps for one press: what it asks next waits, whatever the mode. */
  paused?: boolean
  /** Said above the turn: the model it is asking changed, so it starts fresh. */
  fresh?: string
  /** Read back from a saved chat rather than seen arriving, so it does not rise. */
  replayed?: boolean
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
  /** The saved chat's title. */
  title?: string
  /** The page the chat was begun from, which its first question told the server. */
  about?: AgentAbout
}

/** A page of the dashboard a chat was begun from: where it is, and what it is called. */
export interface AgentAbout {
  path: string
  title: string
}

/**
 * Something the person attached to a question, which the model reads with it: the
 * statement in the editor, a result, an error, a plan. The page draws only the label.
 */
export interface AgentAttachment {
  label: string
  text: string
}

/** How many a question carries, and how long each part may be, as the server bounds them. */
export const MAX_ATTACHED = 4
export const ATTACHED_LABEL = 60
export const ATTACHED_TEXT = 20_000

/**
 * What a question carries, as the server takes it: at most four, each label and
 * text cut to its length, and an empty one left out. A text cut short says so, so
 * the model does not read the end of a table as the end of the data.
 */
export function boundAttached(attached: AgentAttachment[]): AgentAttachment[] {
  return attached
    .map((one) => ({ label: one.label.trim(), text: one.text }))
    .filter((one) => one.label !== "" && one.text.trim() !== "")
    .slice(0, MAX_ATTACHED)
    .map((one) => ({
      label: clip(one.label, ATTACHED_LABEL),
      text:
        [...one.text].length > ATTACHED_TEXT
          ? `${[...one.text].slice(0, ATTACHED_TEXT - 2).join("")}\n…`
          : one.text,
    }))
}

/** A string cut to `max` characters, counted as a person counts them, with an ellipsis. */
function clip(text: string, max: number) {
  const points = [...text]
  return points.length > max ? `${points.slice(0, max - 1).join("")}…` : text
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
export function beginTurn(
  t: AgentTranscript,
  id: string,
  question: string,
  images: AgentImage[] = [],
  attached: string[] = [],
): AgentTranscript {
  // A question that was never sent, because another tab had the chat, gives way to
  // this one.
  const base = t.turns.at(-1)?.phase === "held" ? { ...t, turns: t.turns.slice(0, -1) } : t
  // Asking something else declines the calls the last answer waited on, as the
  // server does.
  const last = base.turns.at(-1)
  let settled = base
  if (last && last.phase === "asking") {
    settled = withTurn(base, last.id, (turn) => ({
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
    turns: [
      ...settled.turns,
      {
        id,
        question,
        ...(images.length > 0 && { images }),
        ...(attached.length > 0 && { attached }),
        parts: [],
        phase: "waiting",
        fresh: t.freshNote,
      },
    ],
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
          title: event.title || t.title,
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
          terminal: event.terminal,
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
      return event.error.code === "taken_over"
        ? endTurn(t, id, "moved", event.error.message)
        : endTurn(t, id, "failed", event.error.message)
    case "question":
    case "decisions":
      // A saved chat's own lines, which `replayLines` reads; a stream never sends them.
      return t
  }
}

/** A turn that ended without its answer: stopped by the person, or failed. */
export function endTurn(
  t: AgentTranscript,
  id: string,
  phase: "stopped" | "failed" | "moved",
  error?: string,
): AgentTranscript {
  return withTurn(t, id, (turn) => {
    // A press that failed before it ran what it answered leaves the calls waiting on
    // the server too, so the person is asked again rather than left with a word
    // saying "Allowed" over something that never ran. A press taken over is the
    // other tab's to answer.
    const waiting = turn.parts.some((part) => isStep(part) && part.state === "waiting")
    return {
      ...turn,
      phase: waiting && phase !== "moved" ? "asking" : phase,
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

/**
 * A press the server refused because another tab is answering the chat: a question
 * waits, unsent, to be taken over; Allow and Decline keep their answers for the
 * same press.
 */
export function holdTurn(t: AgentTranscript, id: string): AgentTranscript {
  return withTurn(t, id, (turn) => {
    const waiting = turn.parts.some((part) => isStep(part) && part.state === "waiting")
    return { ...turn, phase: waiting ? "asking" : "held", error: undefined }
  })
}

/**
 * Taking the chat over: the press held here goes ahead, and what the other tab was
 * answering stops, as the server stops it.
 */
export function takeOverTurn(t: AgentTranscript, id: string): AgentTranscript {
  const settled = t.turns.reduce(
    (next, turn) => (turn.id !== id && answering(turn) ? endTurn(next, turn.id, "stopped") : next),
    t,
  )
  return withTurn(settled, id, (turn) => ({ ...turn, phase: "waiting", paused: false }))
}

/**
 * A saved chat read back: its lines are the person's questions and answers among
 * everything that was streamed, so folding them through the same reducer draws the
 * chat exactly as it looked. A press still going in another tab is left answering;
 * one that ended without its last line, as a restart ends one, reads as stopped.
 */
export function replayLines(
  lines: unknown[],
  {
    running = false,
    imageAt,
  }: {
    running?: boolean
    /** Where an image the server keeps whole is read, by its id. */
    imageAt?: (id: string) => string
  } = {},
): AgentTranscript {
  let t = EMPTY_AGENT
  let turn = ""
  for (const line of lines) {
    if (!line || typeof line !== "object" || typeof (line as AgentEvent).type !== "string") continue
    const event = line as AgentEvent
    if (event.type === "question") {
      turn = `replay-${t.turns.length + 1}`
      const images = Array.isArray(event.images)
        ? event.images
            .filter((one) => typeof one?.mediaType === "string" && typeof one?.data === "string")
            .map((one): AgentImage => {
              const image: AgentImage = { thumb: `data:${one.mediaType};base64,${one.data}` }
              if (typeof one.id === "string" && one.id && imageAt) image.full = imageAt(one.id)
              if (Number(one.width) > 0 && Number(one.height) > 0) {
                image.width = Number(one.width)
                image.height = Number(one.height)
              }
              return image
            })
        : []
      const attached = Array.isArray(event.attached)
        ? event.attached.filter((label): label is string => typeof label === "string" && !!label)
        : []
      t = beginTurn(t, turn, typeof event.text === "string" ? event.text : "", images, attached)
      t = withTurn(t, turn, (one) => ({ ...one, replayed: true }))
      continue
    }
    if (!turn) continue
    if (event.type === "decisions") {
      let next = t
      for (const [id, allow] of Object.entries(event.decisions ?? {})) {
        next = decideStep(next, id, Boolean(allow))
      }
      // A call left out of the answer is declined by the server, and its own line
      // says so; the turn waits for the rest as it did.
      t = t.turns.at(-1)?.phase === "asking" ? sendDecisions(next) : next
      continue
    }
    t = applyEvent(t, turn, event)
  }
  const last = t.turns.at(-1)
  if (last && answering(last) && !running) t = endTurn(t, last.id, "stopped")
  return { ...t, conversation: t.conversation || t.candidate }
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
  if (!usage || (inputTokens(usage) === 0 && usage.output === 0)) return undefined
  return [
    turn.model,
    `${inputTokens(usage).toLocaleString("en-US")} tokens in`,
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
    case "held":
      return { tone: "asking", text: "Running in another tab" }
    case "moved":
      return { tone: "idle", text: "Continued somewhere else" }
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
