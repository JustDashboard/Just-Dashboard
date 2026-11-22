"use client"

import Link from "next/link"
import { Archive, ArrowUpRight, Copy, Download, Eye, FolderOpen, Trash } from "@/components/icons"
import { Fact } from "@/components/fact-line"
import { ForgeFace, BranchChip } from "@/components/git/marks"
import { IdentityHeader } from "@/components/identity-header"
import { LanguageMark } from "@/components/language-icon"
import { Button } from "@/components/ui/button"
import { VerbMenu, type VerbSpec } from "@/components/verbs"
import { useRepoActs } from "@/components/github/repo/acts"
import { roleWord } from "@/components/github/repo/model"
import { useActReason, useRepo } from "@/components/github/repo/shared"
import { copyText } from "@/lib/clipboard"
import { compactCount, relativeTime } from "@/lib/format"
import { localGitHref } from "@/lib/github"
import type { GitRepo } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * The line the repository page opens on: the owner's face, the name, and
 * the facts a repository is told apart by, archived first because it is the
 * one that changes what can be done. The command is getting the code onto
 * this server (**Clone**), or opening it in Local Git once it is there; every
 * other verb is behind `⋯`, Delete last in the destructive ink. A verb the
 * repository's abilities hold back stays in the menu, off, with the reason
 * on hover and focus, so the reader learns what is missing rather than
 * wondering where the verb went.
 */
export function RepoIdentity({
  checkouts,
  onClone,
}: {
  /** This repository's checkouts on this server, matched by remote. */
  checkouts: GitRepo[]
  onClone: () => void
}) {
  const { repo, controlReason } = useRepo()
  const acts = useRepoActs()
  const updated = repo.pushedAt ?? repo.updatedAt
  const local = checkouts[0]
  return (
    <IdentityHeader
      label={`About ${repo.fullName}`}
      slot="github-repo-identity"
      className="animate-rise border-b-0 pb-0"
      logo={
        <ForgeFace
          login={repo.owner.login}
          provider="github"
          className="size-10 rounded-lg max-sm:size-8"
        />
      }
      title={
        <span className="inline-flex min-w-0 items-baseline gap-1">
          <span className="truncate font-normal text-muted-foreground max-sm:hidden">
            {repo.owner.login}/
          </span>
          <span className="truncate">{repo.name}</span>
        </span>
      }
      facts={
        <>
          {repo.archived && <Fact className="text-warning">archived</Fact>}
          <Fact className={cn(repo.visibility !== "public" && "text-foreground")}>
            {repo.visibility}
          </Fact>
          <Fact>
            <BranchChip branch={repo.defaultBranch} title="Default branch" />
          </Fact>
          {repo.role !== "admin" && <Fact>{roleWord(repo.role)}</Fact>}
          {repo.language && (
            <Fact className="max-sm:hidden">
              <LanguageMark language={repo.language} />
            </Fact>
          )}
          <Fact className="numeric max-sm:hidden">{compactCount(repo.stars)} stars</Fact>
          {updated && (
            <Fact className="numeric max-sm:hidden" title={updated}>
              updated {relativeTime(updated)}
            </Fact>
          )}
        </>
      }
      command={
        local ? (
          <Button asChild size="sm" aria-label="Open in Local Git">
            <Link href={localGitHref(local.path)}>
              <FolderOpen className="size-3.5" />
              Open
            </Link>
          </Button>
        ) : controlReason ? undefined : (
          <Button size="sm" aria-label="Clone to server" onClick={onClone}>
            <Download className="size-3.5" />
            Clone
          </Button>
        )
      }
      menu={<RepoMenu acts={acts} cloneable={!controlReason} onClone={onClone} />}
    />
  )
}

function RepoMenu({
  acts,
  cloneable,
  onClone,
}: {
  acts: ReturnType<typeof useRepoActs>
  cloneable: boolean
  onClone: () => void
}) {
  const { repo, controlReason } = useRepo()
  const administer = useActReason("administer")
  const remove = useActReason("delete", { destructive: true })
  const visibility = useActReason("administer", { destructive: true })
  const held = (reason: string | undefined) => ({ disabled: Boolean(reason), reason })
  const verbs: VerbSpec[] = [
    { key: "github", label: "Open on GitHub", icon: ArrowUpRight, href: repo.htmlUrl },
    {
      key: "clone",
      label: "Clone to server",
      icon: Download,
      run: onClone,
      ...held(cloneable ? undefined : controlReason),
    },
    {
      key: "copy",
      label: "Copy clone URL",
      icon: Copy,
      run: () => void copyText(repo.cloneUrl, "Clone URL copied"),
    },
    {
      key: "archive",
      label: repo.archived ? "Unarchive" : "Archive",
      icon: Archive,
      group: "Repository",
      run: acts.archive,
      ...held(administer),
    },
    {
      key: "visibility",
      label: acts.goingPublic ? "Make public" : "Make private",
      icon: Eye,
      group: "Repository",
      run: acts.visibility,
      ...held(visibility),
    },
    {
      key: "delete",
      label: "Delete",
      icon: Trash,
      group: "Repository",
      danger: true,
      run: acts.remove,
      ...held(remove),
    },
  ]
  return <VerbMenu verbs={verbs} label={`More actions for ${repo.fullName}`} />
}
