import { describe, expect, test } from "bun:test"
import { ApiError } from "@/lib/api"
import { sitesReadError, sitesReadFailure } from "./sites-read-failure"

const ingress = new ApiError(
  409,
  "ingress_ambiguous",
  "Two public Caddy containers and no word on which is the ingress",
  undefined,
  {
    reason: "Set JD_INGRESS_CONTAINER to the name of the one that serves this server's domains.",
    raw: "multiple public Caddy containers require an explicit ingress selection",
    retryable: true,
  },
)

describe("a failed read of the sites", () => {
  test("carries the reason and the subsystem's words where the server sent them", () => {
    expect(sitesReadFailure(ingress)).toEqual({
      message: "Two public Caddy containers and no word on which is the ingress",
      reason: "Set JD_INGRESS_CONTAINER to the name of the one that serves this server's domains.",
      raw: "multiple public Caddy containers require an explicit ingress selection",
    })
  })

  test("holds on an answer with neither", () => {
    expect(sitesReadFailure(new ApiError(502, "bad_gateway", "backend restarting"))).toEqual({
      message: "backend restarting",
    })
    expect(sitesReadFailure(new TypeError("Failed to fetch"))).toEqual({
      message: "Failed to fetch",
    })
  })

  test("points a bare 500 at the server's log, where there is an id to look up", () => {
    const bare = new ApiError(500, "internal", "internal error (request 4f2a)", undefined, {
      requestId: "4f2a",
    })
    expect(sitesReadFailure(bare).reason).toContain("request id")
    expect(sitesReadFailure(new ApiError(500, "internal", "internal error")).reason).toBeUndefined()
  })

  test("does not say one sentence twice", () => {
    const echoed = new ApiError(500, "ingress", "Docker did not answer", undefined, {
      reason: " Docker did not answer ",
      raw: "Docker did not answer",
    })
    expect(sitesReadFailure(echoed)).toEqual({ message: "Docker did not answer" })
  })
})

describe("the error a first failed read is drawn from", () => {
  test("is named for what was being read, and can always be asked again", () => {
    const bare = sitesReadError(new ApiError(500, "internal", "internal error"))
    expect(bare).toBeInstanceOf(ApiError)
    expect(bare).toMatchObject({
      status: 500,
      code: "internal",
      message: "internal error",
      operation: "read",
      resource: "the sites",
      retryable: true,
    })
    expect(sitesReadError(ingress)).toMatchObject({
      operation: "read",
      resource: "the sites",
      reason: ingress.reason,
      raw: ingress.raw,
    })
    expect(sitesReadError(new TypeError("Failed to fetch"))).toMatchObject({
      message: "Failed to fetch",
      operation: "read",
      retryable: true,
    })
  })

  test("leaves an absent module and an answer that names itself as they came", () => {
    const absent = new ApiError(503, "no_proxy", "no reverse proxy on this host")
    expect(sitesReadError(absent)).toBe(absent)
    const named = new ApiError(502, "ingress", "Docker did not answer", undefined, {
      operation: "ask",
      resource: "Docker for the ingress",
    })
    expect(sitesReadError(named)).toBe(named)
  })
})
