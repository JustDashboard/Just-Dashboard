import { describe, expect, test } from "bun:test"
import { notLiveYet, sharedCount, sharedReaders } from "./shared-variables-reading"

const use = (projectId, projectName, variable, waiting = false) => ({
  projectId,
  projectName,
  environmentId: projectId * 10,
  environmentName: "production",
  environmentKind: "production",
  variable,
  waiting,
})

const stripe = {
  name: "STRIPE_SECRET_KEY",
  usedBy: [use(7, "api", "STRIPE_KEY", true), use(8, "shop", "STRIPE_SECRET_KEY")],
}
const region = { name: "REGION", usedBy: [use(7, "api", "AWS_REGION")] }
const sentry = { name: "SENTRY_DSN", usedBy: [use(8, "shop", "SENTRY_DSN", true)] }
const unread = { name: "UNUSED", usedBy: [] }

describe("sharedCount", () => {
  test("the count, then how many have a reader on an earlier value", () => {
    expect(sharedCount([region])).toBe("1 variable")
    expect(sharedCount([stripe, region, sentry, unread])).toBe("4 variables · 2 not live yet")
  })
  test("a variable is not live yet while any reader waits", () => {
    expect(notLiveYet(stripe)).toBe(true)
    expect(notLiveYet(region)).toBe(false)
    expect(notLiveYet(unread)).toBe(false)
  })
})

describe("sharedReaders", () => {
  test("each project once, with what it reads and how much of it waits", () => {
    expect(sharedReaders([stripe, region, sentry, unread])).toEqual([
      {
        projectId: 7,
        projectName: "api",
        variables: 2,
        reads: ["STRIPE_SECRET_KEY", "REGION"],
        waiting: 1,
      },
      {
        projectId: 8,
        projectName: "shop",
        variables: 2,
        reads: ["STRIPE_SECRET_KEY", "SENTRY_DSN"],
        waiting: 1,
      },
    ])
    expect(sharedReaders([unread])).toEqual([])
  })
})
