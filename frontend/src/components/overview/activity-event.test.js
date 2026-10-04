import { expect, test } from "bun:test"
import { activityAction, activityStatus } from "./activity-event"

const event = (patch = {}) => ({
  ts: "2026-10-04T14:29:00Z",
  kind: "action",
  title: "terminal.kill d4a0e8350b641a59",
  severity: "info",
  ...patch,
})

test("action labels separate the operation from the complete host target", () => {
  expect(activityAction(event())).toEqual({
    action: "terminal.kill",
    target: "d4a0e8350b641a59",
    label: "Close terminal",
  })
  expect(activityAction(event({ title: "post:docker.container.restart shop web" }))).toEqual({
    action: "post:docker.container.restart",
    target: "shop web",
    label: "Restart container",
  })
  expect(activityAction(event({ title: "file.delete /srv/My Project/a.txt" }))?.target).toBe(
    "/srv/My Project/a.txt",
  )
})

test("unfamiliar actions retain their name, and prose or job names are never rewritten", () => {
  expect(activityAction(event({ title: "new.feature.reset" }))).toEqual({
    action: "new.feature.reset",
    target: "",
    label: undefined,
  })
  expect(activityAction(event({ title: "Restarted grafana" }))).toBeUndefined()
  expect(activityAction(event({ kind: "deploy", title: "terminal.kill" }))).toBeUndefined()
})

test("request acceptance does not claim the requested restart has finished", () => {
  expect(activityStatus(event({ title: "dashboard.restart rebuild" }))).toEqual({
    label: "Accepted",
    tone: "running",
  })
  expect(activityStatus(event({ severity: "error" })).label).toBe("Failed")
  expect(activityStatus(event({ severity: "warning" })).label).toBe("Warning")
  expect(activityStatus(event({ kind: "reboot", severity: "warning" })).label).toBe("Restarted")
})

test("run outcomes come from recorder details, never an informational severity or job name", () => {
  for (const kind of ["deploy", "backup"]) {
    const run = (detail) => event({ kind, title: "success", detail })
    expect(activityStatus(run(undefined)).label).toBe("Recorded")
    expect(activityStatus(run(`${kind} pending`)).label).toBe("Pending")
    expect(activityStatus(run(`${kind} running`)).label).toBe("Running")
    expect(activityStatus(run(`${kind} success`)).label).toBe("Succeeded")
    expect(activityStatus(run(`${kind} cancelled`)).label).toBe("Cancelled")
    const otherKind = kind === "deploy" ? "backup" : "deploy"
    expect(activityStatus(run(`${otherKind} success`)).label).toBe("Recorded")
    expect(
      activityStatus(event({ kind, detail: `${kind} success`, severity: "error" })).label,
    ).toBe("Failed")
  }
})
