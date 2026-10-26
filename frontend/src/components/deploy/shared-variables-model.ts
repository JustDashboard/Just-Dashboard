import type { DeploymentSharedVariable, DeploymentSharedVariableUse } from "@/lib/types"

/**
 * Where a shared variable lives: the server's own, read by every project, or
 * one project's, read by that project's environments alone.
 */
export type SharedScope = { kind: "server" } | { kind: "project"; projectId: number }

/** The server's rule for a shared name: a reference's target starts with a letter. */
export const SHARED_NAME = /^[A-Za-z][A-Za-z0-9_]{0,127}$/

export function sharedBase(scope: SharedScope) {
  return scope.kind === "server"
    ? "/deploy/shared-variables"
    : `/deploy/${scope.projectId}/shared-variables`
}

/** The literal an environment's variable holds to read `name` — the one the server answers with. */
export function sharedReference(kind: SharedScope["kind"], name: string) {
  return `\${{${kind}.${name}}}`
}

/** A typed reference to a shared variable, or nothing for any other value. */
export function parseSharedReference(
  value: string,
): { kind: SharedScope["kind"]; name: string } | undefined {
  const match = /^\$\{\{(server|project)\.([A-Za-z][A-Za-z0-9_]{0,127})\}\}$/.exec(value.trim())
  return match ? { kind: match[1] as SharedScope["kind"], name: match[2] } : undefined
}

/**
 * The variables reading one shared variable, a project at a time — the
 * project once, then each of its environments' variables — so a value read
 * in production and a preview says the project's name once.
 */
export function readersByProject(uses: DeploymentSharedVariableUse[]) {
  const projects = new Map<
    number,
    { projectId: number; projectName: string; uses: DeploymentSharedVariableUse[] }
  >()
  for (const use of uses) {
    const entry = projects.get(use.projectId) ?? {
      projectId: use.projectId,
      projectName: use.projectName,
      uses: [],
    }
    entry.uses.push(use)
    projects.set(use.projectId, entry)
  }
  return [...projects.values()]
}

/** How many of a variable's readers run an earlier value until they deploy again. */
export function waitingReaders(variable: Pick<DeploymentSharedVariable, "usedBy">) {
  return variable.usedBy.filter((use) => use.waiting).length
}

/**
 * What the remove dialog says before the server is asked: the server refuses
 * while anything reads the variable, and its refusal names who, so the
 * sentence says the same rather than offering a remove that cannot happen.
 */
export function removalNote(variable: Pick<DeploymentSharedVariable, "usedBy" | "reference">) {
  const readers = variable.usedBy.length
  if (readers === 0) return `Nothing reads ${variable.reference}; removing it changes no release.`
  return `${readers === 1 ? "One variable reads" : `${readers} variables read`} ${variable.reference}. The server refuses to remove it until ${readers === 1 ? "that variable points" : "they point"} elsewhere.`
}
