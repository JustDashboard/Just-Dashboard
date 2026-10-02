import { describe, expect, test } from "bun:test"
import { clientWork, isHeartbeat, leftOutWords, operationTitle } from "./ops"

const operation = (over) => ({
  opId: "1",
  op: "command",
  ns: "admin.$cmd",
  command: "",
  commandTruncated: false,
  secondsRunning: 0,
  client: "10.0.0.1:1",
  appName: "",
  user: "",
  desc: "conn1",
  active: true,
  waitingForLock: false,
  planSummary: "",
  message: "",
  killPending: false,
  connectionId: 1,
  numYields: 0,
  ...over,
})

describe("a driver's heartbeat", () => {
  test("the hello a client keeps open is one, in either of its names", () => {
    expect(
      isHeartbeat(operation({ command: '{"hello":1,"helloOk":true,"maxAwaitTimeMS":10000}' })),
    ).toBe(true)
    expect(isHeartbeat(operation({ command: '{"isMaster":1,"helloOk":true}' }))).toBe(true)
  })

  test("a command that merely mentions hello, or another kind of operation, is not", () => {
    expect(isHeartbeat(operation({ command: '{"find":"hello","filter":{"hello":1}}' }))).toBe(false)
    expect(isHeartbeat(operation({ op: "query", command: '{"hello":1}' }))).toBe(false)
    expect(isHeartbeat(operation({ command: '{"aggregate":"orders"}' }))).toBe(false)
  })

  test("they, and the server's own tasks, are left out of the list and counted", () => {
    const list = [
      operation({ opId: "1", command: '{"hello":1}' }),
      operation({ opId: "2", op: "query", ns: "app.orders", command: '{"find":"orders"}' }),
      operation({ opId: "3", command: '{"hello":1}' }),
      operation({ opId: "4", op: "none", ns: "", client: "", desc: "JournalFlusher" }),
    ]
    const { shown, heartbeats, internal } = clientWork(list)
    expect(shown.map((entry) => entry.opId)).toEqual(["2"])
    expect(heartbeats).toBe(2)
    expect(internal).toBe(1)
  })

  test("what was left out, in words", () => {
    expect(leftOutWords(2, 1)).toBe(
      "the heartbeats of 2 connected clients and one task of the server's own",
    )
    expect(leftOutWords(1, 0)).toBe("the heartbeat of one connected client")
    expect(leftOutWords(0, 3)).toBe("3 tasks of the server's own")
    expect(leftOutWords(0, 0)).toBe("")
  })
})

test("an operation in one line", () => {
  expect(operationTitle(operation({ op: "query", ns: "app.orders" }))).toBe("query app.orders")
  expect(operationTitle(operation({ op: "none", ns: "", desc: "JournalFlusher" }))).toBe(
    "JournalFlusher",
  )
  expect(operationTitle(operation({ op: "", ns: "", desc: "" }))).toBe("idle")
})
