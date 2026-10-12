import { expect, test } from "bun:test"
import { footprintLine, stackGone, uninstallHeadline, uninstallStages } from "./uninstall"

const run = (over) => ({
  id: "1",
  status: "running",
  phase: "pausing",
  mode: "wipe",
  deleteBackups: false,
  dir: "/opt/jd",
  compose: "docker-compose.yml",
  image: "just-dashboard-backend:latest",
  envPath: "/opt/jd/.env",
  dataDir: "/var/lib/just-dashboard",
  container: "just-dashboard-uninstall",
  actor: "admin",
  startedAt: "2026-10-12T12:00:00Z",
  updatedAt: "2026-10-12T12:00:00Z",
  ...over,
})

test("a wipe has a stage for the deployments and a keep does not", () => {
  expect(uninstallStages(run({}), false).labels).toEqual([
    "Pause deployments",
    "Remove the deployments",
    "Stop the dashboard",
    "Delete its data",
  ])
  expect(uninstallStages(run({ mode: "keep" }), false).labels).toEqual([
    "Pause deployments",
    "Stop the dashboard",
    "Remove its images",
  ])
})

test("losing the page is the stack going, whatever phase was last seen", () => {
  // A run can finish between two polls: the last answer may still say it
  // was removing the deployments.
  const fast = run({ phase: "deployments" })
  expect(stackGone(fast, true)).toBe(true)
  expect(uninstallHeadline(fast, true)).toBe("The dashboard has stopped")
  expect(uninstallStages(fast, true).at).toBe(3)
  expect(uninstallHeadline(fast, false)).toBe("Removing the deployments")
  // A finished run is read from its record, not from contact.
  expect(stackGone(run({ status: "failed" }), true)).toBe(false)
})

test("losing the page once the sibling has it is the stack coming down", () => {
  const handedOver = run({ phase: "stack" })
  expect(stackGone(handedOver, true)).toBe(true)
  expect(uninstallHeadline(handedOver, true)).toBe("The dashboard has stopped")
  // Stopping is done; what is left runs where nothing can watch it.
  expect(uninstallStages(handedOver, true).at).toBe(3)
  expect(uninstallStages(handedOver, false).at).toBe(2)
})

test("a failed run fails at the stage it stopped in", () => {
  const failed = run({
    status: "failed",
    phase: "pausing",
    error: "deployments were still running",
  })
  expect(uninstallStages(failed, false)).toEqual({
    labels: [
      "Pause deployments",
      "Remove the deployments",
      "Stop the dashboard",
      "Delete its data",
    ],
    at: 0,
    outcome: "failed",
  })
  expect(uninstallHeadline(failed, false)).toBe("Uninstall failed")
})

test("the footprint names only what there is", () => {
  const none = {
    projects: 0,
    containers: [],
    routes: [],
    networks: [],
    volumes: [],
    paths: [],
    images: [],
  }
  expect(footprintLine(none)).toBe("nothing on this server")
  expect(
    footprintLine({
      ...none,
      containers: [
        { id: "a", name: "a" },
        { id: "b", name: "b" },
      ],
      images: [{ id: "i", name: "i" }],
      ingress: { id: "c", name: "just-dashboard-ingress" },
    }),
  ).toBe("2 containers · 1 image · the public ingress")
})
