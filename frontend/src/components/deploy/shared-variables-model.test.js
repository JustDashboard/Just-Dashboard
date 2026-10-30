import { describe, expect, test } from "bun:test"
import {
  parseSharedReference,
  readersByProject,
  removalNote,
  SHARED_NAME,
  sharedBase,
  sharedReference,
  waitingReaders,
} from "./shared-variables-model"

const use = (overrides) => ({
  projectId: 1,
  projectName: "shop",
  environmentId: 10,
  environmentName: "production",
  environmentKind: "production",
  variable: "STRIPE_KEY",
  waiting: false,
  ...overrides,
})

describe("shared references", () => {
  test("are written and read in the server's own spelling", () => {
    expect(sharedReference("server", "API_URL")).toBe("${{server.API_URL}}")
    expect(sharedReference("project", "REGION")).toBe("${{project.REGION}}")
    expect(parseSharedReference(" ${{server.API_URL}} ")).toEqual({
      kind: "server",
      name: "API_URL",
    })
    expect(parseSharedReference("${{project.REGION}}")).toEqual({ kind: "project", name: "REGION" })
  })

  test("are only the two shared kinds, with a name the server accepts", () => {
    for (const value of [
      "${{database.11}}",
      "${{server._HIDDEN}}",
      "${{server.A.B}}",
      "${{shared.API_URL}}",
      "server.API_URL",
    ]) {
      expect(parseSharedReference(value)).toBeUndefined()
    }
    expect(SHARED_NAME.test("API_URL")).toBe(true)
    expect(SHARED_NAME.test("_API_URL")).toBe(false)
    expect(SHARED_NAME.test("1API")).toBe(false)
  })

  test("live at the scope's address", () => {
    expect(sharedBase({ kind: "server" })).toBe("/deploy/shared-variables")
    expect(sharedBase({ kind: "project", projectId: 4 })).toBe("/deploy/4/shared-variables")
  })
})

describe("readers", () => {
  test("are grouped a project at a time, in the order the server sent them", () => {
    const groups = readersByProject([
      use({}),
      use({ projectId: 2, projectName: "blog", variable: "STRIPE" }),
      use({ environmentId: 11, environmentName: "pr-12", environmentKind: "preview" }),
    ])
    expect(groups.map((group) => group.projectName)).toEqual(["shop", "blog"])
    expect(groups[0].uses.map((reader) => reader.environmentName)).toEqual(["production", "pr-12"])
  })

  test("count the ones still running an earlier value", () => {
    expect(
      waitingReaders({ usedBy: [use({ waiting: true }), use({}), use({ waiting: true })] }),
    ).toBe(2)
  })

  test("decide what removing says before the server is asked", () => {
    expect(removalNote({ usedBy: [], reference: "${{server.A}}" })).toBe(
      "Nothing reads ${{server.A}}; removing it changes no release.",
    )
    expect(removalNote({ usedBy: [use({})], reference: "${{server.A}}" })).toContain(
      "One variable reads ${{server.A}}. The server refuses to remove it until that variable points elsewhere.",
    )
    expect(removalNote({ usedBy: [use({}), use({})], reference: "${{server.A}}" })).toContain(
      "2 variables read",
    )
  })
})
