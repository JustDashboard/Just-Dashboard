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
  askAgentFirst,
  carryAgentQueue,
  decideAgent,
  holdAgentTurns,
  editQueued,
  newAgentChat,
  openAgentChat,
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

describe("what the page's hold keeps back besides a question", () => {
  test("the page's own first question goes ahead of one the person sent while it held", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    holdAgentTurns(session, "draft-prepare", true)
    askAgent(session, "the person's")
    askAgentFirst(session, "configure it")
    expect(calls).toHaveLength(0)
    holdAgentTurns(session, "draft-prepare", false)
    await settle()
    expect(calls.map((call) => call.body.question)).toEqual(["configure it"])
    calls[0].answer([start, done()])
    await settle()
    expect(calls.map((call) => call.body.question)).toEqual(["configure it", "the person's"])
  })

  test("a subject that became another carries what waits into the new one's box, unsent", async () => {
    const { calls } = turnServer()
    const draft = newAgentChat({ kind: "draft", draftId: "d-carry", name: "shop" })
    holdAgentTurns(draft, "draft-save", true)
    askAgent(draft, "and add a volume", [image], [], [])
    expect(draft.queue).toHaveLength(1)
    const deployment = {
      kind: "deployment",
      projectId: 70,
      environmentId: 71,
      name: "shop",
      environmentName: "production",
    }
    carryAgentQueue(draft, deployment)
    holdAgentTurns(draft, "draft-save", false)
    await settle()
    expect(calls).toHaveLength(0)
    expect(draft.queue).toEqual([])
    const front = newAgentChat(deployment)
    expect(front.returned.map((one) => [one.question, one.images.length])).toEqual([
      ["and add a volume", 1],
    ])
  })

  test("a Take over the page held is not made of a draft deployed meanwhile", async () => {
    const { calls, refusals } = turnServer()
    const draft = newAgentChat({ kind: "draft", draftId: "d-owed", name: "shop" })
    refusals.push("chat_busy")
    askAgent(draft, "first")
    await settle()
    expect(draft.held?.body.question).toBe("first")
    holdAgentTurns(draft, "draft-save", true)
    takeOverAgent(draft)
    expect(draft.owed).toBe("takeOver")
    carryAgentQueue(draft, {
      kind: "deployment",
      projectId: 80,
      environmentId: 81,
      name: "shop",
      environmentName: "production",
    })
    holdAgentTurns(draft, "draft-save", false)
    await settle()
    // Deploy let go of the hold after the commit: nothing goes to the deployed draft.
    expect(calls).toHaveLength(1)
  })

  test("Take over pressed while the page holds goes once it lets go", async () => {
    const { calls, refusals } = turnServer()
    const session = freshChat()
    refusals.push("chat_busy")
    askAgent(session, "first")
    await settle()
    expect(session.held?.body.question).toBe("first")
    holdAgentTurns(session, "draft-save", true)
    takeOverAgent(session)
    expect(calls).toHaveLength(1)
    expect(session.owed).toBe("takeOver")
    holdAgentTurns(session, "draft-save", false)
    await settle()
    expect(calls[1].body).toMatchObject({ question: "first", takeOver: true })
  })

  test("a password typed while the page holds carries on once it lets go, ahead of what waits", async () => {
    const { calls } = turnServer()
    const session = freshChat()
    askAgent(session, "update")
    await settle()
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
    holdAgentTurns(session, "draft-save", true)
    askAgent(session, "then reboot")
    agentPasswordTyped(session)
    expect(calls).toHaveLength(1)
    holdAgentTurns(session, "draft-save", false)
    await settle()
    expect(calls[1].body.question).toBe("I've typed the password.")
    calls[1].answer([start, done()])
    await settle()
    expect(calls[2].body.question).toBe("then reboot")
  })

  test("a Take over the page held goes nowhere once the other tab has finished", async () => {
    const { calls } = turnServer()
    const turns = globalThis.fetch
    let running = true
    globalThis.fetch = async (url, init) => {
      if (init?.method === "POST") return turns(url, init)
      const lines = [{ type: "question", text: "check it" }, start]
      return Response.json({
        chat: { id: "c1", title: "", running, scope: { kind: "database" } },
        lines: running ? lines : [...lines, done()],
      })
    }
    const session = freshChat()
    askAgent(session, "check it")
    await settle()
    calls[0].answer([start, done()])
    await settle()
    // Read again while another tab answers it: it is followed until that ends.
    const hadWindow = "window" in globalThis
    if (!hadWindow) globalThis.window = globalThis
    await openAgentChat(session.scope, "c1")
    expect(session.elsewhere).toBe(true)
    holdAgentTurns(session, "draft-save", true)
    takeOverAgent(session)
    expect(session.owed).toBe("takeOver")
    running = false
    await new Promise((resolve) => setTimeout(resolve, 2700))
    expect(session.elsewhere).toBe(false)
    if (!hadWindow) delete globalThis.window
    holdAgentTurns(session, "draft-save", false)
    await settle()
    // Nothing runs elsewhere any more: taking it over would answer the last turn again.
    expect(calls).toHaveLength(1)
    expect(session.owed).toBeUndefined()
  }, 8000)
})

describe("a draft deployed with something waiting", () => {
  const deployment = (environmentId) => ({
    kind: "deployment",
    projectId: 90,
    environmentId,
    name: "shop",
    environmentName: "production",
  })

  test("a question held for Take over is carried first, with its images, and its turn goes", async () => {
    const { calls, refusals } = turnServer()
    const draft = newAgentChat({ kind: "draft", draftId: "d-held", name: "shop" })
    refusals.push("chat_busy")
    askAgent(draft, "first", [image], [{ label: "statement", text: "SELECT 1" }], ["sql"])
    await settle()
    expect(draft.held?.body.question).toBe("first")
    holdAgentTurns(draft, "draft-save", true)
    askAgent(draft, "second")
    carryAgentQueue(draft, deployment(93))
    holdAgentTurns(draft, "draft-save", false)
    takeOverAgent(draft)
    await settle()
    // Nothing more goes to the deployed draft, and its "not sent" turn is gone.
    expect(calls).toHaveLength(1)
    expect(draft.held).toBeUndefined()
    expect(draft.transcript.turns).toEqual([])
    const carried = newAgentChat(deployment(93)).returned
    expect(carried.map((one) => one.question)).toEqual(["first", "second"])
    expect(carried[0].images).toEqual([image])
    expect(carried[0].attached).toEqual([{ label: "statement", text: "SELECT 1" }])
    expect(carried[0].pressed).toEqual(["sql"])
  })
})
