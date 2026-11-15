import { expect, test } from "bun:test"
import { addressTail, nearestPage } from "./missing-page-model"

const admin = () => true
const reader = (capability) => capability === "read"

test("an address under a page offers that page", () => {
  expect(nearestPage("/metrics/no-such-view", admin)).toMatchObject({
    title: "Metrics",
    href: "/metrics",
  })
  expect(nearestPage("/docker/images/a/b", admin)).toMatchObject({
    title: "Images",
    href: "/docker/images",
  })
  expect(nearestPage("/account/nowhere", admin)?.href).toBe("/account")
})

test("a section's own name wins over its landing page's", () => {
  expect(nearestPage("/docker/nowhere", admin)).toMatchObject({ title: "Docker", href: "/docker" })
  expect(nearestPage("/deploy/a/b/c/d/e", admin)?.title).toBe("Deployments")
})

test("an address under no page offers nothing, and the Overview is never the nearest", () => {
  expect(nearestPage("/this-page-does-not-exist", admin)).toBeUndefined()
  expect(nearestPage("/", admin)).toBeUndefined()
  // A prefix that is not a whole part of the path is another address.
  expect(nearestPage("/metricsandmore", admin)).toBeUndefined()
})

test("a page the role does not reach is not offered", () => {
  expect(nearestPage("/metrics/alerts/nope", admin)?.href).toBe("/metrics/alerts")
  // Alerts is an administrator's: a reader is offered Metrics above it.
  expect(nearestPage("/metrics/alerts/nope", reader)?.href).toBe("/metrics")
})

test("the node names the last part of the address, cut where it runs long", () => {
  expect(addressTail("/metrics/no-such-view")).toBe("/no-such-view")
  expect(addressTail("/a/" + "x".repeat(40))).toBe("/" + "x".repeat(22) + "…")
  expect(addressTail("/")).toBe("/")
})
