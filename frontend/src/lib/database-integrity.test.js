import { describe, expect, test } from "bun:test"
import { coerceDbValue } from "./db-values"
import { resultToCSV } from "./db-export"
import { parseDockerRun } from "./docker-run"
import { api, ApiError } from "./api"

describe("lossless database values", () => {
  test("exact SQL types retain all entered digits through JSON", () => {
    for (const type of [
      "bigint",
      "BIGINT UNSIGNED",
      "bigserial",
      "int8",
      "numeric(30,10)",
      "decimal",
    ])
      expect(JSON.parse(JSON.stringify(coerceDbValue("9007199254740993", type)))).toBe(
        "9007199254740993",
      )
    expect(coerceDbValue("1.123456789012345678901234567890", "numeric")).toBe(
      "1.123456789012345678901234567890",
    )
    expect(coerceDbValue("3", "integer")).toBe("3")
    expect(coerceDbValue("false", "boolean")).toBe(false)
  })
  test("non-finite and unsafe ordinary numbers never turn into null or rounded JSON", () => {
    expect(() => coerceDbValue("1e999", "double precision")).toThrow("finite range")
    expect(coerceDbValue("9007199254740993", "integer")).toBe("9007199254740993")
    expect(coerceDbValue("", "float")).toBe("")
  })
  test("CSV escapes headers and carriage returns as well as data", () => {
    expect(
      resultToCSV({ columns: ["a,b", 'quote"name', "line\rname"], rows: [["a\rb", 'x"y', null]] }),
    ).toBe('"a,b","quote""name","line\rname"\n"a\rb","x""y",')
  })
})

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
  } finally {
    globalThis.fetch = originalFetch
  }
  expect(new ApiError(403, "password_change_required", "change password").isAuthProblem).toBe(true)
})
