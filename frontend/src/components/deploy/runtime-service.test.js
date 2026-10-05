import { describe, expect, test } from "bun:test"
import {
  isDockerService,
  runtimeLogKind,
  runtimeLogSource,
  runtimeServiceId,
  runtimeManagerUrl,
} from "./runtime-service"
import { liveStack } from "./logs-model"

describe("baseline runtime owner", () => {
  test("native services use their owner log source and identity", () => {
    const pm2 = {
      manager: "pm2",
      containerId: "",
      resourceId: "app-0",
      name: "bot",
      logSource: "pm2:daemon/0/bot",
    }
    expect(isDockerService(pm2)).toBe(false)
    expect(runtimeServiceId(pm2)).toBe("app-0")
    expect(runtimeLogSource(pm2)).toBe("pm2:daemon/0/bot")
    expect(runtimeLogKind(pm2)).toBe("pm2")
    expect(runtimeManagerUrl(pm2)).toBe("/processes/pm2")
    expect(runtimeLogKind({ ...pm2, manager: "systemd", logSource: "journal:bot.service" })).toBe(
      "journal",
    )
  })

  test("a native owner without output never becomes an empty Docker target", () => {
    const service = { manager: "process", containerId: "", resourceId: "1234", name: "node" }
    expect(runtimeLogSource(service)).toBeUndefined()
    expect(runtimeServiceId(service)).toBe("1234")
    expect(runtimeManagerUrl(service)).toBe("/processes")
    expect(
      liveStack([
        { ...service, liveRelease: true, stack: "bot" },
        { ...service, resourceId: "1235", liveRelease: true, stack: "bot" },
      ]),
    ).toBeUndefined()
  })

  test("ordinary containers retain Docker logging and Compose grouping", () => {
    const services = ["api", "db"].map((id) => ({
      containerId: id,
      name: id,
      liveRelease: true,
      stack: "bot",
    }))
    expect(isDockerService(services[0])).toBe(true)
    expect(runtimeLogSource(services[0])).toBe("docker:api")
    expect(runtimeLogKind(services[0])).toBe("docker")
    expect(liveStack(services)).toBe("bot")
  })
})
