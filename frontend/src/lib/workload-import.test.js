import { expect, test } from "bun:test"
import { workloadManagerUrl, workloadMatches, workloadPort } from "./workload-import"

const stack = {
  name: "bet-bot",
  kind: "stack",
  resourceId: "bet-bot",
  sourcePath: "/srv/bet-bot/compose.yml",
  services: [
    { name: "cache", image: "redis:7", ports: [] },
    { name: "web", ports: [{ hostPort: 3000, containerPort: 8080 }] },
  ],
}

test("discovery search reaches service images, ports, configuration and manager", () => {
  for (const query of [
    " BET-BOT ",
    "redis:7",
    "3000",
    "compose.yml",
    "Compose stack",
    "cache",
    "",
  ]) {
    expect(workloadMatches(stack, query)).toBe(true)
  }
  expect(workloadMatches(stack, "5678")).toBe(false)
})

test("original-manager links remain local even under browser URL normalization", () => {
  expect(workloadManagerUrl({ managerUrl: "/docker/stacks/bet-bot" })).toBe(
    "/docker/stacks/bet-bot",
  )
  for (const managerUrl of [
    "https://example.com",
    "//example.com",
    "/\\example.com",
    "/\t/example.com",
    "javascript:alert(1)",
  ]) {
    expect(workloadManagerUrl({ managerUrl })).toBeUndefined()
  }
})

test("port labels preserve IPv6, protocol and mapped container ports", () => {
  expect(workloadPort({ hostIp: "::", hostPort: 3000, containerPort: 8080, protocol: "tcp" })).toBe(
    "[::]:3000 → 8080/tcp",
  )
  expect(
    workloadPort({ hostIp: "127.0.0.1", hostPort: 53, containerPort: 53, protocol: "udp" }),
  ).toBe("127.0.0.1:53/udp")
})
