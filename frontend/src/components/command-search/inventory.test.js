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
