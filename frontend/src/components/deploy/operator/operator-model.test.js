import { describe, expect, test } from "bun:test"
import {
  EMPTY_OPERATOR,
  applyEvent,
  beginTurn,
  decideAll,
  decideRead,
  pendingReads,
  proposalRequest,
  proposalVerb,
  ranLine,
  readLines,
  readyDecisions,
  sendDecisions,
  settleProposal,
} from "./operator-model"

const reads = [
  { id: "toolu_1", tool: "recent_runs", label: "Read the last 10 runs", input: { limit: 10 } },
  {
    id: "toolu_2",
    tool: "container_logs",
    label: "Read the last 120 lines the live containers wrote",
    input: { lines: 120 },
  },
]

function asking() {
  let t = beginTurn(EMPTY_OPERATOR, "turn-1", "Why is it down?")
  t = applyEvent(t, "turn-1", {
    type: "start",
    conversation: "c1",
    provider: "anthropic",
    model: "claude-opus-5-5",
    modelLabel: "Claude Opus 5.5",
    tools: true,
  })
  t = applyEvent(t, "turn-1", { type: "text", text: "Reading the runs" })
  t = applyEvent(t, "turn-1", { type: "text", text: " first." })
  return applyEvent(t, "turn-1", {
    type: "done",
    stop: "tool",
    usage: { input: 10, output: 5 },
    model: "claude-opus-5-5",
    kept: true,
    pending: reads,
  })
}

describe("the operator's conversation", () => {
  test("an answer that asks for reads waits on every one of them", () => {
    const t = asking()
    expect(t.conversation).toBe("c1")
    expect(t.turns[0].phase).toBe("asking")
    expect(t.turns[0].parts[0]).toEqual({ kind: "text", text: "Reading the runs first." })
    expect(pendingReads(t).map((call) => call.id)).toEqual(["toolu_1", "toolu_2"])
    // One answered is not all answered: nothing is sent for the other.
    const one = decideRead(t, "toolu_1", true)
    expect(readyDecisions(one)).toBeNull()
    const both = decideRead(one, "toolu_2", false)
    expect(readyDecisions(both)).toEqual({ toolu_1: true, toolu_2: false })
    expect(readyDecisions(decideAll(t, true))).toEqual({ toolu_1: true, toolu_2: true })
    // A read it did not ask for is not answered.
    expect(decideRead(t, "toolu_9", true)).toEqual(t)
  })

  test("sent decisions wait for the rest of the answer, which says what each read came to", () => {
    let t = sendDecisions(decideAll(asking(), true))
    expect(t.turns[0].phase).toBe("waiting")
    expect(pendingReads(t)).toEqual([])
    t = applyEvent(t, "turn-1", {
      type: "ran",
      calls: [
        { id: "toolu_1", tool: "recent_runs", allowed: true, read: "2 runs" },
        { id: "toolu_2", tool: "container_logs", allowed: false },
      ],
    })
    t = applyEvent(t, "turn-1", { type: "text", text: "Run #1 failed." })
    t = applyEvent(t, "turn-1", {
      type: "done",
      stop: "end",
      usage: { input: 20, output: 8 },
      model: "claude-opus-5-5",
      kept: true,
    })
    expect(t.turns[0].phase).toBe("done")
    expect(t.turns[0].parts.map((part) => part.kind)).toEqual(["text", "reads", "ran", "text"])
    expect(ranLine(t.turns[0].parts[2].calls[0])).toBe("Read 2 runs")
    expect(ranLine(t.turns[0].parts[2].calls[1])).toBe("Declined")
    expect(ranLine({ id: "x", tool: "host_alerts", allowed: true, failed: true, read: "no" })).toBe(
      "Could not read: no",
    )
  })

  test("asking something else declines the reads that were waiting", () => {
    const t = beginTurn(asking(), "turn-2", "Never mind: is the disk full?")
    expect(t.turns[0].phase).toBe("done")
    expect(t.turns[0].parts[1].decisions).toEqual({ toolu_1: false, toolu_2: false })
    expect(t.turns[1]).toEqual({
      id: "turn-2",
      question: "Never mind: is the disk full?",
      parts: [],
      phase: "waiting",
    })
    expect(t.conversation).toBe("c1")
  })

  test("a turn that is not kept keeps the conversation it came from", () => {
    let t = beginTurn(EMPTY_OPERATOR, "turn-1", "Why?")
    t = applyEvent(t, "turn-1", {
      type: "start",
      conversation: "c9",
      provider: "anthropic",
      model: "m",
      modelLabel: "M",
      tools: true,
    })
    t = applyEvent(t, "turn-1", {
      type: "done",
      stop: "refusal",
      usage: { input: 1, output: 0 },
      model: "m",
      kept: false,
    })
    expect(t.turns[0].phase).toBe("refused")
    expect(t.conversation).toBe("")
  })

  test("a proposal is a card whose outcome is written in once it is pressed", () => {
    const proposal = {
      id: "toolu_5",
      action: "rollback",
      label: "Roll production back to release #3",
      reason: "Release 3 answered its health check.",
      releaseId: 30,
      releaseNumber: 3,
      capability: "destructive",
      destructive: true,
    }
    let t = beginTurn(EMPTY_OPERATOR, "turn-1", "Fix it")
    t = applyEvent(t, "turn-1", { type: "proposal", proposal })
    expect(t.turns[0].parts).toEqual([{ kind: "proposal", proposal }])
    t = settleProposal(t, "toolu_5", {
      state: "done",
      note: "Run #7 started.",
      href: "/deploy/7/runs/70",
    })
    expect(t.turns[0].parts[0].outcome).toEqual({
      state: "done",
      note: "Run #7 started.",
      href: "/deploy/7/runs/70",
    })
  })
})

describe("a proposal's press", () => {
  const proposal = (action, extra = {}) => ({
    id: "p",
    action,
    label: "",
    reason: "",
    capability: "destructive",
    destructive: true,
    ...extra,
  })

  test("calls the route the project's own verb calls", () => {
    expect(proposalRequest(proposal("restart"), 7, 12)).toEqual({
      method: "POST",
      path: "/deploy/7/environments/12/runs",
      body: { operation: "restart" },
      startsRun: true,
    })
    expect(proposalRequest(proposal("redeploy"), 7, 12).body).toEqual({ operation: "redeploy" })
    expect(proposalRequest(proposal("rollback", { releaseId: 30 }), 7, 12)).toEqual({
      method: "POST",
      path: "/deploy/7/environments/12/rollback",
      body: { releaseId: 30 },
      startsRun: true,
    })
    expect(proposalRequest(proposal("instant_rollback"), 7, 12)).toEqual({
      method: "POST",
      path: "/deploy/7/environments/12/rollback-watch/rollback",
      startsRun: true,
    })
    expect(proposalRequest(proposal("maintenance_on"), 7, 12)).toEqual({
      method: "PUT",
      path: "/deploy/7/environments/12/maintenance",
      body: { on: true },
      startsRun: false,
    })
    expect(proposalRequest(proposal("maintenance_off"), 7, 12).body).toEqual({ on: false })
    expect(proposalRequest(proposal("apply_sizing"), 7, 12)).toEqual({ method: "sizing" })
  })

  test("is named with the verb the project's pages use", () => {
    expect(proposalVerb(proposal("rollback", { releaseNumber: 3 }))).toBe("Roll back to #3")
    expect(proposalVerb(proposal("instant_rollback"))).toBe("Roll back instantly")
    expect(proposalVerb(proposal("maintenance_on"))).toBe("Turn maintenance on")
  })
})

test("a line cut across two reads is kept for the next", () => {
  expect(readLines("", '{"type":"text"}\n{"type":')).toEqual({
    lines: ['{"type":"text"}'],
    rest: '{"type":',
  })
})
