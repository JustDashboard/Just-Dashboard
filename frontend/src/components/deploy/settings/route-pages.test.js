import { expect, test } from "bun:test"
import {
  errorPagesState,
  maintenancePolicyState,
  maintenanceState,
  maintenanceUnready,
  retryAfterOf,
  routeMaintenanceOf,
} from "./route-pages"

const ready = { driver: "nginx", route: true, ready: true, on: false }

test("the state says where the switch stands on the live route", () => {
  expect(maintenanceState(undefined)).toBe("Reading the live route")
  expect(maintenanceState({ ...ready, route: false, ready: false })).toBe("No public route yet")
  expect(maintenanceState({ ...ready, ready: false })).toBe(
    "Switches at once after the next deployment",
  )
  expect(maintenanceState(ready)).toBe("Off · visitors reach the application")
  expect(maintenanceState({ ...ready, on: true })).toBe("On · visitors get the maintenance page")
})

test("the switch says why it cannot be turned on, and never holds turning it off", () => {
  expect(maintenanceUnready(ready)).toBe(null)
  expect(maintenanceUnready({ ...ready, route: false, ready: false })).toContain("Add a domain")
  expect(maintenanceUnready({ ...ready, ready: false })).toContain("Deploy once")
  expect(maintenanceUnready({ ...ready, ready: false, on: true })).toBe(null)
})

test("the pages' states name who is let past and which answers get a page", () => {
  expect(maintenancePolicyState(undefined)).toBe("Nobody let past · no Retry-After")
  expect(maintenancePolicyState({ retryAfter: 300, bypassFrom: ["203.0.113.7"] })).toBe(
    "1 address let past · Retry-After 300s",
  )
  expect(errorPagesState([], false)).toBe("The proxy's own pages")
  expect(errorPagesState([502], false)).toBe("Own 502 page")
  expect(errorPagesState([504, 404, 502], true)).toBe(
    "Own 404, 502 and 504 pages · the application's too",
  )
})

test("Retry after is whole seconds up to a day, and 0 or nothing sends none", () => {
  expect(retryAfterOf("")).toBe(undefined)
  expect(retryAfterOf(" 0 ")).toBe(undefined)
  expect(retryAfterOf("300")).toBe(300)
  expect(retryAfterOf("86400")).toBe(86400)
  expect(retryAfterOf("86401")).toBe(null)
  expect(retryAfterOf("1.5")).toBe(null)
  expect(retryAfterOf("-1")).toBe(null)
  expect(retryAfterOf("five")).toBe(null)
})

test("a plan that asks for nothing while in maintenance keeps no maintenance at all", () => {
  expect(routeMaintenanceOf(undefined, [])).toBe(undefined)
  expect(routeMaintenanceOf(120, [])).toEqual({ retryAfter: 120 })
  expect(routeMaintenanceOf(undefined, ["10.0.0.0/8"])).toEqual({ bypassFrom: ["10.0.0.0/8"] })
})
