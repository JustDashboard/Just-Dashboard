import { describe, expect, test } from "bun:test"
import { fleetVerdict, readers } from "./band"

describe("the identity line's verdict", () => {
  test("not answering outranks stopped, which outranks all clear", () => {
    expect(fleetVerdict({ total: 4, failing: 1, stopped: 1 })).toEqual({
      tone: "danger",
      label: "1 of 4 not answering",
    })
    expect(fleetVerdict({ total: 4, failing: 0, stopped: 2 })).toEqual({
      tone: "warning",
      label: "2 of 4 stopped",
    })
    expect(fleetVerdict({ total: 4, failing: 0, stopped: 0 })).toEqual({
      tone: "running",
      label: "All 4 answering",
    })
  })

  test("one database is not all of one, and none is no verdict", () => {
    expect(fleetVerdict({ total: 1, failing: 0, stopped: 0 })?.label).toBe("Answering")
    expect(fleetVerdict({ total: 1, failing: 1, stopped: 0 })?.label).toBe("Not answering")
    expect(fleetVerdict({ total: 1, failing: 0, stopped: 1 })?.label).toBe("Stopped")
    expect(fleetVerdict({ total: 0, failing: 0, stopped: 0 })).toBeUndefined()
  })
})

describe("what the databases feed", () => {
  test("a reader two databases reach is one reader", () => {
    const topology = {
      nodes: [
        { id: "db:1", kind: "database" },
        { id: "db:2", kind: "database" },
        { id: "deploy:7", kind: "deployment" },
        { id: "host", kind: "host" },
      ],
      edges: [
        { from: "db:1", to: "deploy:7" },
        { from: "db:2", to: "deploy:7" },
        { from: "db:2", to: "host" },
      ],
    }
    expect(readers(topology).count).toBe(2)
    expect(readers(undefined).count).toBe(0)
  })

  test("they are apps while each is a deployment or a container, clients once a machine reads", () => {
    const apps = {
      nodes: [
        { id: "db:1", kind: "database" },
        { id: "deploy:7", kind: "deployment" },
        { id: "ctr:web", kind: "container" },
      ],
      edges: [
        { from: "db:1", to: "deploy:7" },
        { from: "db:1", to: "ctr:web" },
      ],
    }
    expect(readers(apps)).toEqual({ count: 2, nouns: { one: "app", many: "apps" } })
    const machine = {
      nodes: [...apps.nodes, { id: "remote:10.0.0.4", kind: "remote" }],
      edges: [...apps.edges, { from: "db:1", to: "remote:10.0.0.4" }],
    }
    expect(readers(machine)).toEqual({ count: 3, nouns: { one: "client", many: "clients" } })
  })
})
