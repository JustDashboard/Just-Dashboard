import { describe, expect, test } from "bun:test"
import { crc32, entryName, zipOf } from "./zip"

const text = (bytes) => new TextDecoder().decode(bytes)

/** Reads an archive back by its own directory: each entry's name and content. */
function readZip(bytes) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const end = bytes.length - 22
  expect(view.getUint32(end, true)).toBe(0x06054b50)
  const count = view.getUint16(end + 10, true)
  let at = view.getUint32(end + 16, true)
  const files = []
  for (let n = 0; n < count; n++) {
    expect(view.getUint32(at, true)).toBe(0x02014b50)
    const crc = view.getUint32(at + 16, true)
    const size = view.getUint32(at + 24, true)
    const nameLength = view.getUint16(at + 28, true)
    const local = view.getUint32(at + 42, true)
    const name = text(bytes.subarray(at + 46, at + 46 + nameLength))
    expect(view.getUint32(local, true)).toBe(0x04034b50)
    // Stored, not compressed: the two sizes are one.
    expect(view.getUint16(local + 8, true)).toBe(0)
    expect(view.getUint32(local + 18, true)).toBe(size)
    const start = local + 30 + view.getUint16(local + 26, true) + view.getUint16(local + 28, true)
    const content = bytes.subarray(start, start + size)
    expect(crc32(content)).toBe(crc)
    files.push({ name, content: text(content) })
    at += 46 + nameLength
  }
  expect(at).toBe(end)
  return files
}

describe("several files as one archive", () => {
  test("the checksum is the one every ZIP tool computes", () => {
    expect(crc32(new TextEncoder().encode("123456789"))).toBe(0xcbf43926)
    expect(crc32(new Uint8Array())).toBe(0)
  })
  test("each file is in it, whole, under its own name", () => {
    const files = [
      { filename: "index.ts", content: 'export * from "./Order"\n' },
      { filename: "Order.ts", content: "export class Order {\n  // naïve — ö\n}\n" },
      { filename: "empty.ts", content: "" },
    ]
    expect(readZip(zipOf(files, new Date(2026, 9, 2, 12, 30, 10)))).toEqual(
      files.map((file) => ({ name: file.filename, content: file.content })),
    )
  })
  test("no file leaves the folder the archive is opened in", () => {
    expect(entryName("../../etc/passwd")).toBe("etc/passwd")
    expect(entryName("/abs/Order.ts")).toBe("abs/Order.ts")
    expect(entryName("a\\b.ts")).toBe("a/b.ts")
    expect(entryName("..")).toBe("file")
  })
  test("two files of one name are both kept", () => {
    const names = readZip(
      zipOf([
        { filename: "models.go", content: "a" },
        { filename: "models.go", content: "b" },
        { filename: "Makefile", content: "c" },
        { filename: "Makefile", content: "d" },
      ]),
    ).map((file) => file.name)
    expect(names).toEqual(["models.go", "models-2.go", "Makefile", "Makefile-2"])
  })
  test("no files is still an archive", () => {
    expect(readZip(zipOf([]))).toEqual([])
  })
})
