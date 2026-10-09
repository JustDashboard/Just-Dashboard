import { describe, expect, test } from "bun:test"
import {
  basisLabel,
  diagnosticCompatible,
  diagnosticDuration,
  diagnosticFinished,
  diagnosticNameProblem,
  diagnosticReading,
  diagnosticRetentionProblem,
  formatMetric,
  hasEvidence,
  historyKeys,
  historySeries,
  observedSSHKeys,
  resultReading,
  sshFingerprintProblem,
  sshTrustTarget,
  stageTone,
  wakeDeviceProblem,
} from "./network-diagnostics"

const run = {
  id: "one",
  request: { tool: "dns", target: "example.test", record: "AAAA" },
  status: "completed",
  hasResult: true,
}

describe("retained diagnostic readings", () => {
  test("compares only distinct terminal runs with the exact normalized request and evidence", () => {
    expect(diagnosticCompatible(run, { ...run, id: "two" })).toBe(true)
    expect(
      diagnosticCompatible(run, {
        ...run,
        id: "two",
        request: { ...run.request, port: 0, option: "" },
      }),
    ).toBe(true)
    for (const other of [
      run,
      { ...run, id: "two", status: "running" },
      { ...run, id: "two", hasResult: false },
      { ...run, id: "two", request: { ...run.request, record: "A" } },
      { ...run, id: "two", request: { ...run.request, target: "other.test" } },
    ])
      expect(diagnosticCompatible(run, other)).toBe(false)
    expect(diagnosticCompatible(run, { ...run, id: "two", status: "failed" })).toBe(true)
    expect(diagnosticFinished({ status: "cancelling" })).toBe(false)
    expect(diagnosticFinished({ status: "interrupted" })).toBe(true)
  })
  test("names and retention respect server byte, control and numeric bounds", () => {
    expect(diagnosticNameProblem(" resolver baseline ")).toBeUndefined()
    for (const name of [" ", "bad\nname", "界".repeat(34)])
      expect(diagnosticNameProblem(name)).toBeDefined()
    expect(diagnosticNameProblem("界".repeat(33))).toBeUndefined()
    expect(diagnosticRetentionProblem("1", "1")).toBeUndefined()
    expect(diagnosticRetentionProblem("256", "2160")).toBeUndefined()
    for (const [runs, hours] of [
      ["0", "1"],
      ["257", "1"],
      ["2.5", "1"],
      ["1", "2161"],
      ["1", "0"],
      ["1", "1e2"],
    ])
      expect(diagnosticRetentionProblem(runs, hours)).toBeDefined()
  })
  test("keeps failure classes, missing answers and pending cleanup distinct", () => {
    for (const [outcome, label] of Object.entries({
      timed_out: "Timed out",
      refused: "Connection refused",
      dns_failure: "DNS failure",
      permission_denied: "Permission denied",
      unsupported: "Unsupported on this host",
      invalid_certificate: "Invalid certificate",
      completed_with_findings: "Completed with findings",
    }))
      expect(diagnosticReading({ status: "failed", outcome }).label).toBe(label)
    expect(diagnosticReading({ status: "cancelling", outcome: "completed" }).label).toBe("Stopping")
    expect(diagnosticReading({ status: "failed", outcome: "future_category" }).label).toBe(
      "No answer recorded",
    )
    expect(diagnosticReading({ status: "completed" }).label).toBe("Completed")
  })
})

test("investigation comparison keeps the exact source tuple and operation", () => {
  const request = {
    sourceKind: "container",
    containerId: "a".repeat(64),
    sourceAddress: "2001:db8::2",
    target: "private.test",
    address: "2001:db8::8",
    family: "inet6",
    protocol: "tcp",
    port: 8443,
    mark: "",
    measure: true,
  }
  const path = {
    ...run,
    kind: "investigation",
    request: { tool: "", target: "" },
    investigationRequest: request,
  }
  expect(diagnosticCompatible(path, { ...path, id: "two" })).toBe(true)
  for (const change of [
    { sourceKind: "host", containerId: "" },
    { containerId: "b".repeat(64) },
    { sourceAddress: "2001:db8::3" },
    { address: "2001:db8::9" },
    { family: "inet" },
    { protocol: "udp" },
    { port: 443 },
    { mark: "0x1" },
    { measure: false },
  ]) {
    expect(
      diagnosticCompatible(path, {
        ...path,
        id: "two",
        investigationRequest: { ...request, ...change },
      }),
    ).toBe(false)
  }
  expect(diagnosticCompatible(path, { ...path, id: "two", investigationRequest: undefined })).toBe(
    false,
  )
  expect(diagnosticCompatible(path, { ...path, id: "two", kind: undefined })).toBe(false)
  expect(diagnosticReading({ status: "completed", outcome: "completed_with_unknowns" })).toEqual({
    label: "Report completed with unknowns",
    tone: "warning",
  })
})

test("report collection duration does not imply connection latency or invent absent timing", () => {
  expect(diagnosticDuration({ result: { duration: "12ms" } })).toBe("12ms")
  expect(diagnosticDuration({ kind: "investigation" })).toBe("Not recorded")
  const report = {
    kind: "investigation",
    investigation: {
      startedAt: "2026-10-08T12:00:00.000Z",
      endedAt: "2026-10-08T12:00:00.175Z",
    },
  }
  expect(diagnosticDuration(report)).toBe("175 ms to collect the report")
  for (const endedAt of ["unreadable", "2026-10-08T11:59:59.000Z"])
    expect(
      diagnosticDuration({ ...report, investigation: { ...report.investigation, endedAt } }),
    ).toBe("Not recorded")
})

describe("structured diagnostic evidence", () => {
  test("an unanswered probe reads as inconclusive, never as down", () => {
    expect(resultReading({ ok: false, verdict: "unknown" })).toEqual({
      label: "inconclusive",
      tone: "unknown",
    })
    expect(resultReading({ ok: true, verdict: "findings" }).tone).toBe("warning")
    expect(resultReading({ ok: false, verdict: "failed" }).tone).toBe("danger")
    expect(resultReading({ ok: true, verdict: "ok" }, "packet sent").label).toBe("packet sent")
    expect(resultReading({ ok: true }).label).toBe("answered")
    expect(resultReading({ ok: false }).label).toBe("no answer")
    expect(stageTone("skipped")).toBe("stopped")
    expect(stageTone("failed")).toBe("danger")
    expect(basisLabel("self_reported")).toBe("self-reported")
  })

  test("metrics format exactly and history keeps gaps instead of inventing values", () => {
    expect(formatMetric({ value: 25, unit: "%" })).toBe("25%")
    expect(formatMetric({ value: 2.33333, unit: "ms" })).toBe("2.333 ms")
    expect(formatMetric({ value: 4 })).toBe("4")
    const history = {
      request: { tool: "ping", target: "192.0.2.9" },
      limitations: [],
      points: [
        {
          id: "a",
          name: "p",
          createdAt: "2026-10-09T10:00:00Z",
          status: "completed",
          metrics: [{ key: "loss", label: "Packet loss", value: 0, unit: "%" }],
        },
        { id: "b", name: "p", createdAt: "2026-10-09T11:00:00Z", status: "failed", metrics: [] },
        {
          id: "c",
          name: "p",
          createdAt: "2026-10-09T12:00:00Z",
          status: "completed",
          metrics: [
            { key: "loss", label: "Packet loss", value: 25, unit: "%" },
            { key: "jitter", label: "Jitter", value: 1.5, unit: "ms" },
          ],
        },
      ],
    }
    expect(historySeries(history, "loss").map((point) => point.value)).toEqual([0, null, 25])
    expect(historyKeys(history).map((key) => key.key)).toEqual(["loss", "jitter"])
    expect(hasEvidence({ ok: true, records: ["x"] })).toBe(false)
    expect(hasEvidence({ ok: true, stages: [{ id: "a", label: "A", status: "passed" }] })).toBe(
      true,
    )
  })

  test("SSH trust reads only well-formed scan records and canonical targets", () => {
    const fp = "SHA256:" + "A".repeat(43)
    expect(
      observedSSHKeys({
        records: [`ssh-ed25519 ${fp}`, "ssh-rsa not-a-fingerprint", `rot13 ${fp}`],
      }),
    ).toEqual([{ type: "ssh-ed25519", fingerprint: fp }])
    expect(sshFingerprintProblem(fp)).toBeUndefined()
    expect(sshFingerprintProblem("MD5:aa:bb")).toBeDefined()
    expect(sshTrustTarget("Host.Example.test.", 0)).toBe("host.example.test:22")
    expect(sshTrustTarget("2001:db8::1", 2222)).toBe("[2001:db8::1]:2222")
  })

  test("saved Wake-on-LAN devices refuse unwakeable targets before saving", () => {
    expect(
      wakeDeviceProblem({ name: "NAS", mac: "02:11:22:33:44:55", interface: "eno1" }),
    ).toBeUndefined()
    expect(
      wakeDeviceProblem({ name: " ", mac: "02:11:22:33:44:55", interface: "eno1" }),
    ).toBeDefined()
    expect(
      wakeDeviceProblem({ name: "NAS", mac: "ff:ff:ff:ff:ff:ff", interface: "eno1" }),
    ).toContain("broadcast")
    expect(
      wakeDeviceProblem({ name: "NAS", mac: "02:11:22:33:44:55", interface: "a/b" }),
    ).toBeDefined()
  })

  test("compatibility includes the wake verification target", () => {
    const wake = {
      ...run,
      request: { tool: "wol", target: "02:11:22:33:44:55", option: "eno1", verify: "192.168.1.5" },
    }
    expect(diagnosticCompatible(wake, { ...wake, id: "two" })).toBe(true)
    expect(
      diagnosticCompatible(wake, {
        ...wake,
        id: "two",
        request: { ...wake.request, verify: "192.168.1.6" },
      }),
    ).toBe(false)
  })
})
