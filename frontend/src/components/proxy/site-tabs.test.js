import { describe, expect, test } from "bun:test"
import { leavesSettings, settingsView, siteHref, siteSettingsHref, siteTab } from "./site-tabs.ts"

describe("a site's views", () => {
  test("an unknown or missing tab is the Overview", () => {
    expect(siteTab(null)).toBe("overview")
    expect(siteTab("requests")).toBe("overview")
    expect(siteTab("logs")).toBe("logs")
    expect(siteTab("settings")).toBe("settings")
  })

  test("an unknown or missing part of Settings is General", () => {
    expect(settingsView(undefined)).toBe("general")
    expect(settingsView("tls")).toBe("general")
    expect(settingsView("https")).toBe("https")
    expect(settingsView("file")).toBe("file")
  })

  test("the Overview is the bare address, and General is Settings without a view", () => {
    expect(siteHref("app.example.com")).toBe("/proxy/sites/app.example.com")
    expect(siteHref("app.example.com", "logs")).toBe("/proxy/sites/app.example.com?tab=logs")
    expect(siteSettingsHref("app.example.com")).toBe("/proxy/sites/app.example.com?tab=settings")
    expect(siteSettingsHref("app.example.com", "general")).toBe(
      "/proxy/sites/app.example.com?tab=settings",
    )
    expect(siteSettingsHref("app.example.com", "access")).toBe(
      "/proxy/sites/app.example.com?tab=settings&view=access",
    )
  })

  test("a name with a slash or a space is one path segment", () => {
    expect(siteHref("docker-caddy:shop /a", "logs")).toBe(
      "/proxy/sites/docker-caddy%3Ashop%20%2Fa?tab=logs",
    )
  })
})

describe("leaving a site's Settings", () => {
  const here = new URL("http://host/proxy/sites/app.example.com?tab=settings&view=https")
  const to = (path) => new URL(path, here)

  test("another part of Settings stays in the form", () => {
    expect(leavesSettings(here, to("/proxy/sites/app.example.com?tab=settings"))).toBe(false)
    expect(leavesSettings(here, to("/proxy/sites/app.example.com?tab=settings&view=file"))).toBe(
      false,
    )
    expect(leavesSettings(here, to("?tab=settings&view=https#site-custom"))).toBe(false)
  })

  test("another view of the site, another site and another page all leave it", () => {
    expect(leavesSettings(here, to("/proxy/sites/app.example.com"))).toBe(true)
    expect(leavesSettings(here, to("/proxy/sites/app.example.com?tab=logs"))).toBe(true)
    expect(leavesSettings(here, to("/proxy/sites/legacy.example.com?tab=settings"))).toBe(true)
    expect(leavesSettings(here, to("/proxy/certificates?issue=app.example.com"))).toBe(true)
  })

  test("an address on another origin is the browser's to ask about", () => {
    expect(leavesSettings(here, new URL("https://app.example.com/"))).toBe(false)
  })
})
