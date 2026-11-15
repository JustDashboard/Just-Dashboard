import { expect, test } from "bun:test"
import { firewallProduct, limitedKeys, ruleWords } from "./firewall-words"

test("a firewall is drawn as the packet filter it drives", () => {
  for (const backend of ["ufw", "firewalld", "iptables", "nftables"])
    expect(firewallProduct(backend)).toBe("netfilter")
  expect(firewallProduct("pf")).toBeUndefined()
  expect(firewallProduct(undefined)).toBeUndefined()
})

const rule = (fields) => ({
  action: "ALLOW",
  direction: "IN",
  to: "22/tcp",
  from: "Anywhere",
  port: "22",
  protocol: "tcp",
  raw: "",
  ...fields,
})

test("the limited ports are the inbound LIMIT rules, once each, without their IPv6 twins", () => {
  const keys = limitedKeys({
    rules: [
      rule({}),
      rule({ action: "LIMIT", port: "2222", to: "2222/tcp" }),
      rule({ action: "limit", port: "2222", to: "2222/tcp", from: "10.0.0.0/8" }),
      rule({ action: "LIMIT", port: "2222", to: "2222/tcp (v6)", ipv6: true }),
      rule({ action: "LIMIT", port: "", service: "OpenSSH", to: "OpenSSH" }),
      rule({ action: "LIMIT", port: "25", direction: "OUT" }),
    ],
  })
  expect([...keys]).toEqual(["2222", "OpenSSH"])
  expect(limitedKeys({ rules: [rule({})] }).size).toBe(0)
})

test("a recorded rule is said in the few words a line has room for", () => {
  expect(ruleWords({ action: "allow", port: "51820", protocol: "udp" })).toBe("allow 51820/udp")
  expect(ruleWords({ action: "allow", port: "8443", protocol: "tcp", from: "100.64.0.0/10" })).toBe(
    "allow 8443/tcp from 100.64.0.0/10",
  )
  // A source that is everyone is not a source worth the room.
  expect(ruleWords({ action: "deny", port: "23", from: "Anywhere" })).toBe("deny 23")
  expect(ruleWords({ action: "deny", to: "Anywhere", from: "203.0.113.9" })).toBe(
    "deny Anywhere from 203.0.113.9",
  )
  // Whatever the server recorded that is not a word is left out, not printed.
  expect(ruleWords({ action: "allow", port: 22 })).toBe("allow")
  expect(ruleWords(undefined)).toBe("")
})
