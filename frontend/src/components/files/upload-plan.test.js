import { expect, test } from "bun:test"
import { inspectUpload } from "./upload-plan"

const incoming = (rel) => ({ rel, file: new File([rel], rel.split("/").pop()) })
const directory = (name) => ({ name, isDir: true })
const file = (name) => ({ name, isDir: false })

test("recursive collisions use each destination folder's own names and policy", async () => {
  const calls = []
  const tree = {
    "/root": [directory("photos"), directory("docs")],
    "/root/photos": [file("a.jpg"), file("a (2).jpg")],
    "/root/docs": [file("a.jpg")],
  }
  const plan = await inspectUpload(
    [
      incoming("photos/a.jpg"),
      incoming("photos/new.jpg"),
      incoming("docs/a.jpg"),
      incoming("new/deep/a.jpg"),
    ],
    "/root",
    async (path) => {
      calls.push(path)
      return tree[path]
    },
  )
  expect(calls).toEqual(["/root", "/root/photos", "/root/docs"])
  expect(plan.merge).toEqual(["photos", "docs"])
  expect(plan.clashes).toEqual(["photos/a.jpg", "docs/a.jpg"])
  expect(plan.create).toEqual(["new", "new/deep"])
  expect(plan.jobs("keep").map(({ dir, name, overwrite }) => [dir, name, overwrite])).toEqual([
    ["/root/photos", "a (3).jpg", false],
    ["/root/photos", "new.jpg", false],
    ["/root/docs", "a (2).jpg", false],
    ["/root/new/deep", "a.jpg", false],
  ])
  expect(plan.jobs("skip").map((job) => job.name)).toEqual(["new.jpg", "a.jpg"])
  expect(plan.jobs("replace").map((job) => job.overwrite)).toEqual([true, false, true, false])
})

test("a file or symlink blocking a directory fails discovery before any mutation", async () => {
  for (const entry of [file("photos"), { ...directory("photos"), isSymlink: true }]) {
    await expect(
      inspectUpload([incoming("photos/a.jpg")], "/root", async () => [entry]),
    ).rejects.toThrow("cannot receive a folder upload")
  }
})

test("duplicate incoming names and existing directory targets stay safe", async () => {
  const plan = await inspectUpload([incoming("a.txt"), incoming("a.txt")], "/root", async () => [])
  expect(plan.clashes).toEqual(["a.txt"])
  expect(plan.jobs("keep").map((job) => job.name)).toEqual(["a.txt", "a (2).txt"])
  const occupied = await inspectUpload([incoming("photos")], "/root", async () => [
    directory("photos"),
  ])
  expect(() => occupied.jobs("replace")).toThrow("Choose Keep both or Skip")
})
