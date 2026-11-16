import { expect, test } from "bun:test"
import { exportBlob, exportName, streamExport } from "./stream-export"

const MIB = 1 << 20

/** A response whose body sends these chunks, then ends, or breaks with `fail`. */
function response(chunks, { fail, headers } = {}) {
  let at = 0
  const body = new ReadableStream({
    pull(controller) {
      if (at < chunks.length) {
        controller.enqueue(chunks[at++])
        return
      }
      if (fail) controller.error(fail)
      else controller.close()
    },
  })
  return new Response(body, { headers })
}

test("the whole body is read, with a word each megabyte", async () => {
  const said = []
  const read = await streamExport(
    response([new Uint8Array(MIB / 2), new Uint8Array(MIB / 2), new Uint8Array(MIB + 1)]),
    { progress: (received) => said.push(received) },
  )
  expect(read.cut).toBe(false)
  expect(read.received).toBe(2 * MIB + 1)
  expect(read.parts).toHaveLength(3)
  // Not after the first half; at one megabyte; then at the next whole one past it.
  expect(said).toEqual([MIB, 2 * MIB + 1])
})

test("a connection the server broke is a cut export, with what came kept", async () => {
  const read = await streamExport(response([new Uint8Array(10)], { fail: new Error("reset") }))
  expect(read.cut).toBe(true)
  expect(read.received).toBe(10)
})

test("a reader who cancelled is not a cut: the read throws", async () => {
  const controller = new AbortController()
  controller.abort()
  const broken = streamExport(response([new Uint8Array(1)], { fail: new Error("aborted") }), {
    signal: controller.signal,
  })
  await expect(broken).rejects.toThrow("aborted")
})

test("a response with no body is refused", async () => {
  await expect(streamExport(new Response(null))).rejects.toThrow("no body")
})

test("the file takes the server's name, and its type", async () => {
  const named = new Response("", {
    headers: {
      "Content-Disposition": 'attachment; filename="orders.csv"',
      "Content-Type": "text/csv",
    },
  })
  expect(exportName(named, "fallback.csv")).toBe("orders.csv")
  expect(exportName(new Response(""), "fallback.csv")).toBe("fallback.csv")
  expect(exportBlob(["a"], named).type).toStartWith("text/csv")
  expect(exportBlob(["a"], new Response(null)).type).toBe("application/octet-stream")
})
