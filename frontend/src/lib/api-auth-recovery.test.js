import { afterEach, expect, test } from "bun:test"
import { errorFromResponse, fetchResponse, get, post, postForm, getText, postFile } from "./api"

const originalFetch = globalThis.fetch
const originalWindow = globalThis.window
afterEach(() => {
  globalThis.fetch = originalFetch
  if (originalWindow === undefined) delete globalThis.window
  else globalThis.window = originalWindow
})

function refusal(status, code) {
  const events = []
  const target = new EventTarget()
  target.addEventListener("jd:authentication-lost", () => events.push("authentication-lost"))
  globalThis.window = target
  globalThis.fetch = async () =>
    new Response(JSON.stringify({ error: { code, message: "Request refused" } }), {
      status,
      headers: { "Content-Type": "application/json" },
    })
  return events
}

test("expired sessions signal recovery through every authenticated response path", async () => {
  const events = refusal(401, "unauthorized")
  for (const request of [
    () => get("/tokens/"),
    () => post("/docker/containers/example/start"),
    () => postForm("/files/upload", new FormData()),
    () => getText("/git/transcript"),
    () => postFile("/databases/1/export/query", {}),
    () => fetchResponse("/databases/1/export/query", { method: "POST", body: "{}" }),
    () => fetchResponse("/databases/1/export", {}, { table: "items" }),
    () => fetchResponse("/databases/5/mongo/export", {}, { collection: "orders" }),
    () => fetchResponse("/files/raw", {}, { path: "/srv/logo.png", revision: true }),
  ]) {
    await expect(request()).rejects.toMatchObject({ status: 401, code: "unauthorized" })
  }
  expect(events).toHaveLength(9)
})

test("bad passwords, rejected codes and unavailable services do not end a session", async () => {
  for (const [status, code, path] of [
    [401, "invalid_credentials", "/account/password"],
    [401, "invalid_code", "/auth/2fa/verify"],
    [401, "unauthorized", "/auth/session"],
    [503, "not_available", "/docker/ping"],
  ]) {
    const events = refusal(status, code)
    await expect(post(path)).rejects.toMatchObject({ code })
    expect(events).toEqual([])
  }
})

test("a disabled account or outstanding factor revalidates feature access", async () => {
  for (const [status, code] of [
    [403, "account_disabled"],
    [401, "totp_required"],
    [401, "totp_enrollment_required"],
  ]) {
    const events = refusal(status, code)
    await expect(get("/tokens/")).rejects.toMatchObject({ code })
    expect(events).toEqual(["authentication-lost"])
  }
})

test("stream readers preserve successful bodies, headers, and cancellation", async () => {
  const controller = new AbortController()
  let captured
  let cancelled = false
  const stream = new ReadableStream({
    start(output) {
      output.enqueue(new Uint8Array([0, 255, 128]))
    },
    cancel() {
      cancelled = true
    },
  })
  const original = new Response(stream, {
    headers: {
      "Content-Type": "application/octet-stream",
      "Content-Disposition": "attachment; filename=rows.csv",
    },
  })
  globalThis.fetch = async (url, options) => {
    captured = { url, options }
    return original
  }
  const response = await fetchResponse("/databases/1/export/query", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
    signal: controller.signal,
  })
  expect(response).toBe(original)
  expect(response.bodyUsed).toBe(false)
  expect(captured.options.signal).toBe(controller.signal)
  expect(captured.options.credentials).toBe("include")
  expect(captured.options.headers.get("X-JD-CSRF")).toBe("1")
  expect(response.headers.get("Content-Disposition")).toContain("rows.csv")
  const reader = response.body.getReader()
  expect((await reader.read()).value).toEqual(new Uint8Array([0, 255, 128]))
  await reader.cancel()
  expect(cancelled).toBe(true)
})

test("stream refusals preserve error details and distinguish password changes", async () => {
  const events = refusal(403, "password_change_required")
  window.addEventListener("jd:password-change-required", () => events.push("password-change"))
  await expect(fetchResponse("/databases/1/export")).rejects.toMatchObject({
    status: 403,
    code: "password_change_required",
  })
  expect(events).toEqual(["password-change"])
  for (const [status, code, path] of [
    [401, "invalid_credentials", "/databases/1/export/query"],
    [401, "unauthorized", "/auth/session"],
    [503, "not_available", "/databases/1/export"],
  ]) {
    const recovered = refusal(status, code)
    await expect(fetchResponse(path)).rejects.toMatchObject({ status, code })
    expect(recovered).toEqual([])
  }
})

test("XHR uploads use the same recognized authentication failures and error details", () => {
  for (const path of [
    "/files/upload",
    "/databases/1/import",
    "/databases/5/mongo/import",
    "/databases/1/backups/upload",
  ]) {
    const events = refusal(401, "unauthorized")
    const error = errorFromResponse(
      401,
      JSON.stringify({
        error: {
          code: "unauthorized",
          message: "Session expired",
          resource: "rows",
          operation: "import",
          reason: "Ended session",
          raw: "expired",
          retryable: false,
        },
      }),
      path,
    )
    expect(error).toMatchObject({
      code: "unauthorized",
      resource: "rows",
      operation: "import",
      reason: "Ended session",
      raw: "expired",
      retryable: false,
    })
    expect(events).toEqual(["authentication-lost"])
  }
  const events = refusal(503, "not_available")
  expect(errorFromResponse(503, "proxy unavailable", "/files/upload").message).toBe(
    "proxy unavailable",
  )
  expect(events).toEqual([])
})
