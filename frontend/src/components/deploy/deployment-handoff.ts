// A route transition must not discard values someone already typed. Keep only
// the current handoff in memory; secrets never enter URLs or browser storage.
let handoff: { draftId: string; dotenv: string } | undefined

export function setDeploymentHandoff(draftId: string, dotenv: string) {
  handoff = { draftId, dotenv }
}

export function getDeploymentHandoff(draftId: string) {
  return handoff?.draftId === draftId ? handoff.dotenv : undefined
}

export function clearDeploymentHandoff(draftId: string) {
  if (handoff?.draftId === draftId) handoff = undefined
}
