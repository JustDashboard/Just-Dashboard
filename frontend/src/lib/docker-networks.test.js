import { expect, test } from "bun:test"
import {
  driverReading,
  isChangePreview,
  membersKnown,
  orderedConflicts,
  ownerWords,
  prunePlan,
} from "./docker-networks"

test("conflicts read refusals first and keep the backend's order within a level", () => {
  const ordered = orderedConflicts([
    { code: "peers", level: "warn", message: "peers" },
    { code: "compose", level: "info", message: "compose" },
    { code: "dashboard", level: "block", message: "dashboard" },
    { code: "last", level: "warn", message: "last" },
  ])
  expect(ordered.map((c) => c.code)).toEqual(["dashboard", "peers", "last", "compose"])
})

test("an older backend's answer is not believed as an empty preview", () => {
  expect(isChangePreview([])).toBe(false)
  expect(isChangePreview({ conflicts: [], blocked: false })).toBe(true)
})

test("owners are named by who acts on the network next", () => {
  expect(ownerWords({ kind: "deployment", deployment: "shop · production" })).toBe(
    "Deployment · shop · production",
  )
  expect(ownerWords({ kind: "deployment" })).toBe("Deployment (gone)")
  expect(ownerWords({ kind: "compose", project: "shop" })).toBe("Compose · shop")
  expect(ownerWords(undefined)).toBeUndefined()
})

test("unread membership never reads as an unused network", () => {
  expect(membersKnown({})).toBe(true)
  expect(membersKnown({ membersKnown: false })).toBe(false)
})

test("the reviewed prune keeps what is still named, with the first reason that matters", () => {
  const plan = prunePlan([
    { id: "1", name: "spare", owner: { kind: "manual" }, conflicts: [], removable: true },
    {
      id: "2",
      name: "dormant",
      owner: { kind: "compose", project: "shop" },
      conflicts: [
        { code: "compose_recreates", level: "info", message: "Compose recreates it" },
        { code: "stopped_dependents", level: "warn", message: "batch still names this network" },
      ],
      removable: false,
    },
  ])
  expect(plan.removed.map((c) => c.name)).toEqual(["spare"])
  expect(plan.kept[0].reason).toBe("batch still names this network")
})

test("built-in drivers flag option keys Docker would ignore; plugins vouch for their own", () => {
  const catalogue = {
    drivers: [
      {
        name: "bridge",
        source: "builtin",
        scope: "local",
        creatable: true,
        options: [{ key: "com.docker.network.driver.mtu", description: "" }],
      },
      { name: "acme/fabric", source: "plugin", scope: "local", creatable: true, options: [] },
    ],
    swarm: "inactive",
    manager: false,
    pluginsRead: true,
    checkedAt: "",
    limitations: [],
  }
  const bridge = driverReading(
    catalogue,
    "",
    "com.docker.network.driver.mtu=1400\ncom.docker.network.bridge.mtu=1400",
  )
  expect(bridge.known && bridge.ignored).toEqual(["com.docker.network.bridge.mtu"])
  const plugin = driverReading(catalogue, "acme/fabric", "anything=1")
  expect(plugin.known && plugin.ignored).toEqual([])
  const absent = driverReading(catalogue, "calico", "")
  expect(absent.known && absent.driver).toBeUndefined()
  expect(driverReading(undefined, "macvlan", "").known).toBe(false)
})
