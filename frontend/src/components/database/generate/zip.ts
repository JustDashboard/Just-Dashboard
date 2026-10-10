/**
 * Several generated files as one archive.
 *
 * A generator that writes a file per model hands over nine files, and nine
 * downloads are nine prompts in a browser that asks before each. The files are
 * text and small, so they are packed as they are — stored, not compressed —
 * which is a ZIP any tool opens and needs nothing but a checksum to write.
 */

export interface ArchiveFile {
  filename: string
  content: string
}

let table: Uint32Array | undefined

/** CRC-32 (IEEE), the checksum a ZIP entry carries. */
export function crc32(bytes: Uint8Array): number {
  if (!table) {
    table = new Uint32Array(256)
    for (let n = 0; n < 256; n++) {
      let c = n
      for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
      table[n] = c >>> 0
    }
  }
  let crc = 0xffffffff
  for (const byte of bytes) crc = table[(crc ^ byte) & 0xff] ^ (crc >>> 8)
  return (crc ^ 0xffffffff) >>> 0
}

/** A file's name as an archive carries it: forward slashes, no way out of the folder it is opened in. */
export function entryName(filename: string): string {
  const parts = filename
    .replace(/\\/g, "/")
    .split("/")
    .filter((part) => part !== "" && part !== "." && part !== "..")
  return parts.join("/") || "file"
}

/** MS-DOS date and time, which is how a ZIP entry says when it was written. */
function dosStamp(at: Date): { time: number; date: number } {
  return {
    time: (at.getHours() << 11) | (at.getMinutes() << 5) | (at.getSeconds() >> 1),
    date: (Math.max(0, at.getFullYear() - 1980) << 9) | ((at.getMonth() + 1) << 5) | at.getDate(),
  }
}

/** The files as the bytes of a ZIP archive. Two files of one name are both kept, the later one renamed. */
export function zipOf(files: readonly ArchiveFile[], at = new Date()): Uint8Array {
  const encoder = new TextEncoder()
  const { time, date } = dosStamp(at)
  const taken = new Set<string>()
  const entries = files.map((file) => {
    let name = entryName(file.filename)
    for (let copy = 2; taken.has(name); copy++) {
      const dot = entryName(file.filename).lastIndexOf(".")
      const base = entryName(file.filename)
      name = dot > 0 ? `${base.slice(0, dot)}-${copy}${base.slice(dot)}` : `${base}-${copy}`
    }
    taken.add(name)
    const data = encoder.encode(file.content)
    return { name: encoder.encode(name), data, crc: crc32(data) }
  })

  const size =
    entries.reduce(
      (total, entry) => total + 30 + 46 + entry.name.length * 2 + entry.data.length,
      0,
    ) + 22
  const out = new Uint8Array(size)
  const view = new DataView(out.buffer)
  let offset = 0
  const offsets: number[] = []
  // Bit 11 of the flags: the names are UTF-8.
  const FLAGS = 0x0800
  for (const entry of entries) {
    offsets.push(offset)
    view.setUint32(offset, 0x04034b50, true)
    view.setUint16(offset + 4, 20, true)
    view.setUint16(offset + 6, FLAGS, true)
    view.setUint16(offset + 8, 0, true)
    view.setUint16(offset + 10, time, true)
    view.setUint16(offset + 12, date, true)
    view.setUint32(offset + 14, entry.crc, true)
    view.setUint32(offset + 18, entry.data.length, true)
    view.setUint32(offset + 22, entry.data.length, true)
    view.setUint16(offset + 26, entry.name.length, true)
    view.setUint16(offset + 28, 0, true)
    out.set(entry.name, offset + 30)
    out.set(entry.data, offset + 30 + entry.name.length)
    offset += 30 + entry.name.length + entry.data.length
  }
  const directory = offset
  entries.forEach((entry, index) => {
    view.setUint32(offset, 0x02014b50, true)
    view.setUint16(offset + 4, 20, true)
    view.setUint16(offset + 6, 20, true)
    view.setUint16(offset + 8, FLAGS, true)
    view.setUint16(offset + 10, 0, true)
    view.setUint16(offset + 12, time, true)
    view.setUint16(offset + 14, date, true)
    view.setUint32(offset + 16, entry.crc, true)
    view.setUint32(offset + 20, entry.data.length, true)
    view.setUint32(offset + 24, entry.data.length, true)
    view.setUint16(offset + 28, entry.name.length, true)
    // No extra field, no comment, disk 0, no attributes.
    view.setUint32(offset + 42, offsets[index], true)
    out.set(entry.name, offset + 46)
    offset += 46 + entry.name.length
  })
  view.setUint32(offset, 0x06054b50, true)
  view.setUint16(offset + 8, entries.length, true)
  view.setUint16(offset + 10, entries.length, true)
  view.setUint32(offset + 12, offset - directory, true)
  view.setUint32(offset + 16, directory, true)
  return out
}

/** Hands the reader the files as one archive of the given name. */
export function downloadArchive(files: readonly ArchiveFile[], filename: string) {
  const bytes = zipOf(files)
  const url = URL.createObjectURL(
    new Blob([bytes.buffer as ArrayBuffer], { type: "application/zip" }),
  )
  const link = document.createElement("a")
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}
