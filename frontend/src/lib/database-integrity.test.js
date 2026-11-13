import { describe, expect, test } from "bun:test"
import { parseDockerRun } from "./docker-run"
import { api, ApiError } from "./api"

describe("docker command parsing", () => {
  test("explicit false cannot enable privileges or removal", () => {
    const parsed = parseDockerRun(
      "docker run --privileged --privileged=false --rm=false --tty=false --init=0 --read-only=false alpine",
    )
    expect(parsed.spec).toMatchObject({
      privileged: false,
      autoRemove: false,
      tty: false,
      init: false,
      readOnlyRootfs: false,
    })
  })
  test("unrepresentable pull policy and invalid booleans are disclosed", () => {
    const parsed = parseDockerRun("docker run --pull=never --privileged=maybe alpine")
    expect(parsed.unsupported).toEqual(["--pull=never", "--privileged=maybe"])
    expect(parsed.spec.privileged).toBeUndefined()
    expect(parsed.warnings).toHaveLength(2)
  })
})

test("confirmations preserve Unicode, whitespace, and literal percent signs in valid headers", async () => {
  const originalFetch = globalThis.fetch
  try {
    let headers
    globalThis.fetch = async (_, options) => {
      headers = new Headers(options.headers)
      return new Response(null, { status: 204 })
    }
    const phrase = "  日誌 %2F café\n "
    await api("/files/", { method: "DELETE", confirm: phrase })
    expect(headers.get("X-Confirm-Encoding")).toBe("uri")
    expect(decodeURIComponent(headers.get("X-Confirm"))).toBe(phrase)
    expect(headers.get("X-JD-CSRF")).toBe("1")
    await api("/files/", { method: "DELETE", confirm: "" })
    expect(headers.get("X-Confirm")).toBeNull()
  } finally {
    globalThis.fetch = originalFetch
  }
  expect(new ApiError(403, "password_change_required", "change password").isAuthProblem).toBe(true)
})
