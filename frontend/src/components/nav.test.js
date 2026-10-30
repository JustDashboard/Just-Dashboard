import { expect, test } from "bun:test"
import { tabTitle } from "./nav"

test("a page in the nav names the tab", () => {
  expect(tabTitle("/files")).toBe("Files · Just Dashboard")
  expect(tabTitle("/")).toBe("Overview · Just Dashboard")
  expect(tabTitle("/docker/images")).toBe("Images · Just Dashboard")
  expect(tabTitle("/account/security")).toBe("Security · Just Dashboard")
})

test("a detail page takes the section it sits in", () => {
  expect(tabTitle("/deploy/7/logs")).toBe("Deployments · Just Dashboard")
  expect(tabTitle("/account/no-such-page")).toBe("Account · Just Dashboard")
})

test("an address under no section falls back to the product's name", () => {
  expect(tabTitle("/no-such-page")).toBe("Just Dashboard")
})
