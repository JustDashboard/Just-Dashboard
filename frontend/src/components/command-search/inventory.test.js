import { expect, test, spyOn } from "bun:test"
import { INVENTORY_SOURCES } from "./inventory"

test("live search projects explicit metadata and encodes identities without indexing payload secrets", async () => {
  const secret = "must-never-enter-search"
  const fixtures = {
    "/deploy/": {
      deployments: [
        {
          id: 3,
          name: "shop",
          endpoint: `https://admin:${secret}@shop.test?token=${secret}`,
          environmentName: "Production",
          environmentKind: "production",
          health: "healthy",
          env: { PASSWORD: secret },
        },
      ],
    },
    "/proxy/vhosts": [
      {
        kind: "nginx",
        name: "a & b",
        path: "/etc/nginx/a",
        serverNames: ["shop.test"],
        content: secret,
      },
    ],
    "/databases/": [
      {
        id: 1,
        name: "shop-db",
        driver: "postgres",
        database: "shop",
        host: "localhost",
        environment: "production",
        password: secret,
        notes: secret,
        user: secret,
      },
    ],
    "/docker/containers/": [
      {
        id: "a/b",
        name: "shop-web",
        state: "running",
        image: "nginx",
        names: ["shop-web"],
        labels: { password: secret },
        env: [secret],
        command: secret,
      },
    ],
    "/docker/stacks/": [
      {
        name: "a & b",
        workingDir: "/srv/shop",
        declared: ["web"],
        summary: "Running",
        content: secret,
      },
    ],
    "/git/": {
      available: true,
      repos: [
        {
          name: "shop",
          path: "/srv/a & b",
          branch: "main",
          remote: `https://${secret}@example.test`,
        },
      ],
    },
    "/systemd/": {
      available: true,
      units: [{ name: "shop.service", activeState: "active", description: "Shop", exec: secret }],
    },
    "/pm2/": {
      available: true,
      processes: [
        {
          id: 0,
          daemonId: "a:b",
          user: "deploy",
          name: "shop",
          namespace: "shop",
          status: "online",
          env: secret,
        },
      ],
    },
    "/backups/": [
      {
        id: 9,
        name: "shop-nightly",
        targetKind: "local",
        target: { path: secret },
        sources: ["/srv/shop"],
        schedule: "",
        credentials: secret,
      },
    ],
    "/boards/": [{ id: 6, name: "Shop plan", scene: secret }],
  }
  const calls = []
  const fetch = spyOn(globalThis, "fetch").mockImplementation((url, options) => {
    calls.push({ url: String(url), options })
    const path = String(url)
      .replace(/^\/api\/v1/, "")
      .split("?")[0]
    return Promise.resolve(
      new Response(JSON.stringify(fixtures[path]), {
        headers: { "Content-Type": "application/json" },
      }),
    )
  })
  try {
    const results = await Promise.all(
      INVENTORY_SOURCES.map((source) => source.read(new AbortController().signal)),
    )
    expect(results.flat()).toHaveLength(10)
    expect(JSON.stringify(results)).not.toContain(secret)
    expect(results[0][0].detail).toBe("shop.test")
    expect(results[1][0].href).toBe("/proxy/sites/a%20%26%20b")
    expect(results[3][0].href).toBe("/docker/containers/a%2Fb")
    expect(results[5][0].href).toBe("/git?repo=%2Fsrv%2Fa%20%26%20b")
    expect(results[7][0].href).toBe("/processes/pm2?app=a%3Ab%3A0")
    for (const call of calls) {
      expect(call.options.method).toBe("GET")
      expect(call.options.credentials).toBe("include")
      expect(call.options.signal).toBeInstanceOf(AbortSignal)
    }
  } finally {
    fetch.mockRestore()
  }
})

test("resources name what they are wired to by exact values and say the rest as facts", async () => {
  const fixtures = {
    "/deploy/": {
      deployments: [{ id: 1, name: "shop", endpoint: "https://Shop.test/app", health: "healthy" }],
    },
    "/proxy/vhosts": [
      { kind: "caddy", name: "shop", path: "/etc/caddy", serverNames: ["shop.test"] },
    ],
    "/databases/": [
      { id: 2, name: "main", driver: "postgres", host: "shop-db", origin: "", readOnly: true },
    ],
    "/docker/containers/": [
      {
        id: "c1",
        name: "shop-db",
        names: ["/shop-db"],
        image: "postgres:17",
        state: "running",
        status: "Up 2 hours",
        composeStack: "shop",
      },
    ],
    "/docker/stacks/": [{ name: "shop", workingDir: "/srv/shop", declared: ["db"], summary: "Up" }],
    "/git/": { available: true, repos: [{ name: "shop", path: "/srv/shop", branch: "main" }] },
    "/systemd/": { available: false, units: [] },
    "/pm2/": { processes: [] },
    "/backups/": [
      { id: 3, name: "nightly", targetKind: "b2", sources: ["/srv/shop/"], schedule: "" },
    ],
    "/boards/": [],
  }
  const fetch = spyOn(globalThis, "fetch").mockImplementation((url) => {
    const path = String(url)
      .replace(/^\/api\/v1/, "")
      .split("?")[0]
    return Promise.resolve(
      new Response(JSON.stringify(fixtures[path]), {
        headers: { "Content-Type": "application/json" },
      }),
    )
  })
  try {
    const items = (
      await Promise.all(
        INVENTORY_SOURCES.map((source) => source.read(new AbortController().signal)),
      )
    ).flat()
    const links = Object.fromEntries(items.map((item) => [item.id, item.links]))
    expect(links["project:1"]).toEqual(["domain:shop.test"])
    expect(links["site:caddy:/etc/caddy:shop"]).toEqual(["domain:shop.test"])
    expect(links["database:2"]).toEqual(["container:shop-db"])
    expect(links["container:c1"]).toEqual(["container:shop-db", "stack:shop"])
    expect(links["stack:shop"]).toEqual(["stack:shop", "folder:/srv/shop"])
    expect(links["repo:/srv/shop"]).toEqual(["folder:/srv/shop"])
    expect(links["backup:3"]).toEqual(["folder:/srv/shop"])
    const facts = (id) =>
      Object.fromEntries(items.find((item) => item.id === id).facts.map((f) => [f.label, f.value]))
    expect(facts("container:c1")).toEqual({
      Image: "postgres:17",
      Status: "Up 2 hours",
      Compose: "shop",
    })
    expect(facts("database:2")).toEqual({
      Engine: "postgres",
      Host: "shop-db",
      Access: "Protected — read only",
    })
    expect(facts("backup:3")).toEqual({
      "Writes to": "Backblaze B2",
      Schedule: "Manual",
      Covers: "/srv/shop/",
    })
  } finally {
    fetch.mockRestore()
  }
})
