import { describe, expect, test } from "bun:test"
import {
  cronJobHref,
  cronJobId,
  laneHref,
  parseCronJobId,
  sheetAddress,
  timerHref,
} from "./schedule-links"

describe("a schedule's page", () => {
  test("an account's job is named by the account and its line", () => {
    expect(cronJobId({ key: "cron:3", kind: "cron" }, "root")).toBe("root:3")
    expect(cronJobHref("root:3")).toBe("/processes/scheduled/cron/root%3A3")
    expect(cronJobHref("root:3", "runs")).toBe("/processes/scheduled/cron/root%3A3?tab=runs")
    expect(cronJobHref("root:3", "overview")).toBe("/processes/scheduled/cron/root%3A3")
  })

  test("a package file's line keeps its lane key, path and all", () => {
    const id = cronJobId({ key: "system:/etc/crontab:20", kind: "system" }, "root")
    expect(id).toBe("system:/etc/crontab:20")
    expect(parseCronJobId(id)).toEqual({ kind: "system", key: "system:/etc/crontab:20" })
  })

  test("an account named system is still an account", () => {
    expect(parseCronJobId("system:4")).toEqual({ kind: "cron", user: "system", key: "cron:4" })
    expect(parseCronJobId("deploy:12")).toEqual({ kind: "cron", user: "deploy", key: "cron:12" })
    expect(parseCronJobId("nonsense")).toBeNull()
  })

  test("a timer is its unit, and a lane finds its own page", () => {
    expect(timerHref("certbot.timer")).toBe("/processes/scheduled/timers/certbot.timer")
    expect(
      laneHref(
        { key: "timer:certbot.timer", kind: "timer", timer: { unit: "certbot.timer" } },
        "root",
      ),
    ).toBe("/processes/scheduled/timers/certbot.timer")
    expect(laneHref({ key: "cron:3", kind: "cron" }, "deploy", "runs")).toBe(
      "/processes/scheduled/cron/deploy%3A3?tab=runs",
    )
  })
})

test("the old sheets' addresses lead to the pages", () => {
  expect(sheetAddress({ timer: "certbot.timer" }, "root")).toBe(
    "/processes/scheduled/timers/certbot.timer",
  )
  expect(sheetAddress({ job: "cron:3" }, "root")).toBe("/processes/scheduled/cron/root%3A3")
  expect(sheetAddress({ job: "system:/etc/crontab:20" }, "root")).toBe(
    "/processes/scheduled/cron/system%3A%2Fetc%2Fcrontab%3A20",
  )
  expect(sheetAddress({}, "root")).toBeNull()
})
