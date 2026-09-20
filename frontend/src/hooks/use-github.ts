"use client"

import { get } from "@/lib/api"
import type { GitHubAppStatus, GitHubStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"

/**
 * Who this dashboard is, to GitHub.
 *
 * The path is optional and the two answers are different questions, both
 * worth asking: with a repository it is the account that owns that checkout —
 * the one whose credential a push from it would use — and without one it is
 * the account the dashboard itself runs as, which is what the repository list
 * and the files page can honestly show.
 *
 * Slow on purpose: it shells out to gh, and the answer only changes when
 * somebody signs in — which refreshes it directly rather than waiting.
 */
export function useGitHubAccount(path?: string, enabled = true) {
  return usePoll(
    (signal) => get<GitHubStatus>("/git/github/", path ? { path } : undefined, signal),
    120000,
    [path ?? ""],
    { enabled },
  )
}

/**
 * The dashboard's own GitHub App: whether one was created, which accounts
 * installed it, and where to install it on another.
 *
 * It lived in `deploy/github-app-card.tsx` while the Credentials page was the
 * only screen asking. The import picker asks the same question — which of the
 * two GitHub identities can reach a repository — and pulling the card in for
 * its hook would have dragged the manifest flow, the dialog and the drawing
 * along with it.
 */
export function useGitHubApp() {
  return usePoll((signal) => get<GitHubAppStatus>("/deploy/github-app/", undefined, signal), 30000)
}

/**
 * Where the App is on its way from nothing to deploying: not created, created
 * but installed nowhere, or installed and ready to import from. Every surface
 * that draws the App reads it from here, so the tile, the picture and the lit
 * step can never disagree.
 */
export type GitHubAppStage = "create" | "install" | "import"

export function githubAppStage(status: GitHubAppStatus | undefined): GitHubAppStage | undefined {
  if (!status) return undefined
  if (!status.configured) return "create"
  return status.installations.length === 0 ? "install" : "import"
}
