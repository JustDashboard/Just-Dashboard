import { expect, test } from "bun:test"
import {
  isUserTemplate,
  splitCatalogue,
  templateDeployHref,
  templateId,
  templateOrigin,
  templateProvenance,
  templateRuns,
  templateUse,
} from "./templates"

test("the catalogue splits into the built-ins and this dashboard's own, keeping order", () => {
  const entries = [
    { id: "whoami", origin: "builtin" },
    { id: "user-shop", origin: "user" },
    { id: "postgresql" },
    { id: "user-stack" },
  ]
  const { builtIn, yours } = splitCatalogue(entries)
  expect(builtIn.map((entry) => entry.id)).toEqual(["whoami", "postgresql"])
  expect(yours.map((entry) => entry.id)).toEqual(["user-shop", "user-stack"])
  expect(isUserTemplate("user-shop")).toBe(true)
  expect(isUserTemplate("whoami")).toBe(false)
  expect(isUserTemplate(undefined)).toBe(false)
})

test("a name is saved under the id the server gives it", () => {
  expect(templateId("Analytics Stack")).toBe("user-analytics-stack")
  expect(templateId("  nginx + Redis  ")).toBe("user-nginx-redis")
  expect(templateId("user-already-here")).toBe("user-already-here")
  expect(templateId("My_App.v2")).toBe("user-my-app-v2")
  expect(templateId("a".repeat(80))).toBe("user-" + "a".repeat(59))
  expect(templateId("")).toBeUndefined()
  expect(templateId("!!!")).toBeUndefined()
})

test("rows say how a template was made, what it runs and who uses it", () => {
  expect(templateOrigin({ source: "compose" })).toBe("From a Compose file")
  expect(templateOrigin({ source: "deployment" })).toBe("Saved from a deployment")
  expect(templateOrigin({ source: "import" })).toBe("Imported")
  expect(
    templateRuns({ image: "nginx:1.27", services: [{ name: "web" }, { name: "cache" }] }),
  ).toBe("2 services: web, cache")
  expect(templateRuns({ image: "traefik/whoami:v1.12.0" })).toBe("traefik/whoami:v1.12.0")
  expect(templateUse(0)).toBe("No deployment uses it")
  expect(templateUse(1)).toBe("1 deployment uses it")
  expect(templateUse(3)).toBe("3 deployments use it")
})

test("a chosen template says whether it was reviewed or added here", () => {
  const provenance = { reviewedAt: "2026-10-29", maintainer: "admin" }
  expect(templateProvenance({ id: "user-shop", provenance })).toBe(
    "Added 2026-10-29 by admin on this dashboard.",
  )
  expect(templateProvenance({ id: "whoami", provenance })).toBe("Reviewed 2026-10-29 by admin.")
  expect(templateDeployHref("user-shop")).toBe("/deploy/new?source=template&template=user-shop")
})
