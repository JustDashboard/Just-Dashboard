import { expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Cidr } from "./address"

const text = (cidr) => renderToStaticMarkup(createElement(Cidr, { cidr })).replace(/<[^>]+>/g, "")

test("a host route's prefix is left off in its own family only", () => {
  expect(text("192.0.2.7/32")).toBe("192.0.2.7")
  expect(text("2001:db8::7/128")).toBe("2001:db8::7")
})

test("an IPv6 /32 is a network and keeps its prefix", () => {
  expect(text("2001:db8::/32")).toBe("2001:db8::/32")
})

test("a network keeps its prefix, and a bare address draws as it is", () => {
  expect(text("10.0.0.0/8")).toBe("10.0.0.0/8")
  expect(text("fd00::/64")).toBe("fd00::/64")
  expect(text("192.0.2.7")).toBe("192.0.2.7")
})
