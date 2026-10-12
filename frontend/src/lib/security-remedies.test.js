import { expect, test } from "bun:test"
import { needsSecurityAttention, securityRemedy } from "./security-remedies"

test("security remedies gate mutations and every finding area has a real owner page", () => {
  const finding = { area: "ssh", fix: "ssh.permitemptypasswords=no", fixLabel: "Turn off" }
  expect(securityRemedy(finding, () => false)).toEqual({
    label: "An administrator can review the SSH settings",
    apply: false,
  })
  expect(securityRemedy(finding, () => true)).toEqual({
    href: "/security/ssh?setting=permitemptypasswords#ssh-permitemptypasswords",
    label: "Review empty SSH passwords",
    apply: false,
  })
  for (const area of ["exposure", "firewall", "ssh", "intrusion", "ports", "tls", "updates"])
    expect(securityRemedy({ area }, () => true).href).toBeTruthy()
  expect(securityRemedy({ area: "ports" }, () => true).href).toBe("/proxy/ports")
  expect(securityRemedy({ area: "exposure" }, () => true).href).toBe(
    "/dashboard/configuration#allowlist",
  )
})

test("informational preferences and unknown fix codes do not become broken actions", () => {
  expect(needsSecurityAttention({ level: "notice" })).toBe(false)
  expect(needsSecurityAttention({ level: "critical" })).toBe(true)
  expect(securityRemedy({ area: "ssh", fix: "not-a-supported-fix" }, () => true).apply).toBe(false)
  expect(securityRemedy({ area: "exposure" }, () => false).href).toBeUndefined()
  expect(
    securityRemedy({ area: "ssh", fix: "ssh.permitemptypasswords=no" }, () => true).label,
  ).toBe("Review empty SSH passwords")
})
