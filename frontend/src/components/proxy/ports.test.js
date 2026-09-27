import { describe, expect, test } from "bun:test"
import { portFindings } from "./findings/ports"
import { exposedHint, reachWhere } from "./ports"

const listener = (overrides) => ({
  protocol: "tcp",
  address: "0.0.0.0",
  port: 443,
  pid: 812,
  process: "nginx",
  scope: "all",
  exposed: true,
  ...overrides,
})

// Sockets as this host lists them: sshd on every interface, caddy on the
// tailnet address, the DNS stub on loopback.
const sshd = listener({ port: 22, process: "sshd" })
const caddy = listener({
  address: "100.110.34.31",
  port: 8443,
  process: "caddy",
  scope: "interface",
})
const resolved = listener({
  protocol: "udp",
  address: "127.0.0.53",
  port: 53,
  process: "systemd-resolve",
  scope: "loopback",
  exposed: false,
})

describe("where a socket answers", () => {
  test("a socket on one address answers there, not on every interface and not on loopback", () => {
    expect(reachWhere(sshd)).toBe("every interface")
    expect(reachWhere(caddy)).toBe("100.110.34.31")
    expect(reachWhere(resolved)).toBe("loopback")
  })

  test("the Exposed tile says which kind of exposure it counts", () => {
    expect(exposedHint([])).toBe("nothing off the machine")
    expect(exposedHint([resolved])).toBe("nothing off the machine")
    expect(exposedHint([sshd, resolved])).toBe("bound to every interface")
    expect(exposedHint([caddy])).toBe("bound to one address")
    expect(exposedHint([caddy, listener({ scope: "interface", address: "10.0.0.1" })])).toBe(
      "each bound to one address",
    )
    expect(exposedHint([sshd, caddy, resolved])).toBe("1 on every interface · 1 on one address")
  })
})

describe("a database off this machine", () => {
  test("on a public address is named at that address, not 'every interface'", () => {
    const [finding] = portFindings({
      ports: [
        listener({
          address: "203.0.113.5",
          port: 6379,
          process: "redis-server",
          scope: "interface",
        }),
      ],
    })
    expect(finding.title).toBe("Redis answers on 203.0.113.5")
    expect(finding.detail).toBe("6379/tcp redis-server on 203.0.113.5")
    expect(finding.advice).toStartWith(
      "Bind these to 127.0.0.1 unless something on the same network",
    )
  })

  test("several, some on one address, answer off this machine", () => {
    const [finding] = portFindings({
      ports: [
        listener({ port: 5432, process: "postgres" }),
        listener({
          address: "100.64.1.2",
          port: 6379,
          process: "redis-server",
          scope: "interface",
        }),
      ],
    })
    expect(finding.title).toBe("2 database or control ports answer off this machine")
    expect(finding.detail).toBe("5432/tcp postgres, 6379/tcp redis-server on 100.64.1.2")
  })

  test("on every interface reads as it always did", () => {
    const [finding] = portFindings({ ports: [listener({ port: 5432, process: "postgres" })] })
    expect(finding.title).toBe("PostgreSQL answers on every interface")
    expect(finding.detail).toBe("5432/tcp postgres")
    expect(finding.advice).toStartWith("Bind these to loopback or a private address")
  })

  test("on loopback is not a finding", () => {
    expect(
      portFindings({
        ports: [listener({ address: "127.0.0.1", port: 5432, scope: "loopback", exposed: false })],
      }),
    ).toEqual([])
  })
})
