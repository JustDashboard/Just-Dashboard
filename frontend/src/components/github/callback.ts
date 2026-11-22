import { post } from "@/lib/api"
import type { GitHubCallbackResult } from "@/lib/github-types"

/**
 * What GitHub sent the browser back with to `/github/callback`: a code and
 * the state this dashboard issued, or the refusal a press of Cancel on
 * GitHub gives (`error=access_denied`).
 */
export type GitHubReturn =
  | { kind: "code"; code: string; state: string }
  | { kind: "denied"; error: string; description?: string }
  | { kind: "empty" }

/** Read the return out of a query string. Pure, for the unit test. */
export function readReturn(search: string): GitHubReturn {
  const query = new URLSearchParams(search)
  const error = query.get("error")
  if (error) {
    return { kind: "denied", error, description: query.get("error_description") ?? undefined }
  }
  const code = query.get("code")
  const state = query.get("state")
  if (code && state) return { kind: "code", code, state }
  return { kind: "empty" }
}

/**
 * The one exchange per page load. The code is single use, so it must be
 * posted exactly once however often the page mounts: React's strict mode
 * mounts twice, and a remount after the address was cleaned would read
 * nothing and say the sign-in was lost. The first call takes the return off
 * the address (a reload or a shared link cannot replay it) and starts the
 * post; every later call gets the same promise.
 */
let taken: { found: GitHubReturn; result?: Promise<GitHubCallbackResult> } | undefined

export function takeGitHubReturn(): {
  found: GitHubReturn
  result?: Promise<GitHubCallbackResult>
} {
  if (taken) return taken
  const found = readReturn(window.location.search)
  if (found.kind !== "empty") {
    window.history.replaceState(window.history.state, "", window.location.pathname)
  }
  taken = {
    found,
    result:
      found.kind === "code"
        ? post<GitHubCallbackResult>("/github/callback", { code: found.code, state: found.state })
        : undefined,
  }
  return taken
}

/** The words for GitHub's refusal codes a person meets on the way back. */
export function deniedWords(error: string, description?: string): string {
  if (error === "access_denied") return "The sign-in was cancelled on GitHub, so nothing changed."
  if (error === "redirect_uri_mismatch") {
    return "GitHub would not send the browser back to this address: it is not one of the App's callback URLs."
  }
  return description || `GitHub refused the sign-in (${error}).`
}
