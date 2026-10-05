import { expect, test } from "bun:test"
import {
  adoptionReviewMetadata,
  groupImportWarnings,
  recoveredEnvironmentGroups,
  recoveredEnvironmentSatisfied,
  recoveredInputBound,
  retainedVariableSatisfied,
  workloadManagerUrl,
  workloadMatches,
  workloadPort,
} from "./workload-import"

test("recovered input protection follows current bindings and supports legacy aliases", () => {
  expect(recoveredInputBound()).toBe(false)
  expect(recoveredInputBound({ storageKey: "PORT" })).toBe(false)
  expect(recoveredInputBound({ storageKey: "JD_IMPORT_ENV_PORT" })).toBe(true)
  expect(recoveredInputBound({ storageKey: "JD_IMPORTED_ARG_0", bound: true })).toBe(true)
  expect(recoveredInputBound({ storageKey: "JD_IMPORT_ENV_PORT", bound: false })).toBe(false)
})

test("remembered adoption preserves server scope and exclusions but drops baseline and private extras", () => {
  const metadata = adoptionReviewMetadata({
    key: "stack:bot",
    digest: "inspected",
    kind: "stack",
    resourceId: "bot",
    manager: "docker",
    name: "bot",
    serviceCount: 4,
    runningCount: 2,
    warnings: [],
    blockers: [],
    scope: "existing_services",
    excludedServices: ["optional-worker"],
    baseline: { environment: { TOKEN: "must-not-persist" } },
    unexpectedEnvironment: { TOKEN: "must-not-persist" },
  })
  expect(metadata.scope).toBe("existing_services")
  expect(metadata.excludedServices).toEqual(["optional-worker"])
  expect(JSON.stringify(metadata)).not.toContain("must-not-persist")
  expect(metadata).not.toHaveProperty("baseline")
  expect(metadata).not.toHaveProperty("unexpectedEnvironment")
})

test("captured environment groups original keys by service and separates proven image defaults", () => {
  const inputs = [
    {
      storageKey: "alias-one",
      name: "TELEGRAM_CHAT_ID",
      service: "worker",
      kind: "environment",
      category: "application",
      empty: false,
    },
    {
      storageKey: "alias-two",
      name: "TELEGRAM_CHAT_ID",
      service: "bot",
      kind: "environment",
      category: "application",
      empty: true,
    },
    {
      storageKey: "alias-path",
      name: "PATH",
      service: "worker",
      kind: "environment",
      category: "image_default",
      empty: false,
    },
    {
      storageKey: "alias-label",
      name: "owner",
      service: "worker",
      kind: "label",
      category: "runtime_setting",
      empty: false,
    },
  ]
  const groups = recoveredEnvironmentGroups(inputs)
  expect(groups.map((group) => group.service)).toEqual(["worker", "bot"])
  expect(groups[0].application.map((input) => input.name)).toEqual(["TELEGRAM_CHAT_ID"])
  expect(groups[0].imageDefaults.map((input) => input.name)).toEqual(["PATH"])
  expect(retainedVariableSatisfied("alias-one", ["alias-one"], inputs)).toBe(true)
  expect(retainedVariableSatisfied("alias-two", ["alias-two"], inputs)).toBe(false)
  expect(
    recoveredEnvironmentSatisfied("TELEGRAM_CHAT_ID", ["alias-one", "alias-two"], inputs),
  ).toBe(false)
  expect(recoveredEnvironmentSatisfied("PATH", ["alias-path"], inputs)).toBe(true)
})

test("one grouped warning still requires every distinct server acknowledgement", () => {
  const warnings = [
    {
      code: "adoption_data_abc",
      issueCode: "persistent_data_reused",
      service: "bot",
      title: "Data",
      severity: "warning",
    },
    {
      code: "adoption_data_def",
      issueCode: "persistent_data_reused",
      service: "worker",
      title: "Data",
      severity: "warning",
    },
    { code: "runtime_privileged", title: "Runtime", severity: "warning" },
  ]
  const groups = groupImportWarnings(warnings)
  expect(groups).toHaveLength(2)
  expect(groups[0].codes).toEqual(["adoption_data_abc", "adoption_data_def"])
  expect(groups[0].services).toEqual(["bot", "worker"])
  expect(groups[0].details).toHaveLength(2)
})

test("remembered descriptors strip private additions recursively", () => {
  const metadata = adoptionReviewMetadata({
    key: "stack:bot",
    digest: "inspected",
    kind: "stack",
    resourceId: "bot",
    manager: "docker",
    name: "bot",
    serviceCount: 1,
    runningCount: 1,
    warnings: [],
    blockers: [],
    inputs: [
      {
        storageKey: "alias",
        name: "TOKEN",
        kind: "environment",
        origin: "container",
        category: "application",
        sensitivity: "secret",
        retained: true,
        empty: false,
        value: "never-persist",
      },
    ],
    buildSources: [{ service: "bot", status: "snapshot", privatePath: "never-persist" }],
    ingressBindings: [
      {
        id: "route",
        hostname: "example.test",
        path: "/",
        service: "bot",
        owner: "nginx",
        proxyKind: "nginx",
        status: "linked",
        continuity: "host_port",
        rawConfiguration: "never-persist",
      },
    ],
  })
  expect(JSON.stringify(metadata)).not.toContain("never-persist")
  expect(metadata.inputs[0].name).toBe("TOKEN")
  expect(metadata.ingressBindings[0].hostname).toBe("example.test")
})

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
