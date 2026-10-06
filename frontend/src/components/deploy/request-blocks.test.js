import { describe, expect, test } from "bun:test"
import { blockUnavailable, hasSourceDeny } from "./request-blocks"

const rule = {
  action: "DENY",
  direction: "IN",
  from: "203.0.113.55",
  to: "Anywhere",
  raw: "",
}
const firewall = {
  available: true,
  enabled: true,
  capabilities: { editable: true },
  rules: [rule],
}

describe("saved request source denies", () => {
  test("recognizes host and CIDR denies with the firewall's IPv4 and IPv6 spellings", () => {
    expect(hasSourceDeny(firewall, "203.0.113.55")).toBe(true)
    expect(hasSourceDeny(firewall, "203.0.113.56")).toBe(false)
    const listed = (changes) => ({ ...firewall, rules: [{ ...rule, ...changes }] })
    expect(
      hasSourceDeny(listed({ from: "203.0.113.0/24", action: "REJECT" }), "203.0.113.56"),
    ).toBe(true)
    expect(
      hasSourceDeny(
        listed({ from: "2001:db8::5/128", to: "Anywhere (v6)", direction: "" }),
        "2001:0db8:0:0:0:0:0:5",
      ),
    ).toBe(true)
    expect(hasSourceDeny(listed({ from: "2001:db8::/32", to: "any" }), "2001:db8:1::1")).toBe(true)
  })

  test("port, protocol, destination and other directions cannot stand for a source deny", () => {
    for (const changes of [
      { action: "ALLOW" },
      { direction: "OUT" },
      { direction: "FWD" },
      { port: "22" },
      { protocol: "tcp" },
      { to: "OpenSSH" },
      { to: "10.0.0.1" },
      { from: "203.0.113.55 22" },
    ]) {
      expect(hasSourceDeny({ ...firewall, rules: [{ ...rule, ...changes }] }, "203.0.113.55")).toBe(
        false,
      )
    }
    expect(hasSourceDeny(undefined, "203.0.113.55")).toBe(false)
  })

  test("does not offer a write without a readable, enabled, editable firewall", () => {
    expect(blockUnavailable(firewall)).toBe("")
    expect(blockUnavailable(undefined)).toContain("Checking")
    expect(blockUnavailable(firewall, new Error("read failed"))).toContain("could not be read")
    expect(blockUnavailable({ ...firewall, error: "Permission denied" })).toBe("Permission denied")
    expect(blockUnavailable({ ...firewall, available: false })).toContain("No supported")
    expect(blockUnavailable({ ...firewall, enabled: false })).toContain("disabled")
    expect(
      blockUnavailable({
        ...firewall,
        capabilities: { editable: false, readOnlyReason: "iptables is read-only" },
      }),
    ).toBe("iptables is read-only")
  })
})
