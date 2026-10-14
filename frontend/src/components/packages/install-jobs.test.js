import { expect, test } from "bun:test"
import { reconcileInstall } from "./install-jobs"

test("a failed install can be retried without a late response restoring the previous failure", () => {
  const running = { htop: { id: "first", status: "running" } }
  const failed = reconcileInstall(running, "htop", { id: "first", status: "failed" })
  expect(failed.htop.status).toBe("failed")
  const retry = { ...failed, htop: { id: "retry", status: "running" } }
  expect(reconcileInstall(retry, "htop", { id: "first", status: "failed" })).toBe(retry)
  expect(reconcileInstall(retry, "htop", { id: "retry", status: "succeeded" }).htop.status).toBe(
    "succeeded",
  )
})

test("one package's terminal outcome preserves other active installs", () => {
  const running = {
    htop: { id: "htop", status: "running" },
    git: { id: "git", status: "running" },
  }
  const cancelled = reconcileInstall(running, "htop", { id: "htop", status: "cancelled" })
  expect(cancelled.htop.status).toBe("cancelled")
  expect(cancelled.git).toBe(running.git)
  expect(reconcileInstall(cancelled, "htop", cancelled.htop)).toBe(cancelled)
})
