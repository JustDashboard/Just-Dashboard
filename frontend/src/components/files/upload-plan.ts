import { cleanPath, joinPath, numberedName } from "./media"
import type { Incoming, UploadJob } from "./uploads"
import type { ConflictPolicy } from "./conflict-dialog"

type Entry = { name: string; isDir: boolean; isSymlink?: boolean }
type Folder = Map<string, Entry>

/** Read every destination before creating anything, including nested folders. */
export async function inspectUpload(
  incoming: Incoming[],
  destination: string,
  list: (path: string) => Promise<Entry[]>,
) {
  const folders = new Map<string, Folder>()
  const create: string[] = []
  const merge: string[] = []
  folders.set("", new Map((await list(destination)).map((entry) => [entry.name, entry])))
  for (const item of incoming) {
    const parts = item.rel.split("/")
    if (parts.some((part) => !part || part === "." || part === "..")) {
      throw new Error("The upload contains an invalid relative path.")
    }
    parts.pop()
    let parent = ""
    for (const name of parts) {
      const relative = parent ? `${parent}/${name}` : name
      if (!folders.has(relative)) {
        const existing = folders.get(parent)!.get(name)
        if (existing && (!existing.isDir || existing.isSymlink)) {
          throw new Error(
            `${relative} is a file or a symbolic link, so it cannot receive a folder upload.`,
          )
        }
        if (existing) {
          merge.push(relative)
          folders.set(
            relative,
            new Map(
              (await list(joinPath(destination, relative))).map((entry) => [entry.name, entry]),
            ),
          )
        } else {
          create.push(relative)
          folders.set(relative, new Map())
          folders.get(parent)!.set(name, { name, isDir: true })
        }
      }
      parent = relative
    }
  }
  const occupied = new Map([...folders].map(([path, entries]) => [path, new Set(entries.keys())]))
  const clashes: string[] = []
  for (const item of incoming) {
    const cut = item.rel.lastIndexOf("/")
    const parent = cut < 0 ? "" : item.rel.slice(0, cut)
    const name = item.rel.slice(cut + 1)
    const taken = occupied.get(parent)!
    if (taken.has(name)) clashes.push(item.rel)
    taken.add(name)
  }
  return {
    create,
    merge,
    clashes: [...new Set(clashes)],
    jobs(policy: ConflictPolicy): UploadJob[] {
      const names = new Map([...folders].map(([path, entries]) => [path, new Set(entries.keys())]))
      const jobs: UploadJob[] = []
      const assigned = new Set<string>()
      for (const item of incoming) {
        const cut = item.rel.lastIndexOf("/")
        const parent = cut < 0 ? "" : item.rel.slice(0, cut)
        let name = item.rel.slice(cut + 1)
        const taken = names.get(parent)!
        let overwrite = false
        if (taken.has(name)) {
          if (policy === "skip") continue
          if (policy === "replace") {
            if (assigned.has(item.rel))
              throw new Error(
                `${item.rel} appears more than once. Choose Keep both or Skip to handle the duplicate files.`,
              )
            if (folders.get(parent)!.get(name)?.isDir) {
              throw new Error(`${item.rel} is a folder. Choose Keep both or Skip to preserve it.`)
            }
            overwrite = true
          } else name = numberedName(name, taken)
        }
        taken.add(name)
        assigned.add(item.rel)
        jobs.push({
          file: item.file,
          dir: parent ? joinPath(destination, parent) : cleanPath(destination),
          name,
          overwrite,
        })
      }
      return jobs
    },
  }
}
