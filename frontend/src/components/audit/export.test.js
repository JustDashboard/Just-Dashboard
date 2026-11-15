import { expect, test } from "bun:test"
import { auditCsv, exportName } from "./export"

const entry = (fields) => ({
  id: 1,
  ts: "2026-11-13T06:45:43Z",
  userId: 1,
  username: "operator",
  role: "admin",
  ip: "100.64.1.2",
  actor: "session",
  action: "docker.container.restart",
  target: "web",
  method: "POST",
  path: "/api/v1/docker/containers/web/restart",
  status: 200,
  success: true,
  detail: "",
  ...fields,
})

test("an entry is one row with every field the trail keeps", () => {
  const csv = auditCsv([
    entry({}),
    entry({
      id: 2,
      actor: "token",
      keyId: 12,
      keyName: "ci-deploy",
      status: 403,
      success: false,
      detail: 'missing capability "destructive", refused',
    }),
  ])
  expect(csv.split("\n")).toEqual([
    "id,time,user,role,actor,key,key id,hub user,session,address,action,target,method,path,status,result,detail",
    "1,2026-11-13T06:45:43Z,operator,admin,session,,,,,100.64.1.2,docker.container.restart,web,POST,/api/v1/docker/containers/web/restart,200,ok,",
    '2,2026-11-13T06:45:43Z,operator,admin,token,ci-deploy,12,,,100.64.1.2,docker.container.restart,web,POST,/api/v1/docker/containers/web/restart,403,failed,"missing capability ""destructive"", refused"',
    "",
  ])
})

test("an entry the dashboard wrote for itself has no status, and says how it went", () => {
  const [, row] = auditCsv([entry({ status: 0, method: "", path: "" })]).split("\n")
  expect(row.endsWith(",web,,,,ok,")).toBe(true)
})

test("a cell a spreadsheet would run as a formula is text", () => {
  const [, row] = auditCsv([entry({ target: "=HYPERLINK(1)", detail: "-2+3" })]).split("\n")
  expect(row).toContain(",'=HYPERLINK(1),")
  expect(row.endsWith(",'-2+3")).toBe(true)
})

test("the file is named for the day and for what narrowed it", () => {
  const today = new Date("2026-11-13T10:00:00Z")
  expect(exportName({ username: "", action: "", failed: false }, today)).toBe(
    "audit-2026-11-13.csv",
  )
  expect(exportName({ username: "mira", action: "docker.", failed: true }, today)).toBe(
    "audit-mira-docker-failed-2026-11-13.csv",
  )
  expect(exportName({ username: "", action: "../etc passwd", failed: false }, today)).toBe(
    "audit-etc-passwd-2026-11-13.csv",
  )
})
