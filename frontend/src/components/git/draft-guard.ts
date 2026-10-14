// Drafts survive preview and route changes in memory. The unload guard must
// survive those changes too, until every repository draft is saved/discarded.
const dirtyRepositories = new Set<string>()
let listeningForReset = false

function beforeUnload(event: BeforeUnloadEvent) {
  if (dirtyRepositories.size === 0) return
  event.preventDefault()
  event.returnValue = ""
}

export function guardGitDrafts(repository: string, dirty: boolean) {
  if (!listeningForReset) {
    window.addEventListener("jd-working-state-cleared", () => {
      dirtyRepositories.clear()
      window.removeEventListener("beforeunload", beforeUnload)
    })
    listeningForReset = true
  }
  if (dirty) dirtyRepositories.add(repository)
  else dirtyRepositories.delete(repository)
  window.removeEventListener("beforeunload", beforeUnload)
  if (dirtyRepositories.size > 0) window.addEventListener("beforeunload", beforeUnload)
}
