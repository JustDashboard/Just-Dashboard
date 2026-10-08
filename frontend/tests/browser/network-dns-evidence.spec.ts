import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
import { overrides } from "./network-dns-fixture"
import type { DNSInvestigationRequest, SavedDNSEvidence } from "../../src/lib/network-dns-evidence"

const id = "a".repeat(32)
function record(): SavedDNSEvidence {
  const reading = (state: string, basis: string, summary: string) => ({ state, basis, summary })
  return {
    id,
    request: { name: "secret.corp.example", type: "AAAA", expectedInterface: "vpn0" },
    status: "completed",
    startedAt: "2026-10-08T19:00:00Z",
    endedAt: "2026-10-08T19:00:01Z",
    result: {
      version: 1,
      request: { name: "secret.corp.example", type: "AAAA", expectedInterface: "vpn0" },
      startedAt: "2026-10-08T19:00:00Z",
      endedAt: "2026-10-08T19:00:01Z",
      owner: "systemd-resolved",
      ownerVersion: "systemd 257",
      ownerIdentity: ":1.42",
      vantage: "host_native_resolver",
      answerFamily: "inet6",
      upstreamFamily: "not_measured",
      policyMatch: "longest suffix: 2 labels",
      policyStable: true,
      policy: [
        {
          index: 7,
          interface: "vpn0",
          domains: ["~corp.example"],
          servers: ["10.0.0.53#dns.corp.example"],
          currentServer: "10.0.0.53#dns.corp.example",
          defaultRoute: false,
          activeDNS: true,
          dnssec: "yes",
          dnsOverTLS: "yes",
          negativeTrustAnchors: [],
        },
      ],
      answerInterfaces: [7],
      answers: ["2001:db8::7"],
      records: [{ interfaceIndex: 7, owner: "secret.corp.example", type: 28, ttl: 60 }],
      nativeFlags: "8651265",
      route: reading(
        "measured",
        "native_reply",
        "The native reply reports answering link index 7; the exact upstream endpoint remains unmeasured.",
      ),
      transport: reading(
        "encrypted",
        "native_reply",
        "Native-reported fresh network encryption; upstream forwarding is unknown.",
      ),
      trust: reading(
        "native_policy_validated",
        "native_reply_and_configuration",
        "The native strict TLS policy validates its declared identity; certificate inspection was not performed.",
      ),
      dnssec: reading(
        "validated",
        "native_reply",
        "Native resolver reports authenticated fresh DNS data under its trust policy.",
      ),
      nss: reading(
        "not_measured",
        "scope",
        "Hosts, NSS and search semantics are not measured by this wire record query.",
      ),
      limitations: ["Application DoH, upstream forwarding and provider layers remain unmeasured."],
    },
  }
}

async function setup(
  page: Page,
  options: {
    readonly?: boolean
    failedLaunch?: boolean
    interrupted?: boolean
    alias?: boolean
  } = {},
) {
  const calls: DNSInvestigationRequest[] = []
  const saved = record()
  if (options.alias && saved.result) {
    const result = saved.result
    saved.request = { ...saved.request, name: "alias.corp.example" }
    result.request = saved.request
    const unknown = { state: "unknown", basis: "unmeasured", summary: "No accepted native flags." }
    const common = {
      ownerIdentity: result.ownerIdentity!,
      policyMatch: result.policyMatch,
      policy: result.policy,
      policyAfter: result.policy,
      policyStable: true,
      snapshotBefore: "b".repeat(64),
      snapshotAfter: "b".repeat(64),
      answerInterfaces: result.answerInterfaces,
      records: result.records,
      route: result.route,
      transport: result.transport,
      trust: result.trust,
      dnssec: result.dnssec,
      nativeFlags: result.nativeFlags,
    }
    result.hops = [
      {
        ...common,
        name: "alias.corp.example",
        type: "AAAA",
        answers: [],
        records: [],
        answerInterfaces: [],
        nativeFlags: undefined,
        transport: unknown,
        trust: unknown,
        dnssec: unknown,
        error: "Call failed: CNAME resolving disabled on 'alias.corp.example'",
      },
      {
        ...common,
        name: "alias.corp.example",
        type: "CNAME",
        answers: ["secret.corp.example."],
        records: [{ interfaceIndex: 7, owner: "alias.corp.example", type: 5, ttl: 60 }],
        aliasTarget: "secret.corp.example",
      },
      {
        ...common,
        name: "secret.corp.example",
        type: "AAAA",
        answers: result.answers,
      },
    ]
  }
  if (options.interrupted) {
    saved.status = "interrupted"
    delete saved.result
  }
  await mockNetwork(page, [], {
    overrides,
    session: options.readonly
      ? { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
      : admin,
  })
  await page.route("**/api/v1/network/dns/evidence**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() === "POST") {
      calls.push(request.postDataJSON())
      if (options.failedLaunch)
        return json(
          route,
          {
            error: {
              code: "unavailable",
              message: "Native owner unavailable; no alternate resolver was used.",
            },
          },
          503,
        )
      return json(route, saved, 201)
    }
    if (path.endsWith(`/${id}`)) return json(route, saved)
    return json(route, [{ ...saved, result: undefined }])
  })
  await page.goto("/network/dns")
  await expect(
    page.getByRole("heading", { name: "Policy investigation", exact: true }),
  ).toBeVisible()
  return calls
}

test("retained DNS evidence separates answering links, native trust and application scope", async ({
  page,
}) => {
  await setup(page)
  await page
    .getByRole("button", { name: "secret.corp.example · AAAA · completed", exact: true })
    .click()
  const report = page.getByLabel("DNS policy evidence report")
  await expect(report.getByText("Native reports encryption", { exact: true })).toBeVisible()
  await expect(report.getByText("Native reports DNSSEC validation", { exact: true })).toBeVisible()
  await expect(report.getByText("Native strict TLS policy", { exact: true })).toBeVisible()
  await expect(report.getByText("2001:db8::7", { exact: true })).toBeVisible()
  await expect(report).toContainText("not a per-query endpoint")
  await expect(report).toContainText("Hosts, NSS and search semantics")
  await expect(report).toContainText("provider layers remain unmeasured")
  await expect(report.getByRole("link", { name: "Export evidence" })).toHaveAttribute(
    "href",
    `/api/v1/network/dns/evidence/${id}/export`,
  )
})

for (const width of [390, 1280]) {
  test(`retained alias questions expose scope, answers and per-question trust at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 })
    await setup(page, { alias: true })
    await page
      .getByRole("button", { name: "alias.corp.example · AAAA · completed", exact: true })
      .click()
    const chain = page.getByLabel("Native DNS question chain")
    await expect(chain).toContainText("failed discovery questions have no transport measurement")
    const questions = chain.getByRole("listitem")
    await expect(questions).toHaveCount(3)
    await expect(questions.nth(0)).toContainText("alias.corp.example · AAAA")
    await expect(questions.nth(0)).toContainText("Accepted records: None")
    await expect(questions.nth(0)).toContainText("Unknown")
    await expect(questions.nth(1)).toContainText("CNAME target → secret.corp.example")
    await expect(questions.nth(1)).toContainText("vpn0 · ~corp.example · DNS active")
    await expect(questions.nth(1)).toContainText("Native strict TLS policy")
    await expect(questions.nth(2)).toContainText("Accepted records: 2001:db8::7")
    await expect(questions.nth(2)).toContainText("Native reports DNSSEC validation")
    expect(await chain.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
  })
}

test("explicit launch sends only the question and expected native policy link", async ({
  page,
}) => {
  const calls = await setup(page)
  await page.getByLabel("Investigation name", { exact: true }).fill("secret.corp.example")
  await page.getByLabel("Record type", { exact: true }).click()
  await page.getByRole("option", { name: "AAAA", exact: true }).click()
  await page.getByLabel("Expected policy link (optional)", { exact: true }).fill("vpn0")
  await page.getByRole("button", { name: "Investigate policy", exact: true }).click()
  await expect
    .poll(() => calls)
    .toEqual([{ name: "secret.corp.example", type: "AAAA", expectedInterface: "vpn0" }])
  await expect(page.getByLabel("DNS policy evidence report")).toContainText("answer family inet6")
})

test("failed launch preserves dated selected evidence without an automatic retry", async ({
  page,
}) => {
  const calls = await setup(page, { failedLaunch: true })
  await page
    .getByRole("button", { name: "secret.corp.example · AAAA · completed", exact: true })
    .click()
  await expect(page.getByLabel("DNS policy evidence report")).toContainText("2001:db8::7")
  await page.getByLabel("Investigation name", { exact: true }).fill("other.corp.example")
  await page.getByRole("button", { name: "Investigate policy", exact: true }).click()
  await expect(
    page.getByRole("alert").filter({ hasText: "Investigation unavailable" }),
  ).toContainText("No automatic retry")
  await expect(page.getByLabel("DNS policy evidence report")).toContainText("2001:db8::7")
  expect(calls).toHaveLength(1)
})

test("interrupted saved questions have no fabricated trust and never rerun on read", async ({
  page,
}) => {
  const calls = await setup(page, { interrupted: true })
  await page
    .getByRole("button", { name: "secret.corp.example · AAAA · interrupted", exact: true })
    .click()
  const report = page.getByLabel("DNS policy evidence report")
  await expect(report).toContainText("An interrupted query is not rerun")
  await expect(report.getByText("Native reports encryption", { exact: true })).toHaveCount(0)
  expect(calls).toHaveLength(0)
})

test("reader sees the diagnostic boundary without private-history requests", async ({ page }) => {
  const requests: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("/dns/evidence")) requests.push(request.url())
  })
  await setup(page, { readonly: true })
  await expect(
    page.getByText(
      "Policy investigations and saved private answers require system administration access.",
    ),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Investigate policy", exact: true })).toHaveCount(0)
  expect(requests).toHaveLength(0)
})
