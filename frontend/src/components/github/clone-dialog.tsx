"use client"

import type { GitHubRepository } from "@/lib/github-types"

/**
 * PLACEHOLDER owned by lane F-shell: replace this file, keeping the export and
 * its props. The repository page (lane F-detail) opens it from its identity
 * line; the list opens it from a row's menu. See CONTRACT.md in
 * /home/ubuntu/just-dashboard-github-2026-11-22/.
 */
export type CloneToServerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The connection the clone is authorised through. */
  connection: number
  repository: Pick<GitHubRepository, "fullName" | "name" | "defaultBranch" | "private">
  /** The new checkout's path, once the clone finished and Local Git can see it. */
  onCloned?: (path: string) => void
}

export function CloneToServerDialog(props: CloneToServerProps) {
  void props
  return null
}
