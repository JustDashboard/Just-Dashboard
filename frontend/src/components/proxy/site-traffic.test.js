import { expect, test } from "bun:test"
import { busiestSites } from "./site-traffic"

const site = (over) => ({
  site: "shop.example.test",
  engine: "nginx",
  status: "available",
  requests: 0,
  errorRate: 0,
  ...over,
})

test("the busiest sites are figures and shares of the edge's whole hour, not lengths", () => {
  const rows = busiestSites([
    site({ site: "quiet.example.test", requests: 100 }),
    site({ site: "shop.example.test", requests: 300, errorRate: 0.06 }),
    site({ site: "dark.example.test", status: "unavailable", reason: "It writes no access log" }),
  ])
  expect(rows.map((row) => row.label)).toEqual([
    "shop.example.test",
    "quiet.example.test",
    "dark.example.test",
  ])
  // Of all 400 requests, not of the busiest site's 300.
  expect(rows.map((row) => row.share)).toEqual([0.75, 0.25, undefined])
  expect(rows[0]).toMatchObject({ value: "300/h", hint: "6.0% 5xx", tone: "danger" })
  expect(rows[1]).toMatchObject({ value: "100/h", hint: "0.0% 5xx", tone: "default" })
  // A site that logs nothing has no figure to rank by and says why.
  expect(rows[2]).toMatchObject({
    value: "—",
    hint: "no log",
    tone: "default",
    title: "It writes no access log",
  })
  expect(rows[0].href).toBe("/proxy/traffic?site=shop.example.test")
})

test("an hour in which nothing was served has no whole to be a share of", () => {
  expect(busiestSites([site({ requests: 0 })])[0].share).toBeUndefined()
  expect(busiestSites([site({}), site({ site: "b" })], 1)).toHaveLength(1)
})
