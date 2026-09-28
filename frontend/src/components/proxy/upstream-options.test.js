import { describe, expect, test } from "bun:test"
import { loopbackTarget, nothingListening, upstreamOptions } from "./upstream-options"

const listener = (address, port, process = "", protocol = "tcp") => ({
  protocol,
  address,
  port,
  pid: 0,
  process,
  exposed: address === "0.0.0.0" || address === "::",
})

const container = (name, ports, state = "running") => ({
  id: `${name}-id`,
  name,
  image: `ghcr.io/acme/${name}:1`,
  state,
  ports,
})

describe("upstream options", () => {
  test("a loopback or wildcard listener is offered on loopback, one per port", () => {
    const options = upstreamOptions(
      [
        listener("::", 8080, "caddy"),
        listener("0.0.0.0", 8080, "caddy"),
        listener("127.0.0.1", 3000, "node"),
        listener("::1", 4000, "deno"),
        listener("::", 5000, "registry"),
      ],
      [],
    )
    expect(options.map((o) => [o.url, o.name])).toEqual([
      ["http://127.0.0.1:3000", "node"],
      ["http://[::1]:4000", "deno"],
      ["http://[::1]:5000", "registry"],
      ["http://127.0.0.1:8080", "caddy"],
    ])
  })

  test("nginx itself, UDP, other interfaces and non-HTTP services are not offered", () => {
    const options = upstreamOptions(
      [
        listener("0.0.0.0", 443, "nginx"),
        listener("0.0.0.0", 5353, "avahi", "udp"),
        listener("10.0.0.5", 8081, "app"),
        listener("0.0.0.0", 5432, "postgres"),
        listener("127.0.0.1", 2375, "dockerd"),
        listener("0.0.0.0", 22, "sshd"),
        listener("127.0.0.1", 9090, "prometheus"),
      ],
      undefined,
    )
    expect(options.map((o) => o.address)).toEqual(["127.0.0.1:9090"])
  })

  test("a container is reached through the port it publishes, and one it does not is said", () => {
    const options = upstreamOptions(
      [listener("0.0.0.0", 8081, "docker-proxy"), listener("127.0.0.1", 3000, "node")],
      [
        container("shop", [
          { ip: "0.0.0.0", privatePort: 3000, publicPort: 8081, type: "tcp" },
          { ip: "::", privatePort: 3000, publicPort: 8081, type: "tcp" },
          { privatePort: 9229, type: "tcp" },
        ]),
        container("cache", [{ ip: "127.0.0.1", privatePort: 80, publicPort: 8082, type: "tcp" }]),
        container("worker", [{ privatePort: 8000, type: "tcp" }]),
        container(
          "stopped",
          [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8090, type: "tcp" }],
          "exited",
        ),
        container("dns", [{ ip: "0.0.0.0", privatePort: 53, publicPort: 53, type: "udp" }]),
      ],
    )
    expect(
      options.map((o) => ({
        url: o.url,
        name: o.name,
        to: o.containerPort,
        unreachable: Boolean(o.unreachable),
      })),
    ).toEqual([
      // Published first, by host port; then what nothing publishes.
      { url: "http://127.0.0.1:8081", name: "shop", to: 3000, unreachable: false },
      { url: "http://127.0.0.1:8082", name: "cache", to: 80, unreachable: false },
      { url: "", name: "worker", to: 8000, unreachable: true },
      { url: "", name: "shop", to: 9229, unreachable: true },
      // docker-proxy's 8081 is the shop container, already listed.
      { url: "http://127.0.0.1:3000", name: "node", to: undefined, unreachable: false },
    ])
    expect(options.find((o) => o.name === "worker").unreachable).toBe(
      "Not published. Publish it on 127.0.0.1 to reach it from nginx.",
    )
    expect(options.find((o) => o.name === "shop").image).toBe("ghcr.io/acme/shop:1")
  })

  test("a port published on one address is dialled there", () => {
    const options = upstreamOptions(
      [],
      [
        container("lan", [{ ip: "10.0.0.5", privatePort: 80, publicPort: 8000, type: "tcp" }]),
        container("v6", [{ ip: "::1", privatePort: 80, publicPort: 8001, type: "tcp" }]),
      ],
    )
    expect(options.map((o) => o.url)).toEqual(["http://10.0.0.5:8000", "http://[::1]:8001"])
  })

  test("a port TLS is served on is offered over https", () => {
    const options = upstreamOptions(
      [listener("0.0.0.0", 8443, "unifi"), listener("127.0.0.1", 443, "")],
      [container("vault", [{ ip: "127.0.0.1", privatePort: 8200, publicPort: 8443, type: "tcp" }])],
    )
    expect(options.map((o) => o.url)).toEqual(["https://127.0.0.1:8443", "https://127.0.0.1:443"])
  })

  test("nothing to list is an empty list", () => {
    expect(upstreamOptions(undefined, undefined)).toEqual([])
  })
})

describe("nothing listening", () => {
  const listeners = [
    listener("127.0.0.1", 3000, "node"),
    listener("::", 4000, "deno"),
    listener("0.0.0.0", 5000, "app"),
    listener("::1", 6000, "v6only"),
  ]

  test("a loopback upstream is read for its host and port", () => {
    expect(loopbackTarget("http://127.0.0.1:3000/api")).toEqual({ host: "127.0.0.1", port: 3000 })
    expect(loopbackTarget("https://localhost")).toEqual({ host: "localhost", port: 443 })
    expect(loopbackTarget("http://[::1]:8080")).toEqual({ host: "::1", port: 8080 })
    expect(loopbackTarget("http://LOCALHOST:81?x")).toEqual({ host: "localhost", port: 81 })
    for (const upstream of [
      "http://10.0.0.5:3000",
      "http://app:3000",
      "unix:/run/app.sock",
      "http://127.0.0.1:0",
      "http://127.0.0.1:70000",
      "127.0.0.1:3000",
    ]) {
      expect(loopbackTarget(upstream)).toBeUndefined()
    }
  })

  test("a port something accepts on is fine", () => {
    for (const upstream of [
      "http://127.0.0.1:3000",
      "http://localhost:3000",
      "http://127.0.0.1:4000",
      "http://[::1]:4000",
      "http://127.0.0.1:5000/",
      "http://[::1]:6000",
      "http://localhost:6000",
    ]) {
      expect(nothingListening(upstream, listeners, [])).toBeUndefined()
    }
  })

  test("a port nothing accepts on for that address is said", () => {
    expect(nothingListening("http://127.0.0.1:3001", listeners, [])).toBe(
      "Nothing is listening on 127.0.0.1:3001 right now, so nginx answers 502 until something does.",
    )
    // IPv4-only and IPv6-only sockets answer only their own family.
    expect(nothingListening("http://[::1]:5000", listeners, [])).toContain("[::1]:5000")
    expect(nothingListening("http://127.0.0.1:6000", listeners, [])).toContain("127.0.0.1:6000")
    expect(nothingListening("http://127.0.0.2:3000", listeners, [])).toContain("127.0.0.2:3000")
    expect(nothingListening("http://localhost", listeners, [])).toContain("localhost:80")
  })

  test("a published container port counts even when docker-proxy is off", () => {
    const shop = container("shop", [
      { ip: "0.0.0.0", privatePort: 3000, publicPort: 8081, type: "tcp" },
    ])
    expect(nothingListening("http://127.0.0.1:8081", [], [shop])).toBeUndefined()
    expect(nothingListening("http://127.0.0.1:8081", [], [{ ...shop, state: "exited" }])).toContain(
      "127.0.0.1:8081",
    )
  })

  test("nothing is said before the listeners are read, or off loopback", () => {
    expect(nothingListening("http://127.0.0.1:3001", undefined, [])).toBeUndefined()
    expect(nothingListening("http://10.0.0.5:3001", [], [])).toBeUndefined()
  })
})
