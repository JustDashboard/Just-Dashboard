import { afterEach, expect, test } from "bun:test"
import { get, post } from "./api"

const originalFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = originalFetch
})

function limited(retryAfter) {
  return new Response(
    JSON.stringify({ error: { code: "rate_limited", message: "too many requests, slow down" } }),
    {
      status: 429,
      headers: {
        "Content-Type": "application/json",
        ...(retryAfter ? { "Retry-After": retryAfter } : {}),
      },
    },
  )
}

test("a read the rate limit refused is asked again after the wait the server named", async () => {
  let calls = 0
  globalThis.fetch = async () => {
    calls++
    return calls === 1 ? limited("1") : Response.json({ ok: true })
  }
  const started = Date.now()
  expect(await get("/system/host")).toEqual({ ok: true })
  expect(calls).toBe(2)
  expect(Date.now() - started).toBeGreaterThanOrEqual(990)
})

test("a read is asked again once only, and only for a short wait", async () => {
  let calls = 0
  globalThis.fetch = async () => {
    calls++
    return limited("1")
  }
  const twice = await get("/system/host").catch((e) => e)
  expect(twice.status).toBe(429)
  expect(twice.code).toBe("rate_limited")
  expect(calls).toBe(2)

  for (const retryAfter of ["60", undefined, "soon"]) {
    calls = 0
    globalThis.fetch = async () => {
      calls++
      return limited(retryAfter)
    }
    const error = await get("/system/host").catch((e) => e)
    expect(error.code).toBe("rate_limited")
    expect(calls).toBe(1)
  }
})

test("a write the rate limit refused is not sent again behind the person's back", async () => {
  let calls = 0
  globalThis.fetch = async () => {
    calls++
    return limited("1")
  }
  const error = await post("/docker/containers/example/restart").catch((e) => e)
  expect(error.status).toBe(429)
  expect(calls).toBe(1)
})

test("a read aborted while it waits stops waiting", async () => {
  let calls = 0
  const controller = new AbortController()
  globalThis.fetch = async (_url, init) => {
    calls++
    if (init?.signal?.aborted) throw new DOMException("aborted", "AbortError")
    return limited("5")
  }
  setTimeout(() => controller.abort(), 30)
  const started = Date.now()
  const error = await get("/system/host", undefined, controller.signal).catch((e) => e)
  expect(error.name).toBe("AbortError")
  expect(Date.now() - started).toBeLessThan(2000)
  // The fetch that follows the wait is handed the aborted signal and rejects at once.
  expect(calls).toBe(2)
})
