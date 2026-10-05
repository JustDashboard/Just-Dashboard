import { expect, test } from "bun:test"
import { checkTarget, planGroups, variablesFact } from "./plan-reading"

const flow = {
  name: "shop",
  profile: "web",
  sourceLabel: "acme/shop",
  source: { kind: "git", mode: "git_url", url: "https://github.com/acme/shop.git", ref: "main" },
  candidate: { framework: "nextjs", needsDecision: [] },
  detection: { source: { kind: "git", revision: "a12bc34d56ef7890a12bc34d56ef7890a12bc34d" } },
  configuration: {
    build: {
      method: "recipe",
      recipe: "node",
      buildCommand: "bun run build",
      startCommand: "bun start",
    },
    runtime: { internalPort: 3000, strategy: "blue_green", mounts: [] },
    domains: [{ hostname: "shop.example.com", https: true }],
    checks: [
      {
        name: "Shop answers",
        kind: "http",
        phase: "readiness",
        config: { path: "/healthz", attempts: 30, intervalSeconds: 2 },
      },
    ],
    variables: [],
    dependencies: [],
  },
}

const rows = (groups) =>
  Object.fromEntries(groups.flatMap((group) => group.rows).map((row) => [row.key, row]))

test("the rail reads each part of a repository's plan as the product it is", () => {
  const plan = rows(
    planGroups({ flow, branch: "main", errors: {}, variableCount: 0, checking: false }),
  )
  expect(plan.source).toMatchObject({
    product: "github",
    reading: "acme/shop",
    ref: { branch: "main" },
  })
  expect(plan.build).toMatchObject({
    product: "nextjs",
    reading: "Automatic recipe · Next.js",
    code: "bun run build",
  })
  expect(plan.container).toMatchObject({
    reading: "Port 3000 · candidate first",
    code: "bun start",
  })
  expect(plan.address).toMatchObject({ product: "lets-encrypt", facts: "HTTPS" })
  expect(plan.checks).toMatchObject({ code: "GET /healthz", tone: "default" })
  // An unbounded container is the one omission the rail says out loud.
  expect(plan.limits).toMatchObject({ reading: "No memory or CPU limit", tone: "warning" })
})

test("the steps hold the parts they decide, and a worker has no address", () => {
  const groups = planGroups({
    flow: { ...flow, profile: "worker", configuration: { ...flow.configuration, domains: [] } },
    errors: {},
    variableCount: 0,
    checking: false,
  })
  expect(groups.map((group) => [group.step, group.rows.map((row) => row.key)])).toEqual([
    ["project", ["source", "build"]],
    ["runtime", ["container", "checks", "limits", "storage"]],
    ["variables", ["variables"]],
    ["review", ["preflight"]],
  ])
})

test("preflight's answer is read as what stands between the plan and Deploy", () => {
  const reading = (findings) =>
    rows(planGroups({ flow, errors: {}, variableCount: 0, checking: false, findings })).preflight
  expect(reading(undefined)).toMatchObject({
    reading: "Asked when you reach Review",
    tone: "default",
  })
  expect(reading([{ severity: "pass" }, { severity: "pass" }])).toMatchObject({
    reading: "2 checks passed",
    tone: "passed",
  })
  expect(reading([{ severity: "pass" }, { severity: "warning" }])).toMatchObject({
    reading: "1 to acknowledge · 1 passed",
    tone: "warning",
  })
  expect(reading([{ severity: "blocked" }, { severity: "warning" }])).toMatchObject({
    reading: "1 to fix before it deploys",
    tone: "danger",
  })
})

test("a required variable with nothing behind it is named on the rail", () => {
  const plan = rows(
    planGroups({
      flow: {
        ...flow,
        configuration: {
          ...flow.configuration,
          variables: [{ name: "API_KEY", sensitivity: "secret", required: true }],
        },
      },
      errors: {},
      variableCount: 0,
      checking: false,
    }),
  )
  expect(plan.variables).toMatchObject({
    reading: "1 value still needed",
    code: "API_KEY",
    tone: "warning",
  })
})

test("checks and variable counts read the way Review reads them", () => {
  expect(checkTarget({ kind: "http", config: { path: "/", acceptAnyAnswer: true } })).toBe(
    "GET / (any answer)",
  )
  expect(checkTarget({ kind: "docker_health" })).toBe("the image's HEALTHCHECK")
  expect(variablesFact(3, 0)).toBe("3 declared")
  expect(variablesFact(0, 0)).toBe("None")
})
