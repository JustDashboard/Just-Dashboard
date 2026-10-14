import { expect, test } from "bun:test"
import { drainBoardSaves } from "./board-save"

test("a navigation flush waits for the edits made while its first request is pending", async () => {
  let current = 1
  let saved = 0
  const snapshots = []
  const clean = await drainBoardSaves(
    () => current !== saved,
    async () => {
      const snapshot = current
      snapshots.push(snapshot)
      if (snapshot === 1) current = 2
      await Promise.resolve()
      saved = snapshot
      return true
    },
  )
  expect(clean).toBe(true)
  expect(snapshots).toEqual([1, 2])
  expect(saved).toBe(current)
})

test("a later save failure leaves the new edit dirty and refuses navigation", async () => {
  let current = 1
  let saved = 0
  const clean = await drainBoardSaves(
    () => current !== saved,
    async () => {
      if (current === 2) return false
      saved = current
      current = 2
      return true
    },
  )
  expect(clean).toBe(false)
  expect(current).not.toBe(saved)
})
