import { describe, expect, test } from "bun:test"
import { agentChatHref, agentPageOf, agentPagesOf, agentScopeKey } from "./sessions"

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
