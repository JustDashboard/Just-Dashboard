import { describe, expect, test } from "bun:test"
import {
  MAX_REPLICAS,
  effectiveReplicas,
  replicaReading,
  replicaRefusal,
  withReplicas,
} from "./replicas-model"

const runtime = (overrides = {}) => ({ strategy: "blue_green", internalPort: 3000, ...overrides })

describe("replica refusal", () => {
  test("a stateless plan can run replicas, a read-only mount included", () => {
    expect(replicaRefusal(runtime(), "recipe")).toBeUndefined()
    expect(
      replicaRefusal(
        runtime({ mounts: [{ source: "/srv/a", target: "/srv/a", readOnly: true }] }),
        "dockerfile",
      ),
    ).toBeUndefined()
  })

  test("says what only one container can hold", () => {
    expect(replicaRefusal(runtime(), "compose")).toBe(
      "a Compose stack runs one copy of each of its services",
    )
    expect(replicaRefusal(runtime({ hostNetwork: true }), "image")).toContain("host network")
    expect(replicaRefusal(runtime({ hostPort: 8080 }), "image")).toBe(
      "host port 8080 can be held by one container at a time",
    )
    expect(
      replicaRefusal(runtime({ ports: [{ hostPort: 2222, containerPort: 22 }] }), "image"),
    ).toContain("published port 2222")
    expect(
      replicaRefusal(runtime({ mounts: [{ source: "data", target: "/var/lib/app" }] }), "image"),
    ).toBe("/var/lib/app is writable, and replicas would write to it at the same time")
  })
})

describe("replica count", () => {
  test("unset and one are one, and the field is left out at one", () => {
    expect(effectiveReplicas({})).toBe(1)
    expect(effectiveReplicas({ replicas: 1 })).toBe(1)
    expect(effectiveReplicas({ replicas: 3 })).toBe(3)
    expect(effectiveReplicas({ replicas: 40 })).toBe(MAX_REPLICAS)
    expect("replicas" in withReplicas({ replicas: 3, strategy: "blue_green" }, 1)).toBe(false)
    expect(withReplicas({ strategy: "blue_green" }, 4).replicas).toBe(4)
  })
})

describe("replica reading", () => {
  const service = (overrides) => ({
    containerId: "c",
    name: "jd-e7-r3",
    releaseId: 3,
    liveRelease: true,
    state: "running",
    health: "unavailable",
    imageId: "sha256:1",
    ...overrides,
  })

  test("a release of one container has none", () => {
    expect(replicaReading([service({})])).toBeUndefined()
    expect(replicaReading(undefined)).toBeUndefined()
  })

  test("counts the live release's healthy replicas against its own count", () => {
    const services = [
      service({ containerId: "a", replica: 1, replicas: 3 }),
      service({ containerId: "b", replica: 2, replicas: 3, health: "unhealthy" }),
      service({ containerId: "c", replica: 3, replicas: 3, state: "exited" }),
      // The warm release's replicas are not the live release's.
      service({ containerId: "d", releaseId: 2, liveRelease: false, replica: 1, replicas: 2 }),
    ]
    expect(replicaReading(services)).toEqual({ healthy: 1, total: 3 })
    // A replica Docker no longer lists still counts against the release.
    expect(replicaReading(services.slice(0, 1))).toEqual({ healthy: 1, total: 3 })
  })
})
