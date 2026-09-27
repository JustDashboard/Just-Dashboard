import { describe, expect, test } from "bun:test"
import { portFindings } from "./findings/ports"
import {
  dangerousPorts,
  foldDualStack,
  internetHint,
  privateHint,
  reachGroup,
  reachVerdict,
  reachWords,
  socketAddresses,
  tallyReach,
} from "./ports"

const listener = (overrides) => ({
  protocol: "tcp",
  family: "ipv4",
  address: "0.0.0.0",
  port: 443,
  pid: 812,
  process: "nginx",
  scope: "all",
  reach: "all",
  network: "all",
  exposed: true,
  ...overrides,
})

// Sockets as this host lists them: sshd on every interface, caddy on the
// tailnet address, the DNS stub on loopback.
const sshd = listener({ port: 22, process: "sshd" })
const sshd6 = { ...sshd, family: "ipv6", address: "::" }
const caddy = listener({
  address: "100.110.34.31",
  port: 8443,
  process: "caddy",
  scope: "interface",
  reach: "network",
  network: "tailnet",
  interface: "tailscale0",
})
const resolved = listener({
  protocol: "udp",
  address: "127.0.0.53",
  port: 53,
  process: "systemd-resolve",
  scope: "loopback",
  reach: "loopback",
  network: "loopback",
  exposed: false,
})
const dhcp = listener({
  protocol: "udp",
  address: "57.131.21.87",
  port: 68,
  process: "systemd-network",
  scope: "interface",
  reach: "public",
  network: "public",
  interface: "ens3",
})

// Redis for the containers on two Docker bridges, and MongoDB on docker0's
// link-local address: two databases on three sockets.
const redisBridge = listener({
  address: "10.0.0.1",
  port: 6379,
  process: "redis-server",
  scope: "interface",
  reach: "host",
  network: "docker",
  interface: "docker0",
})
const redisOtherBridge = { ...redisBridge, address: "10.0.2.1", interface: "br-b05f8e098ad7" }
const mongoLinkLocal = listener({
  family: "ipv6",
  address: "fe80::b482:4dff:fe92:4281",
  port: 27017,
  process: "mongod",
  scope: "interface",
  reach: "host",
  network: "docker",
  interface: "docker0",
})

describe("where a socket answers", () => {
  test("each network reads as who can connect, with its interface", () => {
    expect(reachWords(sshd)).toBe("Every interface")
    expect(reachWords(resolved)).toBe("This server only")
    expect(reachWords(caddy)).toBe("Tailnet only · tailscale0")
    expect(reachWords(dhcp)).toBe("Public address · ens3")
    expect(reachWords(redisBridge)).toBe("Docker bridge · docker0")
    expect(reachWords({ network: "private", interface: "ens3" })).toBe("Private network · ens3")
    expect(reachWords({ network: "vpn", interface: "wg0" })).toBe("VPN only · wg0")
    expect(reachWords({ network: "bridge", interface: "virbr0" })).toBe("Bridge · virbr0")
    expect(reachWords({ network: "link-local", interface: "ens3" })).toBe("Link-local · ens3")
    // An address on no interface the host listed is named by its network alone.
    expect(reachWords({ network: "public" })).toBe("Public address")
  })

  test("the chips split the sockets three ways, a public IP with the internet", () => {
    expect(reachGroup(sshd)).toBe("internet")
    expect(reachGroup(dhcp)).toBe("internet")
    expect(reachGroup(caddy)).toBe("private")
    expect(reachGroup(redisBridge)).toBe("private")
    expect(reachGroup(resolved)).toBe("local")
  })

  test("a service in both families is one row, its twin kept", () => {
    const folded = foldDualStack([sshd, sshd6, caddy, resolved])
    expect(folded).toHaveLength(3)
    expect(socketAddresses(folded[0])).toEqual(["0.0.0.0", "::"])
    expect(socketAddresses(folded[1])).toEqual(["100.110.34.31"])
    // The listing it was given is left alone.
    expect(sshd.twin).toBeUndefined()
  })

  test("only one owner's sockets on one network fold", () => {
    const loopback = { scope: "loopback", reach: "loopback", network: "loopback", exposed: false }
    const node4 = listener({ ...loopback, address: "127.0.0.1", port: 3000, pid: 1400 })
    const node6 = { ...node4, family: "ipv6", address: "::1" }
    const other6 = { ...node6, pid: 1500 }
    const udp6 = { ...sshd6, protocol: "udp" }
    const tailnet6 = { ...caddy, family: "ipv6", address: "fd7a:115c:a1e0::9e37:2220" }
    const redis6 = { ...redisOtherBridge, family: "ipv6", address: "fe80::5c5d:77ff:fe51:d2be" }
    // Two IPv4 sockets on one network are two binds, not a pair.
    const caddyAgain = { ...caddy, address: "100.110.34.32" }

    expect(foldDualStack([node4, node6]).map(socketAddresses)).toEqual([["127.0.0.1", "::1"]])
    expect(foldDualStack([node4, other6])).toHaveLength(2)
    expect(foldDualStack([sshd, udp6])).toHaveLength(2)
    expect(foldDualStack([caddy, tailnet6]).map(socketAddresses)).toEqual([
      ["100.110.34.31", "fd7a:115c:a1e0::9e37:2220"],
    ])
    expect(foldDualStack([redisBridge, redis6])).toHaveLength(2)
    expect(foldDualStack([caddy, caddyAgain, tailnet6]).map(socketAddresses)).toEqual([
      ["100.110.34.31", "fd7a:115c:a1e0::9e37:2220"],
      ["100.110.34.32"],
    ])
  })

  test("the counts are services, a dual-stack pair once", () => {
    expect(tallyReach(foldDualStack([sshd, sshd6, caddy, dhcp, resolved, redisBridge]))).toEqual({
      all: 5,
      internet: 2,
      private: 2,
      local: 1,
      tcp: 3,
      udp: 2,
    })
  })

  test("the Internet-facing tile says what its count is made of, in half a phone", () => {
    expect(internetHint([])).toBe("nothing the internet can reach")
    expect(internetHint([resolved, caddy])).toBe("nothing the internet can reach")
    expect(internetHint([sshd, resolved])).toBe("on every interface")
    expect(internetHint([dhcp])).toBe("on a public address")
    expect(internetHint([dhcp, { ...dhcp, address: "2001:41d0:2005:100::13" }])).toBe(
      "on public addresses",
    )
    expect(internetHint([sshd, dhcp, caddy])).toBe("1 on all · 1 on a public IP")
    const many = [
      ...Array.from({ length: 19 }, () => sshd),
      ...Array.from({ length: 18 }, () => dhcp),
    ]
    expect(internetHint(many)).toBe("19 on all · 18 on a public IP")
    expect(internetHint(many).length).toBeLessThanOrEqual(30)
  })

  test("the Private networks tile names its networks, most sockets first", () => {
    expect(privateHint([sshd, resolved])).toBe("tailnet, VPN, LAN or bridge")
    expect(privateHint([caddy])).toBe("tailnet")
    expect(privateHint([redisBridge, redisOtherBridge, caddy])).toBe("Docker · tailnet")
    const wg = { ...caddy, network: "vpn", interface: "wg0" }
    const lan = { ...caddy, network: "private", interface: "ens3" }
    expect(privateHint([redisBridge, redisOtherBridge, caddy, wg, lan])).toBe("Docker · tailnet +2")
    expect(privateHint([redisBridge, redisOtherBridge, caddy, wg, lan]).length).toBeLessThanOrEqual(
      30,
    )
  })

  test("a socket is coloured by who can connect and by what answers", () => {
    const redisOn = (reach, scope = "interface") =>
      reachVerdict(listener({ port: 6379, scope, reach, address: "10.0.0.1" }))
    expect(redisOn("all", "all")).toBe("critical")
    expect(redisOn("public")).toBe("critical")
    expect(redisOn("network")).toBe("warning")
    expect(redisOn("host")).toBe("warning")
    // sshd the internet can reach is worth seeing; caddy on the tailnet is not an alarm.
    expect(reachVerdict(sshd)).toBe("warning")
    expect(reachVerdict(dhcp)).toBe("warning")
    expect(reachVerdict(caddy)).toBe("notice")
    expect(reachVerdict(resolved)).toBeUndefined()
  })
})

describe("databases off this machine are counted per port", () => {
  test("one database bound to two addresses is one", () => {
    const ports = dangerousPorts([redisBridge, redisOtherBridge, mongoLinkLocal, sshd, resolved])
    expect(ports.map((p) => `${p.service} ${p.sockets.length}`)).toEqual(["Redis 2", "MongoDB 1"])
    expect(ports.some((p) => p.internet)).toBe(false)
  })

  test("a dual-stack bind is one database, and faces the internet", () => {
    const ports = dangerousPorts([
      listener({ port: 5432, process: "postgres" }),
      listener({ port: 5432, process: "postgres", address: "::" }),
      redisBridge,
    ])
    expect(ports).toHaveLength(2)
    expect(ports[0]).toMatchObject({ service: "PostgreSQL", internet: true })
    expect(ports[1]).toMatchObject({ service: "Redis", internet: false })
  })

  test("a TCP and a UDP socket on one number are two ports", () => {
    expect(
      dangerousPorts([listener({ port: 11211 }), listener({ port: 11211, protocol: "udp" })]),
    ).toHaveLength(2)
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
          reach: "public",
          network: "public",
          interface: "ens3",
        }),
      ],
    })
    // Critical, as the posture calls a database the internet can reach.
    expect(finding.level).toBe("critical")
    expect(finding.title).toBe("Redis answers on 203.0.113.5")
    expect(finding.detail).toBe("6379/tcp redis-server on 203.0.113.5 (Public address · ens3)")
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
          reach: "network",
          network: "tailnet",
        }),
      ],
    })
    expect(finding.level).toBe("critical")
    expect(finding.title).toBe("2 database or control ports answer off this machine")
    expect(finding.detail).toBe(
      "5432/tcp postgres, 6379/tcp redis-server on 100.64.1.2 (Tailnet only)",
    )
  })

  test("on every interface reads as it always did", () => {
    const [finding] = portFindings({ ports: [listener({ port: 5432, process: "postgres" })] })
    expect(finding.level).toBe("critical")
    expect(finding.title).toBe("PostgreSQL answers on every interface")
    expect(finding.detail).toBe("5432/tcp postgres")
    expect(finding.advice).toStartWith("Bind these to loopback or a private address")
  })

  test("two databases on three sockets are two, each named once with every address", () => {
    const [finding] = portFindings({ ports: [redisBridge, redisOtherBridge, mongoLinkLocal] })
    // Only this host's containers can reach them: a warning, as the posture says.
    expect(finding.level).toBe("warning")
    expect(finding.title).toBe("2 database or control ports answer off this machine")
    expect(finding.detail).toBe(
      "6379/tcp redis-server on 10.0.0.1 (Docker bridge · docker0), 10.0.2.1 (Docker bridge · br-b05f8e098ad7), 27017/tcp mongod on fe80::b482:4dff:fe92:4281 (Docker bridge · docker0)",
    )
  })

  test("one database on two addresses is named once, at both", () => {
    const [finding] = portFindings({ ports: [redisBridge, redisOtherBridge] })
    expect(finding.title).toBe("Redis answers on 2 addresses")
    expect(finding.detail).toBe(
      "6379/tcp redis-server on 10.0.0.1 (Docker bridge · docker0), 10.0.2.1 (Docker bridge · br-b05f8e098ad7)",
    )
  })

  test("one database on a tailnet's two addresses names the tailnet once", () => {
    const redis = listener({
      address: "100.110.34.31",
      port: 6379,
      process: "redis-server",
      scope: "interface",
      reach: "network",
      network: "tailnet",
      interface: "tailscale0",
    })
    const [finding] = portFindings({
      ports: [redis, { ...redis, family: "ipv6", address: "fd7a:115c:a1e0::9e37:2220" }],
    })
    expect(finding.level).toBe("warning")
    expect(finding.detail).toBe(
      "6379/tcp redis-server on 100.110.34.31 and fd7a:115c:a1e0::9e37:2220 (Tailnet only · tailscale0)",
    )
  })

  test("a dual-stack database is one, on every interface", () => {
    const [finding] = portFindings({
      ports: [
        listener({ port: 5432, process: "postgres" }),
        listener({ port: 5432, process: "postgres", address: "::" }),
      ],
    })
    expect(finding.title).toBe("PostgreSQL answers on every interface")
    expect(finding.detail).toBe("5432/tcp postgres")
  })

  test("on loopback is not a finding", () => {
    expect(
      portFindings({
        ports: [listener({ address: "127.0.0.1", port: 5432, scope: "loopback", exposed: false })],
      }),
    ).toEqual([])
  })
})
