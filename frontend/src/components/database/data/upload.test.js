import { afterEach, expect, test } from "bun:test"
import { uploadForm } from "./upload"

const originalXHR = globalThis.XMLHttpRequest
const originalWindow = globalThis.window
afterEach(() => {
  if (originalXHR === undefined) delete globalThis.XMLHttpRequest
  else globalThis.XMLHttpRequest = originalXHR
  if (originalWindow === undefined) delete globalThis.window
  else globalThis.window = originalWindow
})

function fixture(status, code) {
  const events = []
  globalThis.window = new EventTarget()
  window.addEventListener("jd:authentication-lost", () => events.push("authentication-lost"))
  const requests = []
  globalThis.XMLHttpRequest = class {
    status = status
    statusText = "Refused"
    responseText = JSON.stringify({ error: { code, message: "Request refused" } })
    headers = {}
    upload = {}
    open(method, url) {
      this.method = method
      this.url = url
      requests.push(this)
    }
    setRequestHeader(name, value) {
      this.headers[name] = value
    }
    send(body) {
      this.body = body
      this.upload.onprogress?.({ lengthComputable: true, loaded: 50, total: 100 })
      queueMicrotask(() => !this.aborted && this.onload())
    }
    abort() {
      this.aborted = true
      this.onabort()
    }
  }
  return { events, request: () => requests.at(-1) }
}

test("an expired XHR database import reports auth loss with its normal progress and mutation headers", async () => {
  const { events, request } = fixture(401, "unauthorized")
  const progress = []
  const body = new FormData()
  await expect(
    uploadForm("/databases/1/import", body, {
      onProgress: (sent, total) => progress.push([sent, total]),
    }),
  ).rejects.toMatchObject({ status: 401, code: "unauthorized" })
  expect(events).toEqual(["authentication-lost"])
  expect(request().withCredentials).toBe(true)
  expect(request().headers["X-JD-CSRF"]).toBe("1")
  expect(request().body).toBe(body)
  expect(progress).toEqual([[50, 100]])
})

test("XHR credentials and transient import errors do not end a session", async () => {
  for (const [status, code] of [
    [401, "invalid_credentials"],
    [503, "not_available"],
  ]) {
    const { events } = fixture(status, code)
    await expect(uploadForm("/databases/5/mongo/import", new FormData())).rejects.toMatchObject({
      code,
    })
    expect(events).toEqual([])
  }
})

test("cancelling an XHR import aborts the real request without session recovery", async () => {
  const { events, request } = fixture(401, "unauthorized")
  const controller = new AbortController()
  const result = uploadForm("/databases/1/import", new FormData(), { signal: controller.signal })
  controller.abort()
  await expect(result).rejects.toMatchObject({ name: "AbortError" })
  expect(request().aborted).toBe(true)
  expect(events).toEqual([])
})
