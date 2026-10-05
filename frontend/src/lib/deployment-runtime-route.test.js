import { expect, test } from "bun:test"
import { deploymentRouteId, deploymentRouteTargets } from "./deployment-runtime-route"

test("external paths wire only to their exact service including private Docker upstreams", () => {
  const services = [
    { id: "web", service: { service: "web", manager: "docker" }, reached: true },
    { id: "api", service: { service: "api", manager: "docker" }, reached: false },
  ]
  const root = { hostname: "app.example.test", path: "/", service: "web" }
  const api = { hostname: "app.example.test", path: "/api", service: "api" }
  expect(deploymentRouteId(root)).not.toBe(deploymentRouteId(api))
  expect(deploymentRouteTargets(root, services).map((service) => service.id)).toEqual(["web"])
  expect(deploymentRouteTargets(api, services).map((service) => service.id)).toEqual(["api"])
  expect(deploymentRouteTargets({ ...api, service: "missing" }, services)).toEqual([])
  expect(deploymentRouteTargets({ hostname: "managed.example.test" }, services)).toEqual([
    services[0],
  ])
})

test("native app bindings require a single original manager when the service is named app", () => {
  const original = { id: "native", service: { manager: "systemd" }, reached: false }
  const route = { id: "external", hostname: "app.example.test", service: "app" }
  expect(deploymentRouteTargets(route, [original])).toEqual([original])
  expect(deploymentRouteTargets(route, [original, { ...original, id: "other" }])).toEqual([])
  expect(deploymentRouteTargets({ ...route, service: "unknown" }, [original])).toEqual([])
  expect(deploymentRouteId(route)).toBe("d:external")
})

test("standalone Docker bindings retain their unique unlabeled runtime and refuse ambiguity", () => {
  const original = { id: "container", service: { manager: "docker" }, reached: false }
  const route = { hostname: "app.example.test", service: "app" }
  expect(deploymentRouteTargets(route, [original])).toEqual([original])
  expect(deploymentRouteTargets(route, [original, { ...original, id: "other" }])).toEqual([])
  expect(
    deploymentRouteTargets(route, [
      original,
      { id: "native", service: { manager: "pm2" }, reached: false },
    ]),
  ).toEqual([])
})
