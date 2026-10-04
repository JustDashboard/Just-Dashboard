import { describe, expect, test } from "bun:test"
import { volumeBackup } from "./volume-backup"

const covered = {
  protected: true,
  lastBackupAt: "2026-10-01T03:00:00Z",
  coveredBy: [{ jobId: 7, enabled: true }],
}
const policy = (fields) => ({
  resourceId: "7",
  required: true,
  status: "present",
  fresh: true,
  ...fields,
})

describe("volumeBackup", () => {
  test("says nothing for a volume no job copies, or before the coverage has loaded", () => {
    expect(volumeBackup(undefined, [])).toBeUndefined()
    expect(volumeBackup({ ...covered, protected: false }, [])).toBeUndefined()
  })

  test("without a declared policy it reads the last run's time", () => {
    expect(volumeBackup(covered, undefined)).toEqual({
      tone: "running",
      word: "Backed up",
      at: covered.lastBackupAt,
    })
    expect(volumeBackup({ ...covered, lastBackupAt: undefined }, [])).toEqual({
      tone: "notice",
      word: "Protected · not run yet",
    })
  })

  test("a declared policy that covers the volume says when its last run failed or went stale", () => {
    expect(volumeBackup(covered, [policy({ lastStatus: "failed" })])?.tone).toBe("danger")
    expect(volumeBackup(covered, [policy({ lastStatus: "success", fresh: false })])?.tone).toBe(
      "warning",
    )
    expect(volumeBackup(covered, [policy({ lastStatus: "success" })])?.tone).toBe("running")
    expect(volumeBackup(covered, [policy({ lastStatus: "never run" })])?.word).toBe(
      "Never backed up",
    )
  })

  test("a policy for some other job, or a disabled one, does not speak for the volume", () => {
    expect(volumeBackup(covered, [policy({ resourceId: "9", lastStatus: "failed" })])?.tone).toBe(
      "running",
    )
    const paused = { ...covered, coveredBy: [{ jobId: 7, enabled: false }] }
    expect(volumeBackup(paused, [policy({ lastStatus: "failed" })])?.tone).toBe("running")
  })
})
