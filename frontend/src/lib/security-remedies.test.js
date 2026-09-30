import { expect, test } from "bun:test"
import { securityRemedy } from "./security-remedies"

test("security remedies gate mutations and every finding area has a real owner page", () => {
  const finding = { area: "ssh", fix: "ssh.permitemptypasswords=no", fixLabel: "Turn off" }
  expect(securityRemedy(finding, () => false)).toEqual({
    href: "/security/ssh",
    label: "Review SSH controls",
    apply: false,
  })
  expect(securityRemedy(finding, () => true).apply).toBe(true)
  for (const area of ["exposure", "firewall", "ssh", "intrusion", "ports", "tls", "updates"])
    expect(securityRemedy({ area }, () => true).href).toBeTruthy()
  expect(securityRemedy({ area: "ports" }, () => true).href).toBe("/proxy/ports")
  expect(securityRemedy({ area: "exposure" }, () => true).href).toBe("/dashboard/configuration")
})
