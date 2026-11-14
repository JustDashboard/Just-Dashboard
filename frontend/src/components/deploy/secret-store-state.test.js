import { describe, expect, test } from "bun:test"
import {
  storeLatest,
  storeLine,
  storeReaders,
  storeState,
  storeStateWord,
  storeUsesByProject,
  storesCount,
} from "./secret-store-state"

const now = Date.parse("2026-11-14T12:00:00Z")
const ago = (seconds) => new Date(now - seconds * 1000).toISOString()
const reads = (projectId, projectName, variable, environmentId = 1) => ({
  projectId,
  projectName,
  environmentId,
  environmentName: "production",
  environmentKind: "production",
  variable,
  reference: `bao/apps/web#${variable}`,
})

const answering = { id: 1, test: { at: ago(60), ok: true, message: "Signed in" }, usedBy: [] }
const refused = {
  id: 2,
  test: { at: ago(1800), ok: true, message: "Signed in" },
  lastRead: { at: ago(130), ok: false, message: "sign-in refused (400)" },
  usedBy: [
    reads(7, "api", "DATABASE_URL"),
    reads(7, "api", "REDIS_URL", 2),
    reads(8, "shop", "KEY"),
  ],
}
const untested = { id: 3, usedBy: [reads(8, "shop", "TOKEN")] }

describe("storeLatest", () => {
  test("the newer of a run's read and a Test", () => {
    expect(storeLatest(refused)).toBe(refused.lastRead)
    expect(storeLatest(answering)).toBe(answering.test)
    expect(storeLatest(untested)).toBeUndefined()
  })
})

describe("storeStateWord", () => {
  test("a dot and a word for each state", () => {
    expect(storeStateWord(answering, now)).toEqual({ tone: "running", label: "Answering" })
    expect(storeStateWord(refused, now)).toEqual({ tone: "warning", label: "Refused 2m ago" })
    expect(storeStateWord(untested, now)).toEqual({ tone: "unknown", label: "Not tested" })
  })
  test("a test that passed after a read failed is answering again", () => {
    const recovered = { ...refused, test: { at: ago(10), ok: true, message: "Signed in" } }
    expect(storeState(recovered)).toBe("answering")
  })
})

describe("storeLine", () => {
  test("a pulse only while it last answered", () => {
    expect(storeLine(answering)).toMatchObject({ state: "up", moving: true })
    expect(storeLine(refused)).toEqual({ state: "warning" })
    expect(storeLine(untested)).toEqual({ state: "up" })
  })
})

describe("storesCount", () => {
  test("the count, then what needs a look", () => {
    expect(storesCount([answering])).toBe("1 store")
    expect(storesCount([answering, refused, untested])).toBe("3 stores · 1 refused")
  })
})

describe("storeReaders", () => {
  test("each project once, with its variables and whether a store it reads refuses", () => {
    expect(storeReaders([answering, refused, untested])).toEqual([
      { projectId: 7, projectName: "api", variables: 2, stores: [2], refused: true },
      { projectId: 8, projectName: "shop", variables: 2, stores: [2, 3], refused: true },
    ])
    expect(storeReaders([answering, untested])).toEqual([
      { projectId: 8, projectName: "shop", variables: 1, stores: [3], refused: false },
    ])
  })
})

describe("storeUsesByProject", () => {
  test("says a project's name once", () => {
    const grouped = storeUsesByProject(refused.usedBy)
    expect(grouped.map((project) => [project.projectName, project.uses.length])).toEqual([
      ["api", 2],
      ["shop", 1],
    ])
  })
})
