import { describe, expect, test } from "bun:test"
import {
  agentChatHref,
  agentPageOf,
  agentPagesOf,
  agentPasswordTyped,
  agentQueueHeld,
  agentQueues,
  agentScopeKey,
  askAgent,
  decideAgent,
  holdAgentTurns,
  editQueued,
  newAgentChat,
  removeQueued,
  stopAgent,
  takeOverAgent,
  takeReturned,
} from "./sessions"

const database = { kind: "database", databaseId: 3, name: "shop" }
const deployment = {
  kind: "deployment",
  projectId: 7,
  environmentId: 12,
  name: "api",
  environmentName: "production",
}

describe("what a chat is about, and where its page is", () => {
  test("a connection is a subject of its own, with its page beside its Home", () => {
    expect(agentScopeKey(database)).toBe("database.3")
    expect(agentPageOf(database)).toBe("/databases/3/assistant")
    // Its SQL editor draws the same chat, so the dock stays off it too.
    expect(agentPagesOf(database)).toEqual(["/databases/3/assistant", "/databases/3/query"])
    expect(agentScopeKey(deployment)).toBe("deploy.12")
    expect(agentPagesOf(deployment)).toEqual(["/deploy/7/assistant"])
    expect(agentScopeKey({ kind: "server", name: "This server" })).toBe("server")
  })

  test("a saved chat opens on its subject's page", () => {
    const chat = (scope) => ({ id: "a b", scope })
    expect(agentChatHref(chat({ kind: "database", database: 3, name: "shop" }))).toBe(
      "/databases/3/assistant?chat=a%20b",
    )
    expect(agentChatHref(chat({ kind: "deployment", deployment: 7, name: "api" }))).toBe(
      "/deploy/7/assistant?chat=a%20b",
    )
    expect(
      agentChatHref(
        chat({ kind: "server", name: "This server", about: { path: "/docker", title: "Docker" } }),
      ),
    ).toBe("/assistant?chat=a%20b")
  })
})

/* ------------------------------------------------- questions sent while it answers */

const start = { type: "start", conversation: "c1", modelLabel: "Model", tools: true }
const done = (extra = {}) => ({
  type: "done",
  stop: "end",
  usage: { input: 1, output: 1 },
  kept: true,
  ...extra,
})
const change = { id: "x", tool: "call_operation", label: "Restart", kind: "change", input: {} }

/**
 * The turn route, answered by the test a line at a time: each press is a call whose
 * stream stays open until the test ends it, or a refusal queued in `refusals`.
 */
function turnServer() {
  const calls = []
  const refusals = []
  globalThis.fetch = async (_url, init) => {
    const body = JSON.parse(init.body)
    const refusal = refusals.shift()
    if (refusal) {
      calls.push({ body })
      return new Response(
        JSON.stringify({ error: { code: refusal, message: "Busy elsewhere." } }),
        {
          status: 409,
          headers: { "Content-Type": "application/json" },
        },
      )
    }
    let controller
    const stream = new ReadableStream({ start: (c) => (controller = c) })
    init.signal?.addEventListener("abort", () =>
      controller.error(new DOMException("", "AbortError")),
    )
    calls.push({
      body,
      answer: (events, end = true) => {
        controller.enqueue(
          new TextEncoder().encode(events.map((e) => JSON.stringify(e) + "\n").join("")),
        )
        if (end) controller.close()
      },
    })
    return new Response(stream, {
      status: 200,
      headers: { "Content-Type": "application/x-ndjson" },
    })
  }
  return { calls, refusals }
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 10))
let subject = 100
const freshChat = () => newAgentChat({ kind: "database", databaseId: subject++, name: "shop" })
const image = {
  id: "i1",
  src: "data:image/png;base64,AA",
  data: "AA",
  mediaType: "image/png",
  thumb: "AA",
  width: 2,
  height: 2,
}

describe("a question sent while it answers waits its turn", () => {
  test("it waits, goes once the answer ends, and the next after that, in order", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    expect(agentQueues(session)).toBe(true)
    askAgent(session, "second", [image], [{ label: "statement", text: "SELECT 1" }], ["sql"])
    askAgent(session, "third")
    expect(calls).toHaveLength(1)
    expect(session.queue.map((one) => one.question)).toEqual(["second", "third"])
    calls[0].answer([start, { type: "text", text: "One." }, done()])
    await settle()
    // The images and what was attached go with it, as they would have then.
    expect(calls).toHaveLength(2)
    expect(calls[1].body).toMatchObject({
      conversation: "c1",
      question: "second",
      images: [{ mediaType: "image/png", data: "AA", thumb: "AA" }],
      attached: [{ label: "statement", text: "SELECT 1" }],
    })
    expect(session.transcript.turns.at(-1).images).toHaveLength(1)
    expect(session.queue.map((one) => one.question)).toEqual(["third"])
    calls[1].answer([start, done()])
    await settle()
    expect(calls.map((call) => call.body.question)).toEqual(["first", "second", "third"])
    expect(session.queue).toEqual([])
    calls[2].answer([start, done()])
    await settle()
    expect(agentQueues(session)).toBe(false)
  })

  test("Stop sends nothing more: what waits goes back to the box", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    askAgent(session, "second", [], [], ["sql"])
    askAgent(session, "third")
    stopAgent(session)
    await settle()
    expect(calls).toHaveLength(1)
    expect(session.queue).toEqual([])
    expect(takeReturned(session).map((one) => [one.question, one.pressed])).toEqual([
      ["second", ["sql"]],
      ["third", []],
    ])
    expect(session.returned).toEqual([])
    // Asked again now, it goes at once.
    askAgent(session, "again")
    expect(calls).toHaveLength(2)
  })

  test("an answer that fails, or is carried on in another tab, hands what waits back too", async () => {
    for (const code of ["provider_error", "taken_over"]) {
      const { calls } = turnServer()
      const session = freshChat()
      askAgent(session, "first")
      await settle()
      askAgent(session, "second")
      calls[0].answer([start, { type: "error", error: { code, message: "No." } }])
      await settle()
      expect(calls).toHaveLength(1)
      expect(session.returned.map((one) => one.question)).toEqual(["second"])
    }
  })

  test("an answer waiting on Allow keeps it waiting, and the answer after the Allow sends it", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    askAgent(session, "second")
    calls[0].answer([start, done({ stop: "tool", pending: [change] })])
    await settle()
    expect(calls).toHaveLength(1)
    expect(agentQueueHeld(session)).toBe("allow")
    // Sent while it waits on the person, a question joins the line rather than
    // declining what waits.
    askAgent(session, "third")
    expect(calls).toHaveLength(1)
    decideAgent(session, "x", true)
    await settle()
    expect(calls[1].body.decisions).toEqual({ x: true })
    calls[1].answer([start, done()])
    await settle()
    expect(calls.map((call) => call.body.question)).toEqual(["first", undefined, "second"])
    expect(session.queue.map((one) => one.question)).toEqual(["third"])
  })

  test("Stop, or a failure, on the answer after an Allow hands what waits back too", async () => {
    for (const end of ["stop", "fail"]) {
      const { calls } = turnServer()
      const session = freshChat()
      askAgent(session, "first")
      await settle()
      askAgent(session, "second")
      calls[0].answer([start, done({ stop: "tool", pending: [change] })])
      await settle()
      decideAgent(session, "x", true)
      await settle()
      if (end === "stop") stopAgent(session)
      else
        calls[1].answer([
          start,
          { type: "error", error: { code: "provider_error", message: "No." } },
        ])
      await settle()
      // The calls it had not run are asked for again; nothing waits behind them.
      expect(session.transcript.turns.at(-1).phase).toBe("asking")
      expect(session.queue).toEqual([])
      expect(session.returned.map((one) => one.question)).toEqual(["second"])
      decideAgent(session, "x", false)
      await settle()
      calls[2].answer([start, done()])
      await settle()
      expect(calls).toHaveLength(3)
    }
  })

  test("with nothing waiting, a question sent while it waits on Allow still declines it", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    calls[0].answer([start, done({ stop: "tool", pending: [change] })])
    await settle()
    askAgent(session, "something else")
    expect(calls).toHaveLength(2)
    expect(session.queue).toEqual([])
  })

  test("a password the terminal asks for holds it, and the password's answer goes first", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "update")
    await settle()
    askAgent(session, "then reboot")
    calls[0].answer([
      start,
      { type: "call", call: { ...change, id: "t", tool: "terminal_run" } },
      {
        type: "called",
        id: "t",
        tool: "terminal_run",
        ok: true,
        terminal: { session: "w", state: "password", prompt: "[sudo] password:" },
      },
      done(),
    ])
    await settle()
    expect(calls).toHaveLength(1)
    expect(agentQueueHeld(session)).toBe("password")
    agentPasswordTyped(session)
    await settle()
    expect(calls[1].body.question).toBe("I've typed the password.")
    calls[1].answer([start, done()])
    await settle()
    expect(calls[2].body.question).toBe("then reboot")
  })

  test("refused because another tab has the chat, the rest waits for Take over", async () => {
    const { calls, refusals } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    askAgent(session, "second")
    askAgent(session, "third")
    refusals.push("chat_busy")
    calls[0].answer([start, done()])
    await settle()
    expect(calls).toHaveLength(2)
    expect(session.held?.body.question).toBe("second")
    expect(session.queue.map((one) => one.question)).toEqual(["third"])
    takeOverAgent(session)
    await settle()
    expect(calls[2].body).toMatchObject({ question: "second", takeOver: true })
    calls[2].answer([start, done()])
    await settle()
    expect(calls[3].body.question).toBe("third")
  })

  test("a waiting question is edited back into the box, or removed, before it goes", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    askAgent(session, "second")
    askAgent(session, "third")
    editQueued(session, session.queue[0].id)
    expect(session.returned.map((one) => one.question)).toEqual(["second"])
    removeQueued(session, session.queue[0].id)
    expect(session.queue).toEqual([])
    calls[0].answer([start, done()])
    await settle()
    expect(calls).toHaveLength(1)
  })
})

describe("a page holds the chat's next turn while it writes what the chat works on", () => {
  test("a question sent while it holds waits, and goes once it lets go", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    holdAgentTurns(session, "draft-save", true)
    expect(agentQueueHeld(session)).toBe("page")
    askAgent(session, "first")
    expect(calls).toHaveLength(0)
    expect(agentQueues(session)).toBe(true)
    holdAgentTurns(session, "draft-save", false)
    await settle()
    expect(calls.map((call) => call.body.question)).toEqual(["first"])
  })

  test("held by the page while an answer waits on Allow, the Allow is what it names", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    calls[0].answer([start, done({ stop: "tool", pending: [change] })])
    await settle()
    // The person typed into the form meanwhile: the page holds for that save,
    // which it makes only once the Allow is answered.
    holdAgentTurns(session, "draft-edits", true)
    askAgent(session, "second")
    expect(calls).toHaveLength(1)
    expect(agentQueueHeld(session)).toBe("allow")
    decideAgent(session, "x", true)
    await settle()
    calls[1].answer([start, done()])
    await settle()
    expect(agentQueueHeld(session)).toBe("page")
    holdAgentTurns(session, "draft-edits", false)
    await settle()
    expect(calls[2].body.question).toBe("second")
  })

  test("a question waiting in the queue is not asked at the end of an answer while it holds", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "first")
    await settle()
    askAgent(session, "second")
    holdAgentTurns(session, "draft-edits", true)
    calls[0].answer([start, done()])
    await settle()
    expect(calls).toHaveLength(1)
    expect(agentQueueHeld(session)).toBe("page")
    // Every reason has to let go.
    holdAgentTurns(session, "draft-save", true)
    holdAgentTurns(session, "draft-edits", false)
    await settle()
    expect(calls).toHaveLength(1)
    holdAgentTurns(session, "draft-save", false)
    await settle()
    expect(calls.map((call) => call.body.question)).toEqual(["first", "second"])
  })
})
