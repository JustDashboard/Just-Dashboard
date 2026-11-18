import { expect, test } from "bun:test"
import { monoBreaks } from "./mono-breaks"

test("a path breaks after its slashes and keeps its leading one on the first word", () => {
  expect(monoBreaks("/etc/just-dashboard/network/links.batch")).toEqual([
    "/etc/",
    "just-dashboard/",
    "network/",
    "links.",
    "batch",
  ])
})

test("a unit's name breaks at its dot, never inside a word", () => {
  expect(monoBreaks("just-dashboard-network.service")).toEqual([
    "just-dashboard-network.",
    "service",
  ])
})

test("an address breaks after each colon, and a run of separators stays whole", () => {
  expect(monoBreaks("2001:db8::1")).toEqual(["2001:", "db8::", "1"])
  expect(monoBreaks("http://just-dashboard-ingress:80")).toEqual([
    "http://",
    "just-dashboard-ingress:",
    "80",
  ])
})

test("the pieces join to the value again", () => {
  for (const value of ["", "/", "a", "10.0.0.1/24", "::1", "a.b.", "//x"]) {
    expect(monoBreaks(value).join("")).toBe(value)
  }
})
