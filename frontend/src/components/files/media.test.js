import { describe, expect, test } from "bun:test"
import {
  isArchive,
  isWithin,
  joinPath,
  mediaKind,
  numberedName,
  parentOf,
  thumbnailKind,
  uniqueName,
} from "./media"

describe("mediaKind", () => {
  test("mirrors the server's inline allowlist by extension", () => {
    expect(mediaKind("photo.JPG")).toBe("image")
    expect(mediaKind("icon.svg")).toBe("image")
    expect(mediaKind("clip.webm")).toBe("video")
    expect(mediaKind("song.flac")).toBe("audio")
    expect(mediaKind("paper.pdf")).toBe("pdf")
    // HTML is deliberately not on it: served inline it would run as the dashboard.
    expect(mediaKind("index.html")).toBeNull()
    expect(mediaKind("nginx.conf")).toBeNull()
    expect(mediaKind("Makefile")).toBeNull()
  })

  test("only the archives the backend can extract count as extractable", () => {
    expect(isArchive("site.tar.gz")).toBe(true)
    expect(isArchive("site.zip")).toBe(true)
    expect(isArchive("site.7z")).toBe(false)
  })

  test("a thumbnail is tried for a small image or any video, never a folder", () => {
    const base = { isDir: false, size: 1024 }
    expect(thumbnailKind({ ...base, name: "a.png" })).toBe("image")
    expect(thumbnailKind({ ...base, name: "a.png", size: 40 << 20 })).toBeNull()
    expect(thumbnailKind({ ...base, name: "a.mp4", size: 40 << 20 })).toBe("video")
    expect(thumbnailKind({ ...base, name: "a.png", isDir: true })).toBeNull()
    expect(thumbnailKind({ ...base, name: "empty.png", size: 0 })).toBeNull()
  })
})

describe("names", () => {
  test("keep-both numbers a taken name the way a desktop does", () => {
    const taken = new Set(["photo.jpg", "photo (2).jpg"])
    expect(numberedName("photo.jpg", taken)).toBe("photo (3).jpg")
    expect(numberedName("new.jpg", taken)).toBe("new.jpg")
    expect(numberedName("README", new Set(["README"]))).toBe("README (2)")
  })

  test("a duplicate made on purpose is a copy beside its original", () => {
    const taken = new Set(["nginx.conf", "nginx copy.conf"])
    expect(uniqueName("nginx.conf", taken)).toBe("nginx copy 2.conf")
    expect(uniqueName(".env", new Set([".env"]))).toBe(".env copy")
  })
})

describe("paths", () => {
  test("containment treats the root as holding everything", () => {
    expect(isWithin("/home/op/a", "/home/op")).toBe(true)
    expect(isWithin("/home/op", "/home/op")).toBe(true)
    expect(isWithin("/home/operator", "/home/op")).toBe(false)
    expect(isWithin("/etc", "/")).toBe(true)
  })

  test("join and parent agree", () => {
    expect(joinPath("/", "a")).toBe("/a")
    expect(joinPath("/x/", "a")).toBe("/x/a")
    expect(parentOf("/x/a")).toBe("/x")
    expect(parentOf("/a")).toBe("/")
    expect(parentOf("/")).toBe("/")
  })
})
