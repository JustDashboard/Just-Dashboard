import { expect, test } from "bun:test"
import {
  foldPull,
  freshness,
  layerInstruction,
  parseSize,
  pullShare,
  registryName,
  splitReference,
  visibleImages,
} from "./images"

test("a reference splits the way a registry reads it", () => {
  expect(splitReference("nginx:1.27-alpine")).toEqual({
    registry: undefined,
    repository: "nginx",
    tag: "1.27-alpine",
    digest: undefined,
  })
  // A first segment without a dot, a port or localhost is a Docker Hub namespace.
  expect(splitReference("grafana/grafana:11.2.0").registry).toBeUndefined()
  expect(splitReference("grafana/grafana:11.2.0").repository).toBe("grafana/grafana")
  expect(splitReference("ghcr.io/acme/api:main")).toMatchObject({
    registry: "ghcr.io",
    repository: "acme/api",
    tag: "main",
  })
  // The port's colon is the registry's, not a tag.
  expect(splitReference("localhost:5000/tools")).toMatchObject({
    registry: "localhost:5000",
    repository: "tools",
    tag: undefined,
  })
  expect(splitReference("caddy@sha256:abc")).toMatchObject({
    repository: "caddy",
    digest: "sha256:abc",
  })
  expect(registryName("quay.io/prometheus/node-exporter:v1.8.2")).toBe("quay.io")
  expect(registryName("redis:7")).toBe("Docker Hub")
})

const image = (over) => ({
  id: "sha256:aa",
  repoTags: ["nginx:alpine"],
  repoDigests: ["nginx@sha256:bb"],
  size: 10,
  created: "2026-10-01T00:00:00Z",
  containers: 1,
  labels: {},
  dangling: false,
  ...over,
})

test("every image gets one answer, and one nothing runs is not called unchecked", () => {
  const checked = { ref: "nginx:alpine", checkedAt: "" }
  expect(freshness(image(), { ...checked, state: "outdated" })).toBe("outdated")
  expect(freshness(image(), { ...checked, state: "pinned" })).toBe("pinned")
  expect(freshness(image(), { ...checked, state: "unknown" })).toBe("unknown")
  expect(freshness(image(), undefined)).toBe("unknown")
  expect(freshness(image({ containers: 0 }), undefined)).toBe("unused")
  // Running and never pushed: there is no registry to ask.
  expect(freshness(image({ repoDigests: [] }), undefined)).toBe("local")
  expect(freshness(image({ repoTags: [], dangling: true }), undefined)).toBe("untagged")
})

test("the table narrows by use, by answer and by text, largest first", () => {
  const images = [
    image({ id: "a", repoTags: ["nginx:alpine"], size: 5 }),
    image({ id: "b", repoTags: ["postgres:15"], size: 50, containers: 0 }),
    image({ id: "c", repoTags: [], dangling: true, size: 20, containers: 0 }),
    image({
      id: "d",
      repoTags: ["redis:7"],
      size: 9,
      labels: { "org.opencontainers.image.version": "7.4.1" },
    }),
  ]
  const states = new Map([
    ["a", "outdated"],
    ["b", "unused"],
    ["c", "untagged"],
    ["d", "current"],
  ])
  const ids = (options) =>
    visibleImages(images, {
      query: "",
      use: "all",
      state: "",
      sort: "size",
      states,
      ...options,
    }).map((i) => i.id)
  expect(ids({})).toEqual(["b", "c", "d", "a"])
  expect(ids({ use: "running" })).toEqual(["d", "a"])
  expect(ids({ use: "unused" })).toEqual(["b"])
  expect(ids({ use: "untagged" })).toEqual(["c"])
  expect(ids({ state: "outdated" })).toEqual(["a"])
  expect(ids({ query: "7.4" })).toEqual(["d"])
  // By name, an untagged layer has no name to place and goes last.
  expect(ids({ sort: "name" })).toEqual(["a", "b", "d", "c"])
  expect(images.map((i) => i.id)).toEqual(["a", "b", "c", "d"])
})

test("a pull's stream folds into one row per layer with its bytes", () => {
  expect(parseSize("41.9MB")).toBe(41.9e6)
  expect(parseSize("512B")).toBe(512)
  expect(parseSize("1.2 GB")).toBe(1.2e9)
  expect(parseSize("soon")).toBeUndefined()

  const { layers, summary } = foldPull([
    { id: "8", status: "Pulling from valkey/valkey" },
    { id: "302e3ee49805", status: "Pulling fs layer" },
    { id: "a2318d6c47ec", status: "Already exists" },
    { id: "302e3ee49805", status: "Downloading", progress: "[==>   ]  6.29MB/31.4MB" },
    { id: "5f2c4b1c2a7d", status: "Downloading", progress: "[=====>]  41.9MB/57.6MB" },
    { id: "5f2c4b1c2a7d", status: "Download complete" },
    { id: "5f2c4b1c2a7d", status: "Extracting", progress: "[=>    ]  10MB/57.6MB" },
    { status: "Digest: sha256:abc" },
  ])
  expect(summary).toBe("Digest: sha256:abc")
  expect(layers.map((l) => [l.id, l.phase])).toEqual([
    ["302e3ee49805", "downloading"],
    ["a2318d6c47ec", "cached"],
    ["5f2c4b1c2a7d", "extracting"],
  ])
  // "Download complete" carries no bytes; the total from before it survives.
  expect(layers[2].total).toBe(57.6e6)
  // A layer already here is not part of what is being downloaded.
  expect(pullShare(layers)).toEqual({ moved: 6.29e6 + 57.6e6, total: 31.4e6 + 57.6e6 })
})

test("a history line reads as the instruction that wrote it", () => {
  expect(layerInstruction('/bin/sh -c #(nop)  CMD ["nginx"]')).toEqual({
    verb: "CMD",
    rest: '["nginx"]',
  })
  expect(layerInstruction("/bin/sh -c apk add curl")).toEqual({ verb: "RUN", rest: "apk add curl" })
  expect(layerInstruction("RUN /bin/sh -c set -x && make # buildkit")).toEqual({
    verb: "RUN",
    rest: "set -x && make",
  })
  expect(layerInstruction("COPY app /srv # buildkit")).toEqual({ verb: "COPY", rest: "app /srv" })
  expect(layerInstruction("")).toEqual({ verb: "", rest: "" })
})
