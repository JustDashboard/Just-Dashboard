import { expect, test } from "bun:test"
import { kindViews } from "./kind-views"

const ids = (source) => kindViews(source).map((view) => view.id)

test("a container and a stack are read by what Docker did to them, a unit by its runs", () => {
  expect(ids({ id: "docker:shop-web", kind: "docker" })).toEqual(["events"])
  expect(ids({ id: "stack:shop", kind: "stack" })).toEqual(["events"])
  expect(ids({ id: "journal:caddy.service", kind: "journal" })).toEqual(["runs"])
})

test("the whole journal, a file and the kernel's ring have no view of their own", () => {
  expect(ids({ id: "journal:", kind: "journal" })).toEqual([])
  expect(ids({ id: "file:/var/log/nginx/error.log", kind: "nginx" })).toEqual([])
  expect(ids({ id: "kernel:", kind: "kernel" })).toEqual([])
  expect(ids({ id: "journal-id:CRON,crond", kind: "journal-id" })).toEqual([])
})
