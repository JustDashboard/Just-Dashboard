import { expect, test } from "bun:test"
import { FLOW_ANCHOR_VIEW, FLOW_VIEWS, flowHref, flowViewOf } from "./views"

test("an address with no view opens the socket hours", () => {
  expect(flowViewOf(null)).toBe("hours")
  expect(flowViewOf(undefined)).toBe("hours")
  expect(flowViewOf("")).toBe("hours")
})

test("the address names the view, and a name that is none falls to the socket hours", () => {
  expect(flowViewOf("observer")).toBe("observer")
  expect(flowViewOf("sample")).toBe("sample")
  expect(flowViewOf("coverage")).toBe("hours")
})

test("the default view's address carries no tab", () => {
  expect(flowHref("hours")).toBe("/network/flows")
  expect(flowHref("observer")).toBe("/network/flows?tab=observer")
})

test("every block the picture leads to is in a view the page has", () => {
  for (const view of Object.values(FLOW_ANCHOR_VIEW)) expect(FLOW_VIEWS).toContain(view)
})
