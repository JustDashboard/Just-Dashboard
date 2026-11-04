import { describe, expect, test } from "bun:test"
import {
  ATTACHED_LABEL,
  ATTACHED_TEXT,
  EMPTY_AGENT,
  MAX_ATTACHED,
  agentStatus,
  answering,
  applyEvent,
  beginTurn,
  boundAttached,
  changesOf,
  decideAll,
  decideStep,
  endTurn,
  holdTurn,
  inputLines,
  parseEvent,
  pendingSteps,
  readLines,
  readyDecisions,
  replayLines,
  sendDecisions,
  shownSteps,
  takeOverTurn,
  totalUsage,
  usageLine,
  inputTokens,
} from "./model"

const read = { id: "a", tool: "recent_runs", label: "Read the last 10 runs", kind: "read" }
const restart = {
  id: "b",
  tool: "call_operation",
  operation: "deployment_run_create",
  route: "POST /deploy/{id}/environments/{env}/runs",
  label: "Queue a run",
  kind: "destructive",
  reason: "It is stuck.",
}
const usage = { input: 100, output: 10 }

const started = () => {
  const t = beginTurn(EMPTY_AGENT, "t1", "why")
  return applyEvent(t, "t1", {
    type: "start",
    conversation: "c1",
    provider: "anthropic",
    model: "m",
    modelLabel: "Claude",
    tools: true,
    mode: "changes",
  })
}

const asking = () =>
  applyEvent(started(), "t1", {
    type: "done",
    stop: "tool",
    usage,
    model: "m",
    kept: true,
    pending: [read, restart],
  })

describe("reading the stream", () => {
  test("a line cut across two reads is held for the next", () => {
    expect(readLines("", '{"a":1}\n{"b"')).toEqual({ lines: ['{"a":1}'], rest: '{"b"' })
    expect(readLines('{"b"', ":2}\n")).toEqual({ lines: ['{"b":2}'], rest: "" })
  })
  test("a line this page cannot read is left out", () => {
    expect(parseEvent("not json")).toBeNull()
    expect(parseEvent("3")).toBeNull()
    expect(parseEvent('{"type":"thinking"}')).toEqual({ type: "thinking" })
  })
})

describe("a turn", () => {
  test("text arrives into one part, and a call starts another", () => {
    let t = applyEvent(started(), "t1", { type: "text", text: "Read" })
    t = applyEvent(t, "t1", { type: "text", text: "ing." })
    t = applyEvent(t, "t1", { type: "call", call: read })
    expect(t.turns[0].parts.map((part) => part.kind)).toEqual(["text", "step"])
    expect(t.turns[0].parts[0].text).toBe("Reading.")
    expect(t.turns[0].phase).toBe("working")
    expect(answering(t.turns[0])).toBe(true)
    expect(agentStatus(t)).toEqual({ tone: "working", text: "Read the last 10 runs…" })
  })
  test("a call that ran says what it came to, and its change", () => {
    let t = applyEvent(started(), "t1", { type: "call", call: restart })
    t = applyEvent(t, "t1", {
      type: "called",
      id: "b",
      tool: "call_operation",
      ok: true,
      said: "Done",
      change: { label: "Run #12", href: "/deploy/7/runs/40" },
    })
    const step = t.turns[0].parts[0]
    expect(step.state).toBe("done")
    expect(changesOf(t).map((one) => one.change.label)).toEqual(["Run #12"])
    t = applyEvent(t, "t1", { type: "done", stop: "end", usage, model: "m", kept: true })
    expect(agentStatus(t)).toEqual({ tone: "done", text: "Changed Run #12" })
  })
  test("a call the route refused reads as failed", () => {
    let t = applyEvent(started(), "t1", { type: "call", call: restart })
    t = applyEvent(t, "t1", { type: "called", id: "b", tool: "x", ok: false, said: "Forbidden" })
    expect(t.turns[0].parts[0].state).toBe("failed")
  })
  test("the conversation is the server's only once the turn is kept", () => {
    expect(started().conversation).toBe("")
    const kept = applyEvent(started(), "t1", {
      type: "done",
      stop: "end",
      usage,
      model: "m",
      kept: true,
    })
    expect(kept.conversation).toBe("c1")
    const lost = applyEvent(started(), "t1", {
      type: "done",
      stop: "refusal",
      usage,
      model: "m",
      kept: false,
    })
    expect(lost.conversation).toBe("")
    expect(lost.turns[0].phase).toBe("refused")
  })
  test("looking up a route is not a step a person reads", () => {
    const free = { id: "f", tool: "describe_operation", label: "Look up", kind: "free" }
    let t = applyEvent(started(), "t1", { type: "call", call: free })
    t = applyEvent(t, "t1", { type: "call", call: read })
    expect(shownSteps(t.turns[0]).map((step) => step.call.id)).toEqual(["a"])
  })
  test("stopping leaves no call reading as running", () => {
    const t = endTurn(applyEvent(started(), "t1", { type: "call", call: read }), "t1", "stopped")
    expect(t.turns[0].phase).toBe("stopped")
    expect(t.turns[0].parts[0].state).toBe("failed")
    expect(answering(t.turns[0])).toBe(false)
  })
  test("usage adds up over the presses of a turn and over the conversation", () => {
    let t = asking()
    t = sendDecisions(decideAll(t, true))
    t = applyEvent(t, "t1", { type: "done", stop: "end", usage, model: "m", kept: true })
    expect(t.turns[0].usage.input).toBe(200)
    expect(totalUsage(t).output).toBe(20)
    expect(usageLine(t.turns[0])).toBe("Claude · 200 tokens in · 20 out")
    expect(usageLine({ ...t.turns[0], usage: { input: 0, output: 0 } })).toBeUndefined()
  })
  test("what the model read from its cache counts as tokens in", () => {
    const cached = { input: 6, output: 571, cacheRead: 41_200, cacheWrite: 900 }
    expect(inputTokens(cached)).toBe(42_106)
    expect(
      usageLine({
        id: "t",
        question: "q",
        parts: [],
        phase: "done",
        model: "Claude",
        usage: cached,
      }),
    ).toBe("Claude · 42,106 tokens in · 571 out")
    expect(inputTokens({ input: 300, output: 1 })).toBe(300)
  })
})

describe("the calls waiting on the person", () => {
  test("nothing is sent until every call has an answer", () => {
    let t = asking()
    expect(pendingSteps(t).length).toBe(2)
    expect(agentStatus(t).tone).toBe("asking")
    t = decideStep(t, "a", true)
    expect(readyDecisions(t)).toBeNull()
    t = decideStep(t, "b", false)
    expect(readyDecisions(t)).toEqual({ a: true, b: false })
  })
  test("Allow all answers each", () => {
    expect(readyDecisions(decideAll(asking(), true))).toEqual({ a: true, b: true })
    expect(readyDecisions(decideAll(asking(), false))).toEqual({ a: false, b: false })
  })
  test("sending them waits for the rest, and each then runs", () => {
    let t = sendDecisions(decideAll(asking(), true))
    expect(t.turns[0].phase).toBe("waiting")
    expect(answering(t.turns[0])).toBe(true)
    t = applyEvent(t, "t1", { type: "call", call: read })
    expect(t.turns[0].parts[0].state).toBe("running")
    expect(t.turns[0].parts.filter((part) => part.kind === "step").length).toBe(2)
    t = applyEvent(t, "t1", { type: "called", id: "b", tool: "x", ok: false, declined: true })
    expect(t.turns[0].parts[1].state).toBe("declined")
  })
  test("a question declines what waits, as the server does", () => {
    const t = beginTurn(asking(), "t2", "actually")
    expect(t.turns[0].phase).toBe("done")
    expect(t.turns[0].parts.every((part) => part.state === "declined")).toBe(true)
    expect(t.turns[1].phase).toBe("waiting")
    expect(pendingSteps(t)).toEqual([])
  })
  test("an answer that fails asks again, rather than saying Allowed over nothing", () => {
    const sent = sendDecisions(decideAll(asking(), true))
    const t = endTurn(sent, "t1", "failed", "It is busy")
    expect(t.turns[0].phase).toBe("asking")
    expect(t.turns[0].error).toBe("It is busy")
    expect(pendingSteps(t).length).toBe(2)
    expect(pendingSteps(t).every((step) => step.decision === undefined)).toBe(true)
    // And answering clears the word that said it failed.
    expect(sendDecisions(decideAll(t, true)).turns[0].error).toBeUndefined()
  })
  test("a pause is said on the turn, and answering clears it", () => {
    const t = applyEvent(started(), "t1", {
      type: "done",
      stop: "tool",
      usage,
      model: "m",
      kept: true,
      pending: [read],
      paused: true,
    })
    expect(t.turns[0].paused).toBe(true)
    expect(sendDecisions(decideAll(t, true)).turns[0].paused).toBe(false)
  })
  test("a change of model is said above the next turn", () => {
    const t = beginTurn({ ...EMPTY_AGENT, freshNote: "Now Haiku" }, "t1", "hi")
    expect(t.turns[0].fresh).toBe("Now Haiku")
    expect(t.freshNote).toBeUndefined()
  })
})

describe("what a call would send", () => {
  test("reads as lines, without the body it comes in", () => {
    expect(inputLines({ body: { runtime: { port: 3000 }, domains: ["a.example"] } }).lines).toEqual(
      [
        { path: "runtime.port", value: "3000" },
        { path: "domains[0]", value: "a.example" },
      ],
    )
    expect(inputLines({ limit: 5 }).lines).toEqual([{ path: "limit", value: "5" }])
    expect(inputLines({ flag: false, none: [] }).lines).toEqual([
      { path: "flag", value: "false" },
      { path: "none", value: "[]" },
    ])
  })
  test("nothing to say is nothing", () => {
    expect(inputLines(undefined).lines).toEqual([])
    expect(inputLines({}).lines).toEqual([])
  })
  test("a long one is bounded and says how much is left", () => {
    const body = Object.fromEntries(Array.from({ length: 20 }, (_, at) => [`k${at}`, at]))
    const { lines, more } = inputLines({ body })
    expect(lines.length).toBe(12)
    expect(more).toBe(8)
  })
})

describe("what a question carries", () => {
  test("its attachments are drawn by their labels, never their text", () => {
    const t = beginTurn(EMPTY_AGENT, "t1", "why did it fail", [], ["statement", "error"])
    expect(t.turns[0].attached).toEqual(["statement", "error"])
    // A question with nothing attached carries no empty list.
    expect(beginTurn(EMPTY_AGENT, "t2", "hello").turns[0]).not.toHaveProperty("attached")
  })

  test("goes within the server's bounds: four at most, each label and text cut", () => {
    const sent = boundAttached([
      { label: "  statement ", text: "SELECT 1" },
      { label: "nothing", text: "  " },
      { label: "", text: "orphan" },
      { label: "r".repeat(ATTACHED_LABEL + 10), text: "x".repeat(ATTACHED_TEXT + 500) },
      { label: "c", text: "c" },
      { label: "d", text: "d" },
      { label: "e", text: "e" },
    ])
    expect(sent).toHaveLength(MAX_ATTACHED)
    expect(sent[0]).toEqual({ label: "statement", text: "SELECT 1" })
    expect([...sent[1].label]).toHaveLength(ATTACHED_LABEL)
    expect([...sent[1].text].length).toBeLessThanOrEqual(ATTACHED_TEXT)
    expect(sent[1].text.endsWith("…")).toBe(true)
    expect(sent.map((one) => one.label).slice(2)).toEqual(["c", "d"])
  })
})

describe("a saved chat read back", () => {
  const setPort = {
    id: "c",
    tool: "api_request",
    route: "PUT /deploy/7/environments/12/configuration",
    label: "Save the configuration",
    kind: "change",
  }
  const at = "2026-11-02T10:00:00Z"
  test("draws the questions and answers as they were", () => {
    const t = replayLines([
      { type: "question", text: "why", at },
      {
        type: "start",
        conversation: "c1",
        provider: "anthropic",
        model: "m",
        modelLabel: "Claude",
        tools: true,
        mode: "changes",
        title: "why",
        at,
      },
      { type: "thinking", at },
      { type: "text", text: "Because.", at },
      { type: "done", stop: "end", usage, model: "m", kept: true, at },
    ])
    expect(t.turns).toHaveLength(1)
    expect(t.turns[0].question).toBe("why")
    expect(t.turns[0].phase).toBe("done")
    expect(t.turns[0].replayed).toBe(true)
    expect(t.turns[0].parts).toEqual([{ kind: "text", text: "Because." }])
    expect(t.conversation).toBe("c1")
    expect(t.title).toBe("why")
    expect(t.modelLabel).toBe("Claude")
  })
  test("a question's attachments come back as their labels", () => {
    const t = replayLines([
      { type: "question", text: "explain", attached: ["plan", 7, "", "statement"], at },
      { type: "text", text: "It scans." },
      { type: "done", stop: "end", usage, model: "m", kept: true },
    ])
    expect(t.turns[0].attached).toEqual(["plan", "statement"])
    expect(replayLines([{ type: "question", text: "hi" }]).turns[0]).not.toHaveProperty("attached")
  })

  test("a command it ran on the host reads back with what it printed", () => {
    const run = {
      id: "r1",
      tool: "run_command",
      label: "Run df -h",
      kind: "read",
      input: { command: "df -h" },
    }
    const t = replayLines([
      { type: "question", text: "disk?" },
      { type: "call", call: run },
      { type: "called", id: "r1", tool: "run_command", ok: false, said: "exit 2", output: "no" },
      { type: "done", stop: "end", usage, model: "m", kept: true },
    ])
    const [step] = shownSteps(t.turns[0])
    expect(step.state).toBe("failed")
    expect(step.said).toBe("exit 2")
    expect(step.output).toBe("no")
    expect(inputLines(step.call.input).lines).toEqual([{ path: "command", value: "df -h" }])
  })

  test("an Allow pressed among the lines runs the call it answered", () => {
    const t = replayLines([
      { type: "question", text: "fix it" },
      {
        type: "start",
        conversation: "c1",
        provider: "a",
        model: "m",
        modelLabel: "Claude",
        tools: true,
        mode: "changes",
      },
      { type: "done", stop: "tool", usage, model: "m", kept: true, pending: [setPort, restart] },
      { type: "decisions", decisions: { c: true } },
      { type: "call", call: setPort },
      {
        type: "called",
        id: "c",
        tool: "api_request",
        ok: true,
        said: "Done",
        change: { label: "Runtime", href: "/deploy/7/settings/runtime" },
      },
      { type: "called", id: "b", tool: "call_operation", ok: false, declined: true },
      { type: "text", text: "Saved." },
      { type: "done", stop: "end", usage, model: "m", kept: true },
    ])
    const steps = t.turns[0].parts.filter((part) => part.kind === "step")
    expect(steps.map((step) => [step.call.id, step.state])).toEqual([
      ["c", "done"],
      ["b", "declined"],
    ])
    expect(t.turns[0].phase).toBe("done")
    expect(changesOf(t).map((step) => step.change.label)).toEqual(["Runtime"])
    expect(totalUsage(t).input).toBe(200)
  })
  test("a chat left waiting on the person is asking again", () => {
    const t = replayLines([
      { type: "question", text: "restart it" },
      { type: "done", stop: "tool", usage, model: "m", kept: true, pending: [restart] },
    ])
    expect(t.turns[0].phase).toBe("asking")
    expect(pendingSteps(t).map((step) => step.call.id)).toEqual(["b"])
  })
  test("a second question declines what the first left waiting", () => {
    const t = replayLines([
      { type: "question", text: "restart it" },
      { type: "done", stop: "tool", usage, model: "m", kept: true, pending: [restart] },
      { type: "question", text: "no, leave it" },
      { type: "text", text: "Left." },
      { type: "done", stop: "end", usage, model: "m", kept: true },
    ])
    expect(t.turns.map((turn) => turn.phase)).toEqual(["done", "done"])
    expect(t.turns[0].parts[0].state).toBe("declined")
  })
  test("a press another tab is still answering stays open, one that died reads stopped", () => {
    const lines = [
      { type: "question", text: "why" },
      { type: "call", call: read },
    ]
    const live = replayLines(lines, { running: true })
    expect(answering(live.turns[0])).toBe(true)
    expect(live.turns[0].parts[0].state).toBe("running")
    const dead = replayLines(lines)
    expect(dead.turns[0].phase).toBe("stopped")
    expect(dead.turns[0].parts[0].state).toBe("failed")
  })
  test("what it cannot read, or what came before any question, is left out", () => {
    const t = replayLines([null, 3, "x", { type: "text", text: "orphan" }, { nope: true }])
    expect(t.turns).toEqual([])
  })
})

describe("a chat in two tabs", () => {
  test("a tab whose press was taken over stops cleanly", () => {
    let t = applyEvent(started(), "t1", { type: "call", call: read })
    t = applyEvent(t, "t1", {
      type: "error",
      error: { code: "taken_over", message: "This chat was continued somewhere else." },
    })
    expect(t.turns[0].phase).toBe("moved")
    expect(t.turns[0].parts[0].state).toBe("failed")
    expect(answering(t.turns[0])).toBe(false)
    expect(agentStatus(t)).toEqual({ tone: "idle", text: "Continued somewhere else" })
  })
  test("a question refused because another tab has the chat waits, unsent", () => {
    const t = holdTurn(beginTurn(EMPTY_AGENT, "t1", "why"), "t1")
    expect(t.turns[0].phase).toBe("held")
    expect(answering(t.turns[0])).toBe(false)
    expect(agentStatus(t)).toEqual({ tone: "asking", text: "Running in another tab" })
    // Asking something else instead replaces it.
    const next = beginTurn(t, "t2", "something else")
    expect(next.turns.map((turn) => turn.question)).toEqual(["something else"])
  })
  test("an Allow refused the same way keeps its answers for the take over", () => {
    const sent = sendDecisions(decideAll(asking(), true))
    const t = holdTurn(sent, "t1")
    expect(t.turns[0].phase).toBe("asking")
    expect(pendingSteps(t).every((step) => step.decision === true)).toBe(true)
  })
  test("taking over sends the held press and stops what the other tab was answering", () => {
    let t = replayLines([{ type: "question", text: "why" }, { type: "thinking" }], {
      running: true,
    })
    t = holdTurn(beginTurn(t, "t2", "carry on"), "t2")
    t = takeOverTurn(t, "t2")
    expect(t.turns.map((turn) => turn.phase)).toEqual(["stopped", "waiting"])
  })
})
