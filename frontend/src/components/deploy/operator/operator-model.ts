import type { Capability } from "@/lib/types"

/**
 * The deployment operator's conversation, as data: the questions asked about
 * one environment, the answers as they streamed in, the reads the assistant
 * asked for with the person's answer to each, and the actions it proposed
 * with what became of each once somebody pressed it.
 *
 * The conversation itself is the server's (`POST /deploy/{id}/assistant`);
 * this is only the page's reading of it. Kept free of React, so `bun test`
 * reads it without a DOM.
 */

/** A read the assistant asks for, waiting on the person's answer. */
export interface OperatorRead {
  id: string
  tool: string
  /** The read in the dashboard's words: "Read the last 120 lines the live containers wrote". */
  label: string
  input: Record<string, unknown>
}

export type OperatorAction =
  | "restart"
  | "redeploy"
  | "rollback"
  | "instant_rollback"
  | "apply_sizing"
  | "maintenance_on"
  | "maintenance_off"

/** An action the assistant proposed. Nothing ran when it was drawn. */
export interface OperatorProposal {
  id: string
  action: OperatorAction
  label: string
  reason: string
  releaseId?: number
  releaseNumber?: number
  /** What the action's own route asks of the person. */
  capability: Capability
  destructive: boolean
}

/** One read as it ran, or did not. */
export interface OperatorRan {
  id: string
  tool: string
  allowed: boolean
  /** A few words of what was read: "12 runs". */
  read?: string
  failed?: boolean
}

export interface OperatorUsage {
  input: number
  output: number
  cacheRead?: number
  cacheWrite?: number
}

export type OperatorStop = "end" | "tool" | "length" | "refusal"

/** One line of a streamed answer. */
export type OperatorEvent =
  | {
      type: "start"
      conversation: string
      provider: string
      model: string
      modelLabel: string
      tools: boolean
    }
  | { type: "thinking" }
  | { type: "text"; text: string }
  | { type: "ran"; calls: OperatorRan[] }
  | { type: "proposal"; proposal: OperatorProposal }
  | {
      type: "done"
      stop: OperatorStop
      usage: OperatorUsage
      model: string
      kept: boolean
      pending?: OperatorRead[]
    }
  | { type: "error"; error: { code: string; message: string } }

/** What became of a proposal once it was pressed. */
export interface ProposalOutcome {
  state: "working" | "done" | "failed"
  note?: string
  /** The run it started, to open. */
  href?: string
}

/** A piece of an answer, in the order it arrived. */
export type OperatorPart =
  | { kind: "text"; text: string }
  /** The reads it asked for; `decisions` fill as the person answers each. */
  | { kind: "reads"; calls: OperatorRead[]; decisions: Record<string, boolean>; sent?: boolean }
  | { kind: "ran"; calls: OperatorRan[] }
  | { kind: "proposal"; proposal: OperatorProposal; outcome?: ProposalOutcome }

export type OperatorPhase =
  "waiting" | "thinking" | "writing" | "asking" | "done" | "stopped" | "failed" | "refused" | "cut"

export interface OperatorTurn {
  id: string
  question: string
  parts: OperatorPart[]
  phase: OperatorPhase
  error?: string
  model?: string
  usage?: OperatorUsage
}

export interface OperatorTranscript {
  /** The server's id for the conversation, once a turn of it has been kept. */
  conversation: string
  /** The id the last answer began under, kept only if its turn is. */
  candidate: string
  modelLabel?: string
  provider?: string
  turns: OperatorTurn[]
}

export const EMPTY_OPERATOR: OperatorTranscript = { conversation: "", candidate: "", turns: [] }

/** The question the Diagnose entry asks when a conversation begins. */
export const DIAGNOSE_QUESTION =
  "Diagnose this deployment: is anything wrong with it or the server under it, what is the likely cause, and what should I do?"

/**
 * Splits what has arrived into whole lines, keeping the unfinished last one
 * for the next chunk: a line of JSON is often cut across two reads.
 */
export function readLines(held: string, chunk: string): { lines: string[]; rest: string } {
  const parts = (held + chunk).split("\n")
  const rest = parts.pop() ?? ""
  return { lines: parts.filter((line) => line.trim() !== ""), rest }
}

/** One line as an event, or null for a line this page does not know. */
export function parseEvent(line: string): OperatorEvent | null {
  try {
    const event = JSON.parse(line) as OperatorEvent
    return event && typeof event === "object" && typeof event.type === "string" ? event : null
  } catch {
    return null
  }
}

function withTurn(
  t: OperatorTranscript,
  id: string,
  change: (turn: OperatorTurn) => OperatorTurn,
): OperatorTranscript {
  return { ...t, turns: t.turns.map((turn) => (turn.id === id ? change(turn) : turn)) }
}

/** The reads the last answer is waiting on, if it is waiting. */
export function pendingReads(t: OperatorTranscript): OperatorRead[] {
  const last = t.turns.at(-1)
  if (!last || last.phase !== "asking") return []
  const reads = last.parts.findLast((part) => part.kind === "reads")
  return reads?.kind === "reads" && !reads.sent ? reads.calls : []
}

function readsAt(turn: OperatorTurn) {
  return turn.parts.findLastIndex((part) => part.kind === "reads" && !part.sent)
}

/**
 * The person's answer to one read. Nothing is sent until every read the
 * answer asked for has one, so a read is never declined by not being looked
 * at.
 */
export function decideRead(t: OperatorTranscript, id: string, allow: boolean): OperatorTranscript {
  const last = t.turns.at(-1)
  if (!last || last.phase !== "asking") return t
  return withTurn(t, last.id, (turn) => {
    const at = readsAt(turn)
    if (at < 0) return turn
    const part = turn.parts[at] as Extract<OperatorPart, { kind: "reads" }>
    if (!part.calls.some((call) => call.id === id)) return turn
    const parts = [...turn.parts]
    parts[at] = { ...part, decisions: { ...part.decisions, [id]: allow } }
    return { ...turn, parts }
  })
}

/** Every waiting read answered alike: "Allow all" and "Decline all". */
export function decideAll(t: OperatorTranscript, allow: boolean): OperatorTranscript {
  return pendingReads(t).reduce((next, call) => decideRead(next, call.id, allow), t)
}

/** The decisions to send, once every waiting read has one; otherwise null. */
export function readyDecisions(t: OperatorTranscript): Record<string, boolean> | null {
  const reads = pendingReads(t)
  if (reads.length === 0) return null
  const last = t.turns.at(-1)!
  const part = last.parts[readsAt(last)] as Extract<OperatorPart, { kind: "reads" }>
  const decisions: Record<string, boolean> = {}
  for (const call of reads) {
    if (part.decisions[call.id] === undefined) return null
    decisions[call.id] = part.decisions[call.id]
  }
  return decisions
}

/** The decisions sent: the turn waits for the rest of its answer. */
export function sendDecisions(t: OperatorTranscript): OperatorTranscript {
  const last = t.turns.at(-1)
  if (!last) return t
  return withTurn(t, last.id, (turn) => {
    const at = readsAt(turn)
    if (at < 0) return turn
    const parts = [...turn.parts]
    parts[at] = { ...(parts[at] as Extract<OperatorPart, { kind: "reads" }>), sent: true }
    return { ...turn, parts, phase: "waiting" }
  })
}

/** A new turn, waiting for its first line. */
export function beginTurn(t: OperatorTranscript, id: string, question: string): OperatorTranscript {
  // Asking something else declines the reads the last answer waited on, as
  // the server does.
  const last = t.turns.at(-1)
  let settled = t
  if (last && pendingReads(t).length > 0) {
    settled = withTurn(sendDecisions(decideAll(t, false)), last.id, (turn) => ({
      ...turn,
      phase: "done",
    }))
  }
  return {
    ...settled,
    candidate: "",
    turns: [...settled.turns, { id, question, parts: [], phase: "waiting" }],
  }
}

/** Folds one line of the answer into the turn it belongs to. */
export function applyEvent(
  t: OperatorTranscript,
  id: string,
  event: OperatorEvent,
): OperatorTranscript {
  switch (event.type) {
    case "start":
      return withTurn(
        {
          ...t,
          candidate: event.conversation,
          modelLabel: event.modelLabel,
          provider: event.provider,
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
        const parts: OperatorPart[] =
          last?.kind === "text"
            ? [...turn.parts.slice(0, -1), { kind: "text", text: last.text + event.text }]
            : [...turn.parts, { kind: "text", text: event.text }]
        return { ...turn, parts, phase: "writing" }
      })
    case "ran":
      return withTurn(t, id, (turn) => ({
        ...turn,
        parts: [...turn.parts, { kind: "ran", calls: event.calls ?? [] }],
      }))
    case "proposal":
      return withTurn(t, id, (turn) => ({
        ...turn,
        parts: [...turn.parts, { kind: "proposal", proposal: event.proposal }],
      }))
    case "done": {
      const phase: OperatorPhase =
        event.stop === "length"
          ? "cut"
          : event.stop === "refusal"
            ? "refused"
            : event.pending && event.pending.length > 0
              ? "asking"
              : "done"
      const kept = event.kept ? { conversation: t.candidate || t.conversation } : {}
      return withTurn({ ...t, ...kept }, id, (turn) => ({
        ...turn,
        phase,
        usage: event.usage,
        parts:
          event.pending && event.pending.length > 0
            ? [...turn.parts, { kind: "reads", calls: event.pending, decisions: {} }]
            : turn.parts,
      }))
    }
    case "error":
      return endTurn(t, id, "failed", event.error.message)
  }
}

/** A turn that ended without its answer: stopped by the person, or failed. */
export function endTurn(
  t: OperatorTranscript,
  id: string,
  phase: "stopped" | "failed",
  error?: string,
): OperatorTranscript {
  return withTurn(t, id, (turn) => ({ ...turn, phase, error }))
}

/** What became of a proposal, written into the turn it was drawn in. */
export function settleProposal(
  t: OperatorTranscript,
  proposalId: string,
  outcome: ProposalOutcome,
): OperatorTranscript {
  return {
    ...t,
    turns: t.turns.map((turn) =>
      turn.parts.some((part) => part.kind === "proposal" && part.proposal.id === proposalId)
        ? {
            ...turn,
            parts: turn.parts.map((part) =>
              part.kind === "proposal" && part.proposal.id === proposalId
                ? { ...part, outcome }
                : part,
            ),
          }
        : turn,
    ),
  }
}

/** Whether a turn is still coming in. */
export const answering = (turn: OperatorTurn | undefined) =>
  turn !== undefined && ["waiting", "thinking", "writing"].includes(turn.phase)

/**
 * The request a proposal's press sends: the route the project's own verb for
 * that action calls, so it carries that route's capability check, its
 * confirmation and its audit. Applying the suggested limits is a read and a
 * save of the environment's configuration, which the Right-size card does,
 * so it has no single request here.
 */
export type ProposalRequest =
  | { method: "POST" | "PUT"; path: string; body?: Record<string, unknown>; startsRun: boolean }
  | { method: "sizing" }

export function proposalRequest(
  proposal: OperatorProposal,
  projectId: number,
  environmentId: number,
): ProposalRequest {
  const base = `/deploy/${projectId}/environments/${environmentId}`
  switch (proposal.action) {
    case "restart":
    case "redeploy":
      return {
        method: "POST",
        path: `${base}/runs`,
        body: { operation: proposal.action },
        startsRun: true,
      }
    case "rollback":
      return {
        method: "POST",
        path: `${base}/rollback`,
        body: { releaseId: proposal.releaseId },
        startsRun: true,
      }
    case "instant_rollback":
      return { method: "POST", path: `${base}/rollback-watch/rollback`, startsRun: true }
    case "maintenance_on":
    case "maintenance_off":
      return {
        method: "PUT",
        path: `${base}/maintenance`,
        body: { on: proposal.action === "maintenance_on" },
        startsRun: false,
      }
    case "apply_sizing":
      return { method: "sizing" }
  }
}

/** The command a proposal's card carries: the verb the project's pages use for it. */
export function proposalVerb(proposal: OperatorProposal): string {
  switch (proposal.action) {
    case "restart":
      return "Restart"
    case "redeploy":
      return "Redeploy"
    case "rollback":
      return proposal.releaseNumber ? `Roll back to #${proposal.releaseNumber}` : "Roll back"
    case "instant_rollback":
      return "Roll back instantly"
    case "apply_sizing":
      return "Apply the limits"
    case "maintenance_on":
      return "Turn maintenance on"
    case "maintenance_off":
      return "Turn maintenance off"
  }
}

/** What pressing it does, in the confirmation's words. */
export function proposalConsequence(proposal: OperatorProposal, environment: string): string {
  switch (proposal.action) {
    case "restart":
      return `The live release's containers on ${environment} restart. Requests fail while they come back up.`
    case "redeploy":
      return `The live release of ${environment} is started again from its image, behind the same checks as any deployment.`
    case "rollback":
      return `${environment} switches to release #${proposal.releaseNumber}, with that release's configuration. The live release stays retained.`
    case "instant_rollback":
      return `Traffic on ${environment} goes back to warm release #${proposal.releaseNumber}, which was kept paused since the cutover. The current release is removed.`
    case "apply_sizing":
      return `The suggested limits are saved to ${environment}'s runtime settings. They apply with the next release.`
    case "maintenance_on":
      return `Visitors to ${environment} see the maintenance page instead of the application until it is turned off.`
    case "maintenance_off":
      return `${environment} answers its visitors again in place of the maintenance page.`
  }
}

/** The usage line under an answer. */
export function usageLine(turn: OperatorTurn): string | undefined {
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

/** A read's line once it ran: "Allowed · 12 runs", "Declined", "Failed · the runs could not be read". */
export function ranLine(call: OperatorRan): string {
  if (!call.allowed) return "Declined"
  if (call.failed) return call.read ? `Could not read: ${call.read}` : "Could not read"
  return call.read ? `Read ${call.read}` : "Read"
}
