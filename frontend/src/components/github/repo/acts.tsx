"use client"

import { useRouter } from "next/navigation"
import { ForgeFace } from "@/components/git/marks"
import { FormFact } from "@/components/form"
import { useRepo } from "@/components/github/repo/shared"
import { del, post } from "@/lib/api"
import type { GitHubRepositoryDetail } from "@/lib/github-types"

/**
 * The three acts that change what a repository is, offered from the
 * identity line's `⋯` and from the Settings view's danger zone, so both ask
 * the same question in the same words.
 *
 * Each says what it does in plain words and only what GitHub documents:
 * archiving is undone by unarchiving; a visibility change is undone by the
 * opposite change, except what it took with it (stars, watchers, forks);
 * deleting cannot be undone from here, so it asks for the repository's full
 * name, which the server checks again (`X-Confirm`).
 */
export function useRepoActs() {
  const router = useRouter()
  const { repo, path, refresh, confirm, connection } = useRepo()
  const subject = {
    mark: <ForgeFace login={repo.owner.login} provider="github" />,
    name: <span className="font-mono">{repo.fullName}</span>,
    facts: (
      <>
        <FormFact>{repo.visibility}</FormFact>
        {repo.archived && <FormFact>archived</FormFact>}
      </>
    ),
  }

  const archive = () =>
    confirm({
      title: repo.archived ? `Unarchive ${repo.name}` : `Archive ${repo.name}`,
      confirmLabel: repo.archived ? "Unarchive" : "Archive",
      subject,
      description: repo.archived ? (
        <p>
          It takes pushes, issues, pull requests and changes to its settings again, for everyone who
          could before.
        </p>
      ) : (
        <p>
          It becomes read-only for everyone: no pushes, issues, pull requests, comments or changes to
          its settings until it is unarchived. Nothing in it is deleted, and it can be unarchived
          here.
        </p>
      ),
      action: async () => {
        await post<GitHubRepositoryDetail>(path("/archive"), { archived: !repo.archived })
        refresh()
      },
    })

  const goingPublic = repo.visibility !== "public"
  const visibility = () =>
    confirm({
      title: goingPublic ? `Make ${repo.name} public` : `Make ${repo.name} private`,
      confirmLabel: goingPublic ? "Make public" : "Make private",
      subject,
      description: goingPublic ? (
        <p>
          Everyone on the internet will be able to see its code and its whole history, including
          every past commit, and anyone will be able to fork it.
        </p>
      ) : (
        <p>
          Only people with access will see it. The stars and watchers of everyone without access are
          removed and do not come back if it is made public again, and its public forks may be
          detached into repositories of their own.
        </p>
      ),
      action: async () => {
        await post<GitHubRepositoryDetail>(path("/visibility"), {
          visibility: goingPublic ? "public" : "private",
        })
        refresh()
      },
    })

  const remove = () =>
    confirm({
      title: `Delete ${repo.fullName}`,
      confirmLabel: "Delete",
      subject,
      phrase: repo.fullName,
      description: (
        <p>
          Deletes the repository on GitHub with its code, issues, pull requests, wiki and settings.
          This cannot be undone from here. Checkouts on this server are left as they are.
        </p>
      ),
      action: async (typed) => {
        await del(path(), { confirm: typed })
      },
      onDone: () => router.push(`/github?account=${connection}`),
    })

  return { archive, visibility, remove, goingPublic }
}
