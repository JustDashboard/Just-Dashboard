import { expect, test } from "bun:test"
import {
  containersByImage,
  deployableStacks,
  imageStack,
  imageStackName,
  stackSource,
  usedBy,
} from "./server-sources"

function container(name, overrides = {}) {
  return {
    id: name,
    name,
    imageId: "sha256:tracker",
    state: "running",
    labels: {},
    ...overrides,
  }
}

function stack(name, overrides = {}) {
  return {
    name,
    workingDir: `/home/ubuntu/${name}`,
    configFiles: [`/home/ubuntu/${name}/docker-compose.yml`],
    services: [],
    running: 2,
    total: 2,
    managed: true,
    declared: [],
    containers: 2,
    deployed: true,
    orphans: [],
    state: "running",
    summary: "Running · 2/2 services",
    ...overrides,
  }
}

test("an image is described by the containers it runs as, running ones first", () => {
  expect(usedBy([])).toBeUndefined()
  expect(usedBy([container("old", { state: "exited" }), container("high-market-tracker")])).toBe(
    "Used by high-market-tracker and old",
  )
  expect(usedBy(["a", "b", "c", "d"].map((name) => container(name)))).toBe(
    "Used by a, b and 2 more",
  )
})

test("a deployment's own containers are not offered as something to deploy", () => {
  const byImage = containersByImage([
    container("high-market-tracker"),
    container("jd-e7-r2", { labels: { "io.just-dashboard.managed": "true" } }),
  ])
  expect(byImage.get("sha256:tracker").map((entry) => entry.name)).toEqual(["high-market-tracker"])
})

test("a stack is deployed from its own files, named inside its directory", () => {
  expect(
    stackSource(
      stack("bet-bot", {
        configFiles: [
          "/home/ubuntu/bet-bot/docker-compose.yml",
          "/home/ubuntu/bet-bot/ops/override.yml",
        ],
      }),
    ),
  ).toEqual({
    kind: "compose",
    mode: "compose_local",
    localPath: "/home/ubuntu/bet-bot",
    composeFiles: [
      { path: "docker-compose.yml", content: "", order: 0 },
      { path: "ops/override.yml", content: "", order: 1 },
    ],
  })
  // A file outside the directory cannot be named relative to it.
  expect(stackSource(stack("split", { configFiles: ["/srv/shared/compose.yml"] }))).toBeUndefined()
})

test("only stacks with files on disk, not already a deployment, are offered", () => {
  const stacks = [
    stack("idle", { running: 0 }),
    stack("bet-bot"),
    stack("gone", { managed: false }),
    stack("just-dashboard"),
    stack("jd-e7"),
  ]
  const containers = [
    container("jd-e7-r2", {
      composeStack: "jd-e7",
      labels: { "io.just-dashboard.managed": "true" },
    }),
  ]
  expect(deployableStacks(stacks, containers).map((entry) => entry.name)).toEqual([
    "bet-bot",
    "idle",
  ])
})

test("images chosen together become one stack with a service for each", () => {
  const images = [
    {
      tag: "bet-bot-high-market-tracker:latest",
      containers: [
        container("high-market-tracker", {
          composeStack: "bet-bot",
          composeService: "high-market-tracker",
        }),
      ],
    },
    {
      tag: "bet-bot-doubles-games-tracker:latest",
      containers: [
        container("doubles-games-tracker", {
          composeStack: "bet-bot",
          composeService: "doubles-games-tracker",
        }),
      ],
    },
    { tag: "ghcr.io/acme/Worker:1", containers: [] },
    { tag: "registry.example/acme/worker:2", containers: [] },
  ]
  const source = imageStack(images)
  expect(source.kind).toBe("compose")
  expect(source.mode).toBe("compose_paste")
  expect(source.composeFiles).toEqual([
    {
      path: "compose.yaml",
      order: 0,
      content: [
        "services:",
        "  high-market-tracker:",
        '    image: "bet-bot-high-market-tracker:latest"',
        "    restart: unless-stopped",
        "  doubles-games-tracker:",
        '    image: "bet-bot-doubles-games-tracker:latest"',
        "    restart: unless-stopped",
        "  worker:",
        '    image: "ghcr.io/acme/Worker:1"',
        "    restart: unless-stopped",
        "  worker-2:",
        '    image: "registry.example/acme/worker:2"',
        "    restart: unless-stopped",
        "",
      ].join("\n"),
    },
  ])
  expect(imageStackName(images.slice(0, 2))).toBe("bet-bot")
  expect(imageStackName(images.slice(1))).toBe("bet-bot-doubles-games-tracker")
})
