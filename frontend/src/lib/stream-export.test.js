import { afterEach, expect, test } from "bun:test"
import { exportBlob, exportName, saveExport, settledExport, streamExport } from "./stream-export"

const originalFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = originalFetch
})

/** A fetch that answers only once it is let go, and fails as the browser's does when aborted. */
function heldFetch(answer) {
  const asked = []
  let release = () => {}
  const until = new Promise((resolve) => {
    release = resolve
  })
  globalThis.fetch = (url, init = {}) => {
    asked.push(String(url))
    return new Promise((resolve, reject) => {
      const signal = init.signal
      if (signal?.aborted) return reject(signal.reason)
      signal?.addEventListener("abort", () => reject(signal.reason), { once: true })
      void until.then(() => resolve(Response.json(answer())))
    })
  }
  return { asked, release }
}

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

test("Cancel pressed while the export's ending is being asked throws, rather than answering", async () => {
  const held = heldFetch(() => ({ status: "complete", rows: 1, format: "csv" }))
  const controller = new AbortController()
  const settled = settledExport(1, "e-1", controller.signal)
  await Bun.sleep(0)
  expect(held.asked).toHaveLength(1)
  controller.abort()
  held.release()
  await expect(settled).rejects.toMatchObject({ name: "AbortError" })
})

test("Cancel pressed between two asks of a running export throws at once", async () => {
  const held = heldFetch(() => ({ status: "running", rows: 0, format: "csv" }))
  held.release()
  const controller = new AbortController()
  const settled = settledExport(1, "e-2", controller.signal)
  await Bun.sleep(20)
  controller.abort()
  const started = Date.now()
  await expect(settled).rejects.toMatchObject({ name: "AbortError" })
  // Not after the half-second wait between asks.
  expect(Date.now() - started).toBeLessThan(200)
  expect(held.asked).toHaveLength(1)
})

test("a status read that fails on its own is no record, not a cancel", async () => {
  globalThis.fetch = async () => new Response("", { status: 502 })
  expect(await settledExport(1, "e-3", new AbortController().signal)).toBeNull()
})

test("a cancelled export saves nothing", () => {
  const controller = new AbortController()
  controller.abort()
  let thrown
  try {
    saveExport(new Blob(["a"]), new Response(""), "items.csv", controller.signal)
  } catch (err) {
    thrown = err
  }
  // The cancel itself, before the browser is touched: not a save that failed.
  expect(thrown?.name).toBe("AbortError")
})
