import { plural } from "@/lib/format"
import type { DeploymentSharedVariable } from "@/lib/types"

/**
 * What the server's Shared variables page says about the list as a whole,
 * kept apart from the page so it is tested in `bun test src`: the count on
 * the section's head, and the projects its picture draws.
 */

type Read = Pick<DeploymentSharedVariable, "name" | "usedBy">

/** A variable some reader still runs an earlier value of. */
export function notLiveYet(variable: Pick<DeploymentSharedVariable, "usedBy">) {
  return variable.usedBy.some((use) => use.waiting)
}

/** The section head's count: "4 variables · 2 not live yet". */
export function sharedCount(variables: Pick<DeploymentSharedVariable, "usedBy">[]): string {
  const waiting = variables.filter(notLiveYet).length
  return [plural(variables.length, "variable"), waiting > 0 && `${waiting} not live yet`]
    .filter(Boolean)
    .join(" · ")
}

export type SharedReader = {
  projectId: number
  projectName: string
  /** How many of its environments' variables read a shared one. */
  variables: number
  /** The shared variables it reads, by name. */
  reads: string[]
  /** How many of those readers run an earlier value until they deploy again. */
  waiting: number
}

/**
 * The projects that read the shared variables, each once with how many of
 * its variables do, in the order the variables first name them.
 */
export function sharedReaders(variables: Read[]): SharedReader[] {
  const readers = new Map<number, SharedReader>()
  for (const variable of variables) {
    for (const use of variable.usedBy) {
      const reader = readers.get(use.projectId) ?? {
        projectId: use.projectId,
        projectName: use.projectName,
        variables: 0,
        reads: [],
        waiting: 0,
      }
      reader.variables += 1
      if (!reader.reads.includes(variable.name)) reader.reads.push(variable.name)
      if (use.waiting) reader.waiting += 1
      readers.set(use.projectId, reader)
    }
  }
  return [...readers.values()]
}
