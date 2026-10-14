import { afterEach, expect, test } from "bun:test"
import { get, post, postForm, getText, postFile } from "./api"

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
  ]) {
    await expect(request()).rejects.toMatchObject({ status: 401, code: "unauthorized" })
  }
  expect(events).toHaveLength(5)
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
