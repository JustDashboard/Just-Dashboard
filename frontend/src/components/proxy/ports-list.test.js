import { describe, expect, test } from "bun:test"
import { foldDualStack } from "./ports"
import {
  chosenPort,
  DEFAULT_SORT,
  facetCounts,
  formatEndpoint,
  groupByOwner,
  groupPids,
  groupPorts,
  isLoopbackEphemeral,
  matchesQuery,
  parseQuery,
  parseSort,
  portsHref,
  sinceWords,
  sortParam,
  sortSockets,
  toCsv,
  toJson,
  viewFromParams,
  viewQuery,
  visibleSockets,
  withView,
  worstSocket,
} from "./ports-list"

const listener = (overrides) => ({
  protocol: "tcp",
  family: "ipv4",
  address: "0.0.0.0",
  port: 443,
  pid: 812,
  process: "nginx",
  cmdline: "nginx: master process",
  user: "root",
  scope: "all",
  reach: "all",
  network: "all",
  exposed: true,
  ...overrides,
})

const loopback = (overrides) =>
  listener({
    address: "127.0.0.1",
    scope: "loopback",
    reach: "loopback",
    network: "loopback",
    exposed: false,
    ...overrides,
  })

// A host as the ports page lists it: sshd in both families, caddy on the
// tailnet, the DNS stub, Postgres on every interface (critical), and twenty
// git-daemon sockets on loopback ports the kernel handed out.
const sshd = listener({
  port: 22,
  pid: 2450808,
  process: "sshd",
  cmdline: "sshd: /usr/sbin/sshd -D",
})
const sshd6 = { ...sshd, family: "ipv6", address: "::" }
const caddy = listener({
  address: "100.110.34.31",
  port: 8443,
  pid: 3001,
  process: "caddy",
  cmdline: "caddy run",
  user: "caddy",
  scope: "interface",
  reach: "network",
  network: "tailnet",
  interface: "tailscale0",
})
const resolved = loopback({
  protocol: "udp",
  address: "127.0.0.53",
  port: 53,
  pid: 4412,
  process: "systemd-resolve",
  cmdline: "/usr/lib/systemd/systemd-resolved",
  user: "systemd-resolve",
})
const postgres = listener({
  port: 5432,
  pid: 900,
  process: "postgres",
  cmdline: "postgres -D /var/lib/postgresql",
  user: "postgres",
  level: "critical",
})
const gitDaemons = Array.from({ length: 20 }, (_, i) =>
  loopback({
    port: 33000 + i * 997,
    pid: 5000 + i,
    process: "git-daemon",
    cmdline: `git daemon --port=${33000 + i * 997}`,
    user: "ubuntu",
  }),
)
const host = foldDualStack([sshd, sshd6, caddy, resolved, postgres, ...gitDaemons])
const range = { low: 32768, high: 60999 }

const matching = (query, sockets = host) =>
  sockets.filter((socket) => matchesQuery(socket, parseQuery(query)))

describe("the search box's tokens", () => {
  test("parse into fields, and anything else is text", () => {
    expect(parseQuery("  :443  proto:UDP user:postgres pid:812  nginx ")).toEqual([
      { kind: "port", spans: [[443, 443]] },
      { kind: "proto", value: "udp" },
      { kind: "user", value: "postgres" },
      { kind: "pid", value: 812 },
      { kind: "text", value: "nginx" },
    ])
    expect(parseQuery("[::]:22 0.0.0.0:80 *:53 [fe80::1%eth0]:5353 [::1]")).toEqual([
      { kind: "endpoint", address: "::", port: 22 },
      { kind: "endpoint", address: "0.0.0.0", port: 80 },
      { kind: "endpoint", address: "*", port: 53 },
      { kind: "endpoint", address: "fe80::1%eth0", port: 5353 },
      { kind: "address", address: "::1" },
    ])
    expect(parseQuery("port:5432,6379 port:8000-8100")).toEqual([
      {
        kind: "port",
        spans: [
          [5432, 5432],
          [6379, 6379],
        ],
      },
      { kind: "port", spans: [[8000, 8100]] },
    ])
  })

  test("a token that looks like a field but is not one is searched as typed", () => {
    for (const token of [
      "pid:abc",
      "proto:icmp",
      "port:0",
      "port:70000",
      "port:9-3",
      "port:1-2-3",
      "port:",
      ":0",
      ":http",
      "[::1]:99999",
      "localhost:3000",
      "fe80::1",
      "container:web",
    ]) {
      expect(parseQuery(token)).toEqual([{ kind: "text", value: token }])
    }
  })

  test("a port matches exactly, a list or span by any of it", () => {
    expect(matching(":22")).toEqual([host.find((s) => s.port === 22)])
    expect(matching("port:5432,53").map((s) => s.port)).toEqual([53, 5432])
    expect(matching("port:33000-34000").map((s) => s.port)).toEqual([33000, 33997])
    // Text still finds a port inside a longer one, as the box always did.
    expect(matching("443").map((s) => s.port)).toEqual([8443])
  })

  test("an endpoint matches either address of a folded pair, and * any wildcard", () => {
    const [ssh] = matching("[::]:22")
    expect(ssh.address).toBe("0.0.0.0")
    expect(ssh.twin.address).toBe("::")
    expect(matching("0.0.0.0:22")).toEqual([ssh])
    expect(matching("*:22")).toEqual([ssh])
    expect(matching("*:53")).toEqual([])
    expect(matching("127.0.0.53:53")).toEqual([host.find((s) => s.port === 53)])
    expect(matching("[fd7a::1]:8443")).toEqual([])
    expect(matching("100.110.34.31:8443").map((s) => s.process)).toEqual(["caddy"])
    expect(matching("[::]").map((s) => s.port)).toEqual([22])
  })

  test("fields narrow together, and text looks everywhere a row shows", () => {
    expect(matching("proto:udp").map((s) => s.port)).toEqual([53])
    expect(matching("user:post").map((s) => s.process)).toEqual(["postgres"])
    expect(matching("pid:3001").map((s) => s.process)).toEqual(["caddy"])
    expect(matching("pid:2450808 :22")).toHaveLength(1)
    expect(matching("pid:2450808 :23")).toHaveLength(0)
    expect(matching("tailscale0").map((s) => s.process)).toEqual(["caddy"])
    expect(matching("tailnet").map((s) => s.process)).toEqual(["caddy"])
    expect(matching("git DAEMON")).toHaveLength(20)
    expect(matching("[::]:22")).toHaveLength(1)
  })
})

describe("owners in the search box", () => {
  const ingress = listener({
    port: 80,
    process: "docker-proxy",
    container: {
      id: "5e3ac6b0d3f1",
      name: "just-dashboard-ingress",
      image: "caddy:2-alpine",
      published: true,
    },
  })
  const node = loopback({ port: 3222, process: "MainThread", displayName: "node" })
  const self = loopback({ port: 8080, process: "jd-server", self: true })
  const matching = (query) =>
    [ingress, node, self].filter((s) => matchesQuery(s, parseQuery(query))).map((s) => s.port)

  test("a container, its image, a program under a thread's name, the dashboard", () => {
    expect(matching("ingress")).toEqual([80])
    expect(matching("caddy")).toEqual([80])
    expect(matching("node")).toEqual([3222])
    // The dashboard's own backend is named jd-server; its tag is a word too.
    expect(matching("dashboard")).toEqual([80, 8080])
  })

  test("the application column sorts by owner", () => {
    expect(
      sortSockets([self, ingress, node], { key: "process", dir: "asc" }).map((s) => s.port),
    ).toEqual([8080, 80, 3222])
  })
})

describe("endpoints", () => {
  test("are written as a client dials them", () => {
    expect(formatEndpoint("127.0.0.1", 8080)).toBe("127.0.0.1:8080")
    expect(formatEndpoint("::1", 8080)).toBe("[::1]:8080")
    expect(formatEndpoint("::", 22)).toBe("[::]:22")
    expect(formatEndpoint("fe80::b482:4dff:fe92:4281%docker0", 46505)).toBe(
      "[fe80::b482:4dff:fe92:4281%docker0]:46505",
    )
    expect(formatEndpoint("*", 53)).toBe("*:53")
    expect(formatEndpoint("", 53)).toBe("*:53")
  })
})

describe("the two filters combine", () => {
  const filters = (overrides) => ({
    terms: [],
    reach: "all",
    proto: "all",
    hide: null,
    ...overrides,
  })

  test("reach and protocol narrow together", () => {
    const udp = listener({ protocol: "udp", port: 41641, process: "tailscaled" })
    const sockets = foldDualStack([sshd, sshd6, caddy, resolved, postgres, ...gitDaemons, udp])
    expect(
      visibleSockets(sockets, filters({ reach: "internet", proto: "udp" })).map((s) => s.port),
    ).toEqual([41641])
    expect(visibleSockets(sockets, filters({ reach: "local", proto: "udp" }))).toEqual([
      sockets.find((s) => s.port === 53),
    ])
  })

  test("each chip counts what it would show with the other filters as they are", () => {
    const facets = facetCounts(host, filters({ proto: "tcp", terms: parseQuery("git") }), range)
    // The reach counts follow the protocol and the search, not the reach.
    expect(facets.reach).toEqual({ all: 20, internet: 0, private: 0, local: 20 })
    const all = facetCounts(host, filters({ reach: "local" }), range)
    expect(all.reach).toEqual({ all: 24, internet: 2, private: 1, local: 21 })
    // The protocol counts follow the reach.
    expect(all.proto).toEqual({ tcp: 20, udp: 1 })
    expect(all.ephemeral).toBe(20)
  })

  test("loopback ephemeral sockets are set aside, and counted, only with the range known", () => {
    expect(visibleSockets(host, filters({ hide: range }))).toHaveLength(4)
    expect(visibleSockets(host, filters())).toHaveLength(24)
    const hidden = facetCounts(host, filters({ hide: range }), range)
    expect(hidden.reach.all).toBe(4)
    expect(hidden.ephemeral).toBe(20)
    // A socket on a public address in the range is not the kernel's gift.
    expect(isLoopbackEphemeral(listener({ port: 40000 }), range)).toBe(false)
    expect(isLoopbackEphemeral(loopback({ port: 40000 }), range)).toBe(true)
    expect(isLoopbackEphemeral(loopback({ port: 32767 }), range)).toBe(false)
    expect(isLoopbackEphemeral(loopback({ port: 60999 }), range)).toBe(true)
    expect(isLoopbackEphemeral(loopback({ port: 40000 }), null)).toBe(false)
  })

  test("a connection's port is a listener's only outside the kernel's range", () => {
    expect(chosenPort(443, range)).toBe(true)
    expect(chosenPort(61000, range)).toBe(true)
    expect(chosenPort(51234, range)).toBe(false)
    expect(chosenPort(443, null)).toBe(false)
  })
})

describe("sorting", () => {
  const ports = (sort) => sortSockets(host, sort).map((s) => s.port)

  test("worst first by default, then by port", () => {
    expect(ports(DEFAULT_SORT).slice(0, 5)).toEqual([5432, 22, 8443, 53, 33000])
    expect(ports({ key: "reach", dir: "asc" }).slice(-3)).toEqual([8443, 22, 5432])
  })

  test("by port either way, and by application with the port breaking ties", () => {
    expect(ports({ key: "port", dir: "asc" }).slice(0, 4)).toEqual([22, 53, 5432, 8443])
    expect(ports({ key: "port", dir: "desc" })[0]).toBe(33000 + 19 * 997)
    const byName = sortSockets(host, { key: "process", dir: "asc" }).map((s) => s.process)
    expect(byName[0]).toBe("caddy")
    expect(byName.at(-1)).toBe("systemd-resolve")
    expect(sortSockets(host, { key: "process", dir: "desc" })[0].process).toBe("systemd-resolve")
    const gits = sortSockets(host, { key: "process", dir: "asc" }).filter(
      (s) => s.process === "git-daemon",
    )
    expect(gits.map((s) => s.port)).toEqual(gitDaemons.map((s) => s.port))
  })

  test("a sort is written in the address bar as a column, minus for descending", () => {
    expect(sortParam(DEFAULT_SORT)).toBe("-reach")
    expect(parseSort("-port")).toEqual({ key: "port", dir: "desc" })
    expect(parseSort("process")).toEqual({ key: "process", dir: "asc" })
    expect(parseSort("-owner")).toBeUndefined()
    expect(parseSort("")).toBeUndefined()
  })
})

describe("grouping by application", () => {
  test("twenty git-daemon sockets are one group, in the order the sort put them", () => {
    const groups = groupByOwner(sortSockets(host, DEFAULT_SORT))
    expect(groups.map((g) => [g.process, g.sockets.length])).toEqual([
      ["postgres", 1],
      ["sshd", 1],
      ["caddy", 1],
      ["systemd-resolve", 1],
      ["git-daemon", 20],
    ])
    const git = groups.at(-1)
    expect(groupPorts(git)).toBe("33000, 33997, 34994 +17")
    expect(groupPids(git)).toHaveLength(20)
    expect(worstSocket(git).port).toBe(33000)
  })

  test("one program run by two accounts is two groups; an unreadable owner is 'unknown'", () => {
    const groups = groupByOwner([
      listener({ process: "node", user: "app", port: 3000 }),
      listener({ process: "node", user: "ci", port: 3001 }),
      listener({ process: "node", user: "app", port: 3002 }),
      listener({ process: "", user: undefined, pid: 0, port: 9 }),
    ])
    expect(groups.map((g) => [g.process, g.user, g.sockets.map((s) => s.port)])).toEqual([
      ["node", "app", [3000, 3002]],
      ["node", "ci", [3001]],
      ["unknown", "", [9]],
    ])
  })

  test("a container's ports are its own group, whatever holds them", () => {
    const proxy = (port, name) =>
      listener({
        port,
        process: "docker-proxy",
        container: { id: name.slice(0, 12), name, image: "postgres:16-alpine", published: true },
      })
    const groups = groupByOwner([
      proxy(5432, "Qhahdhhas"),
      proxy(5433, "epgjauto-db-1"),
      proxy(5434, "Qhahdhhas"),
    ])
    expect(groups.map((g) => [g.process, g.sockets.map((s) => s.port)])).toEqual([
      ["Qhahdhhas", [5432, 5434]],
      ["epgjauto-db-1", [5433]],
    ])
  })

  test("a group's reach is its worst socket's", () => {
    const [group] = groupByOwner([
      listener({ process: "redis-server", port: 6380, reach: "loopback", exposed: false }),
      listener({ process: "redis-server", port: 6379, level: "critical" }),
    ])
    expect(worstSocket(group).port).toBe(6379)
    expect(groupPorts(group)).toBe("6379, 6380")
  })
})

describe("the view in the address bar", () => {
  const params = (search) => new URLSearchParams(search)

  test("a link that names any part of the view names all of it", () => {
    expect(viewFromParams(params(""))).toBeNull()
    expect(viewFromParams(params("socket=tcp:0.0.0.0:443"))).toBeNull()
    expect(viewFromParams(params("reach=internet&proto=udp"))).toEqual({
      q: "",
      reach: "internet",
      proto: "udp",
      sort: DEFAULT_SORT,
    })
    expect(viewFromParams(params("q=:443&sort=-port"))).toEqual({
      q: ":443",
      reach: "all",
      proto: "all",
      sort: { key: "port", dir: "desc" },
    })
    // A value the page has no filter for is that filter's default.
    expect(viewFromParams(params("reach=exposed&proto=icmp&sort=owner"))).toEqual({
      q: "",
      reach: "all",
      proto: "all",
      sort: DEFAULT_SORT,
    })
  })

  test("is written readably, defaults left out, other keys kept", () => {
    const view = {
      q: "[::]:22 port:5432,6379 user:a b",
      reach: "all",
      proto: "all",
      sort: DEFAULT_SORT,
    }
    expect(viewQuery(view)).toBe("q=[::]:22+port:5432,6379+user:a+b")
    expect(viewFromParams(params(viewQuery(view)))).toEqual(view)
    expect(viewQuery({ ...view, q: "a+b&c=d#e" })).toBe("q=a%2Bb%26c%3Dd%23e")
    expect(viewFromParams(params(viewQuery({ ...view, q: "a+b&c=d#e" }))).q).toBe("a+b&c=d#e")
    expect(
      withView("?socket=x&q=old&reach=local", {
        q: "",
        reach: "internet",
        proto: "tcp",
        sort: { key: "port", dir: "asc" },
      }),
    ).toBe("?socket=x&reach=internet&proto=tcp&sort=port")
    expect(withView("?q=old", { q: "", reach: "all", proto: "all", sort: DEFAULT_SORT })).toBe("")
  })

  test("a link into the page opens on the view it names", () => {
    expect(portsHref({ q: "port:5432,6379" })).toBe("/proxy/ports?q=port:5432,6379")
    expect(portsHref({ reach: "internet" })).toBe("/proxy/ports?reach=internet")
    // The whole list is a view the link names, not the tab's last one.
    expect(portsHref({})).toBe("/proxy/ports?q=")
    for (const href of [portsHref({}), portsHref({ reach: "internet" })]) {
      const view = viewFromParams(new URL(href, "http://localhost").searchParams)
      expect(view).not.toBeNull()
      expect(portsHref(view)).toBe(href)
    }
    expect(withView("?q=", viewFromParams(params("q=")))).toBe("")
  })
})

describe("exports", () => {
  test("CSV is one line per socket, a folded pair as both, escaped and inert", () => {
    const risky = listener({
      port: 9999,
      process: "evil",
      cmdline: '=HYPERLINK("http://x","y"), with "quotes"\nand a line',
      user: "@admin",
      family: "ipv6",
      address: "::",
    })
    const csv = toCsv([...foldDualStack([sshd, sshd6]), risky])
    const lines = csv.trimEnd().split("\n")
    expect(lines[0]).toBe(
      "protocol,family,address,port,endpoint,reach,network,interface,process,pid,ppid,user,cmdline,level,owner",
    )
    expect(lines[1]).toBe(
      "tcp,ipv4,0.0.0.0,22,0.0.0.0:22,all,all,,sshd,2450808,,root,sshd: /usr/sbin/sshd -D,,sshd",
    )
    expect(lines[2]).toBe(
      "tcp,ipv6,::,22,[::]:22,all,all,,sshd,2450808,,root,sshd: /usr/sbin/sshd -D,,sshd",
    )
    // A command beginning "=" is text to a spreadsheet, and a quote, comma
    // or newline stays inside its cell.
    expect(csv).toContain(
      `tcp,ipv6,::,9999,[::]:9999,all,all,,evil,812,,'@admin,"'=HYPERLINK(""http://x"",""y""), with ""quotes""\nand a line",,evil`,
    )
    expect(csv.endsWith("\n")).toBe(true)
  })

  test("JSON is the sockets as GET /ports answers them, without the fold", () => {
    const rows = JSON.parse(toJson(foldDualStack([sshd, sshd6])))
    expect(rows).toEqual([sshd, sshd6])
  })
})

describe("freshness", () => {
  test("says how old the list is, to the second while that matters", () => {
    expect(sinceWords(-200)).toBe("just now")
    expect(sinceWords(4999)).toBe("just now")
    expect(sinceWords(8000)).toBe("8s ago")
    expect(sinceWords(59_999)).toBe("59s ago")
    expect(sinceWords(60_000)).toBe("1m ago")
    expect(sinceWords(3 * 3_600_000 + 5)).toBe("3h ago")
  })
})
