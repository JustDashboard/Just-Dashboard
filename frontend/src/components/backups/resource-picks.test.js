import { describe, expect, test } from "bun:test"
import {
  backupInputs,
  editPickedInput,
  pickedInputs,
  startResourcePicks,
  toggleResourcePick,
} from "./resource-picks"

const suggestion = backupInputs({
  sources: ["/srv/app"],
  excludes: ["*.log"],
  sqlitePaths: ["/srv/app/app.db"],
  pauseContainers: ["app"],
  databaseDumps: [3],
})

describe("backup resource suggestions", () => {
  test("unpick preserves every matching setting entered manually", () => {
    const manual = startResourcePicks(suggestion)
    const chosen = toggleResourcePick(manual, "deployment:1", suggestion, true)
    expect(pickedInputs(chosen)).toEqual(suggestion)
    const removed = toggleResourcePick(chosen, "deployment:1", suggestion, false)
    expect(pickedInputs(removed)).toEqual(suggestion)
    expect(removed.picks).toEqual({})
  })

  test("overlapping picks retain shared sources and consistency until the last owner is removed", () => {
    let state = startResourcePicks(backupInputs({ sources: ["/manual"] }))
    state = toggleResourcePick(state, "deployment:1", suggestion, true)
    state = toggleResourcePick(state, "volume:2", suggestion, true)
    state = toggleResourcePick(state, "deployment:1", suggestion, false)
    expect(pickedInputs(state)).toEqual({ ...suggestion, sources: ["/manual", "/srv/app"] })
    state = toggleResourcePick(state, "volume:2", suggestion, false)
    expect(pickedInputs(state)).toEqual(backupInputs({ sources: ["/manual"] }))
  })

  test("typing new values while a resource is picked keeps them after unpick", () => {
    let state = startResourcePicks(backupInputs({}))
    state = toggleResourcePick(state, "deployment:1", suggestion, true)
    state = editPickedInput(state, "sources", ["/srv/app", "/srv/extra"])
    state = editPickedInput(state, "databaseDumps", ["3", "7"])
    state = toggleResourcePick(state, "deployment:1", suggestion, false)
    expect(pickedInputs(state)).toEqual(
      backupInputs({ sources: ["/srv/extra"], databaseDumps: [7] }),
    )
  })

  test("manually removing a picked setting does not reintroduce it on another pick", () => {
    let state = startResourcePicks(suggestion, "deployment:1")
    state = editPickedInput(state, "excludes", [])
    state = toggleResourcePick(state, "database:3", backupInputs({ databaseDumps: [3] }), true)
    expect(pickedInputs(state).excludes).toEqual([])
    state = toggleResourcePick(state, "deployment:1", suggestion, false)
    expect(pickedInputs(state)).toEqual(backupInputs({ databaseDumps: [3] }))
  })

  test("a resource-specific handoff starts with a removable pick", () => {
    const state = startResourcePicks(suggestion, "deployment:1")
    expect(pickedInputs(state)).toEqual(suggestion)
    expect(pickedInputs(toggleResourcePick(state, "deployment:1", suggestion, false))).toEqual(
      backupInputs({}),
    )
  })
})
