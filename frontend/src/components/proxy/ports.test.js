import { describe, expect, test } from "bun:test"
import { portFindings } from "./findings/ports"
import {
  dangerousPorts,
  dangerousService,
  foldDualStack,
  internetHint,
  pastFirewallWords,
  privateHint,
  privateNetworksHint,
  reachGroup,
  reachVerdict,
  reachWords,
  socketAddresses,
  socketPids,
  tallyReach,
  withoutAddresses,
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
  level: "warning",
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
  level: "warning",
})

/**
 * Docker's ingress as this host publishes it: one docker-proxy per family for
 * each of 80 and 443, four processes for two services, children of dockerd.
 */
const dockerProxy = (port, family, hostIp, pid, containerIp = "10.0.0.3") =>
  listener({
    family,
    address: hostIp,
    port,
    pid,
    ppid: 1755428,
    process: "docker-proxy",
    user: "root",
    cmdline: `/usr/bin/docker-proxy -proto tcp -host-ip ${hostIp} -host-port ${port} -container-ip ${containerIp} -container-port ${port} -use-listen-fd`,
  })
const ingress = [
  dockerProxy(80, "ipv4", "0.0.0.0", 1883643),
  dockerProxy(80, "ipv6", "::", 1883650),
  dockerProxy(443, "ipv4", "0.0.0.0", 1883666),
  dockerProxy(443, "ipv6", "::", 1883672),
]

describe("where a socket answers", () => {
  test("each network reads as who can connect, with its interface", () => {
    expect(reachWords(sshd)).toBe("Every interface")
    expect(reachWords(resolved)).toBe("This server only")
    expect(reachWords(caddy)).toBe("Tailnet only · tailscale0")
    expect(reachWords(dhcp)).toBe("Public address · ens3")
    expect(reachWords(redisBridge)).toBe("Docker bridge · docker0")
    expect(reachWords({ network: "private", interface: "ens4" })).toBe("Private network · ens4")
    expect(reachWords({ network: "uplink", interface: "eth0" })).toBe("Private uplink · eth0")
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

  test("only one service's sockets on one network fold", () => {
    const loopback = { scope: "loopback", reach: "loopback", network: "loopback", exposed: false }
    const node4 = listener({
      ...loopback,
      address: "127.0.0.1",
      port: 3000,
      pid: 1400,
      process: "node",
      user: "app",
      cmdline: "node server.js",
    })
    const node6 = { ...node4, family: "ipv6", address: "::1" }
    // Another program, or the same one run by another account, on the other family.
    const other6 = { ...node6, pid: 1500, process: "python3", cmdline: "python3 -m http.server" }
    const otherUser6 = { ...node6, pid: 1500, user: "deploy" }
    const udp6 = { ...sshd6, protocol: "udp" }
    const tailnet6 = { ...caddy, family: "ipv6", address: "fd7a:115c:a1e0::9e37:2220" }
    const redis6 = { ...redisOtherBridge, family: "ipv6", address: "fe80::5c5d:77ff:fe51:d2be" }
    // Two IPv4 sockets on one network are two binds, not a pair.
    const caddyAgain = { ...caddy, address: "100.110.34.32" }

    expect(foldDualStack([node4, node6]).map(socketAddresses)).toEqual([["127.0.0.1", "::1"]])
    expect(foldDualStack([node4, other6])).toHaveLength(2)
    expect(foldDualStack([node4, otherUser6])).toHaveLength(2)
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

  test("a port Docker publishes in both families is one service, though two processes hold it", () => {
    const folded = foldDualStack([...ingress, sshd, sshd6])
    expect(folded.map((s) => `${s.port} ${socketAddresses(s).join(", ")}`)).toEqual([
      "80 0.0.0.0, ::",
      "443 0.0.0.0, ::",
      "22 0.0.0.0, ::",
    ])
    expect(tallyReach(folded)).toMatchObject({ all: 3, internet: 3 })
    // Both processes stay named, for the row to show and the Process verb to open.
    expect(socketPids(folded[0])).toEqual([1883643, 1883650])
    expect(socketPids(folded[2])).toEqual([812])

    // On a dual-stack network each proxy is also told its own container
    // address; started apart from dockerd, the command lines still agree.
    expect(
      foldDualStack([
        { ...dockerProxy(8080, "ipv4", "0.0.0.0", 2001, "172.18.0.2"), ppid: undefined },
        { ...dockerProxy(8080, "ipv6", "::", 2002, "fd00:dead:beef::2"), ppid: undefined },
      ]),
    ).toHaveLength(1)
  })

  test("two processes of one program are one service when one parent started them", () => {
    const node = (family, address, pid, ppid, script) =>
      listener({
        family,
        address,
        port: 37965,
        pid,
        ppid,
        process: "node",
        user: "ubuntu",
        scope: "loopback",
        reach: "loopback",
        network: "loopback",
        exposed: false,
        cmdline: `node ${script}`,
      })
    // Started together, as a supervisor starts one per family.
    expect(
      foldDualStack([
        node("ipv4", "127.0.0.1", 3053394, 3053300, "listen.js --port 0"),
        node("ipv6", "::1", 3053404, 3053300, "listen.js --port 37965"),
      ]),
    ).toHaveLength(1)
    // Started apart, and differently: two services on one number.
    expect(
      foldDualStack([
        node("ipv4", "127.0.0.1", 3053394, 3053300, "api.js"),
        node("ipv6", "::1", 3053404, 4100, "admin.js"),
      ]),
    ).toHaveLength(2)
    // Init is everybody's parent, and says nothing.
    expect(
      foldDualStack([
        node("ipv4", "127.0.0.1", 3053394, 1, "api.js"),
        node("ipv6", "::1", 3053404, 1, "admin.js"),
      ]),
    ).toHaveLength(2)
  })

  test("a command line is compared without its addresses", () => {
    expect(
      withoutAddresses(
        "/usr/bin/docker-proxy -proto tcp -host-ip :: -host-port 80 -container-ip fd00:dead:beef::2 -container-port 80",
      ),
    ).toBe(
      "/usr/bin/docker-proxy -proto tcp -host-ip <address> -host-port 80 -container-ip <address> -container-port 80",
    )
    expect(withoutAddresses("/usr/bin/dockerd -H tcp://172.31.5.9:2375")).toBe(
      "/usr/bin/dockerd -H tcp://<address>:2375",
    )
    expect(
      withoutAddresses("app --listen=[::1]:8080 --peer fe80::b482:4dff:fe92:4281%docker0"),
    ).toBe("app --listen=<address>:8080 --peer <address>")
    // What is not an address is left alone.
    expect(withoutAddresses("sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups")).toBe(
      "sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups",
    )
    expect(withoutAddresses("node_exporter --web.listen-address=:9100 v1.8.2")).toBe(
      "node_exporter --web.listen-address=:9100 v1.8.2",
    )
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

  test("a private address on the uplink is not said to be beyond the internet", () => {
    const uplink = listener({
      address: "172.31.5.9",
      port: 8080,
      process: "gunicorn",
      scope: "interface",
      reach: "network",
      network: "uplink",
      interface: "eth0",
    })
    // Not counted as the internet's, which it may not be...
    expect(reachGroup(uplink)).toBe("private")
    expect(privateHint([uplink, caddy])).toBe("uplink · tailnet")
    // ...but not called out of its reach either, and drawn as a socket the
    // internet may reach rather than a notice.
    expect(internetHint([uplink, caddy, resolved])).toBe("+1 if the uplink is mapped")
    expect(internetHint(Array.from({ length: 12 }, () => uplink)).length).toBeLessThanOrEqual(30)
    expect(internetHint([sshd, uplink])).toBe("on every interface")
    expect(reachVerdict(uplink)).toBe("warning")
    // A LAN on a second NIC is a private network like any other.
    expect(reachVerdict({ ...uplink, network: "private", interface: "eth1" })).toBe("notice")
  })

  test("the overview's hint counts what the ports page's Private networks tile counts", () => {
    const tally = tallyReach(foldDualStack([sshd, sshd6, caddy, redisBridge, resolved]))
    expect(privateNetworksHint(tally)).toBe("2 on private networks")
    expect(privateNetworksHint(tallyReach(foldDualStack([sshd, resolved])))).toBe(
      "none on private networks",
    )
    expect(privateNetworksHint(tallyReach([resolved]))).toBe("everything on this server")
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
    // A database takes the posture's level, which GET /ports carries.
    const redisAt = (level) => reachVerdict(listener({ port: 6379, level }))
    expect(redisAt("critical")).toBe("critical")
    expect(redisAt("warning")).toBe("warning")
    // Redis on every interface behind a firewall denying inbound is the
    // posture's warning, not the red its reach alone would say.
    expect(reachVerdict(listener({ port: 6379, level: "warning", inboundDefault: "deny" }))).toBe(
      "warning",
    )
    // A socket on a database's number the posture raises nothing for (Redis
    // is TCP) is drawn by its reach, and named no database.
    const udp = listener({ protocol: "udp", port: 6379 })
    expect(reachVerdict(udp)).toBe("warning")
    expect(dangerousService(udp)).toBeUndefined()
    expect(dangerousService(redisBridge)).toBe("Redis")
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
    expect(ports.every((p) => p.level === "warning")).toBe(true)
  })

  test("a dual-stack bind is one database, at its highest level", () => {
    const ports = dangerousPorts([
      listener({ port: 5432, process: "postgres", level: "critical" }),
      listener({ port: 5432, process: "postgres", address: "::", level: "critical" }),
      listener({ port: 6379, address: "10.0.0.1", level: "warning" }),
      listener({ port: 6379, address: "57.131.21.87", level: "critical" }),
      redisBridge,
    ])
    expect(ports).toHaveLength(2)
    expect(ports[0]).toMatchObject({ service: "PostgreSQL", level: "critical" })
    expect(ports[1]).toMatchObject({ service: "Redis", level: "critical" })
    expect(ports[1].sockets).toHaveLength(3)
  })

  test("a socket the posture does not level is no database", () => {
    // memcached's TCP port is the catalogue's; a UDP socket on its number is not.
    expect(
      dangerousPorts([
        listener({ port: 11211, level: "critical" }),
        listener({ port: 11211, protocol: "udp" }),
      ]),
    ).toHaveLength(1)
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
          level: "critical",
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
        listener({ port: 5432, process: "postgres", level: "critical" }),
        listener({
          address: "100.64.1.2",
          port: 6379,
          process: "redis-server",
          scope: "interface",
          reach: "network",
          network: "tailnet",
          level: "warning",
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
    const [finding] = portFindings({
      ports: [listener({ port: 5432, process: "postgres", level: "critical" })],
    })
    expect(finding.level).toBe("critical")
    expect(finding.title).toBe("PostgreSQL answers on every interface")
    expect(finding.detail).toBe("5432/tcp postgres")
    expect(finding.advice).toStartWith("Bind these to loopback or a private address")
  })

  test("behind a firewall denying inbound is the posture's warning, and says why", () => {
    const behind = { level: "warning", inboundDefault: "deny" }
    const [finding] = portFindings({
      ports: [
        listener({ port: 6379, process: "redis-server", ...behind }),
        listener({ port: 6379, process: "redis-server", address: "::", ...behind }),
        listener({
          address: "57.131.21.87",
          port: 5432,
          process: "postgres",
          scope: "interface",
          reach: "public",
          network: "public",
          interface: "ens3",
          ...behind,
        }),
      ],
    })
    expect(finding.level).toBe("warning")
    expect(finding.title).toBe("2 database or control ports answer off this machine")
    expect(finding.detail).toBe(
      "6379/tcp redis-server, 5432/tcp postgres on 57.131.21.87 (Public address · ens3), though the firewall's inbound default is deny",
    )
  })

  test("one Docker publishes behind a firewall denying inbound stays critical, and says why", () => {
    // Docker forwards a port it publishes before ufw's inbound default is
    // met, so the posture does not credit the default and neither does the page.
    const docker = { process: "docker-proxy", level: "critical", pastFirewall: "docker" }
    const sockets = [
      listener({ port: 5432, pid: 1883700, ...docker }),
      listener({ port: 5432, pid: 1883706, family: "ipv6", address: "::", ...docker }),
    ]
    expect(reachVerdict(sockets[0])).toBe("critical")
    const [port] = dangerousPorts(sockets)
    expect(port.level).toBe("critical")
    expect(port.inboundDefault).toBeUndefined()
    expect(port.pastFirewall).toBe("docker")
    const [finding] = portFindings({ ports: sockets })
    expect(finding.level).toBe("critical")
    expect(finding.title).toBe("PostgreSQL answers on every interface")
    expect(finding.detail).toBe("5432/tcp docker-proxy (published by Docker past the firewall)")
    expect(finding.detail).not.toContain("inbound default")
  })

  test("one a rule admits from anywhere stays critical, and names the rule", () => {
    const redis = listener({
      port: 6379,
      process: "redis-server",
      level: "critical",
      pastFirewall: "rule",
      firewallRule: 10,
    })
    expect(reachVerdict(redis)).toBe("critical")
    expect(pastFirewallWords(redis)).toBe("Firewall rule 10 admits it from anywhere")
    const [finding] = portFindings({ ports: [redis] })
    expect(finding.level).toBe("critical")
    expect(finding.detail).toBe("6379/tcp redis-server (firewall rule 10 admits it from anywhere)")
  })

  test("the firewall's default is said beside the ports it holds when it does not hold all", () => {
    const [finding] = portFindings({
      ports: [
        listener({ port: 6379, process: "redis-server", level: "warning", inboundDefault: "deny" }),
        listener({
          port: 5432,
          process: "docker-proxy",
          level: "critical",
          pastFirewall: "docker",
        }),
      ],
    })
    expect(finding.level).toBe("critical")
    expect(finding.detail).toBe(
      "6379/tcp redis-server (the firewall's inbound default is deny), 5432/tcp docker-proxy (published by Docker past the firewall)",
    )
    // A port the default holds on one socket and not another is not held.
    const [mixed] = dangerousPorts([
      listener({ port: 5432, process: "postgres", level: "warning", inboundDefault: "deny" }),
      listener({
        port: 5432,
        process: "docker-proxy",
        address: "57.131.21.87",
        scope: "interface",
        reach: "public",
        network: "public",
        level: "critical",
        pastFirewall: "docker",
      }),
    ])
    expect(mixed.inboundDefault).toBeUndefined()
    expect(mixed.level).toBe("critical")
    expect(pastFirewallWords(mixed)).toBe("Published by Docker past the firewall")
    expect(pastFirewallWords(listener({ port: 5432 }))).toBeUndefined()
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
      level: "warning",
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
        listener({ port: 5432, process: "postgres", level: "critical" }),
        listener({ port: 5432, process: "postgres", address: "::", level: "critical" }),
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
