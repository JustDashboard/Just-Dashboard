"use client"

import { useState } from "react"
import { ArrowUpRight, ShieldCheck } from "@/components/icons"
import { ForgeFace, ShortSha } from "@/components/git/marks"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ListBody, ShowMore, usePages, useRepo } from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { GitHubBranchPage, GitHubCommit, GitHubCommitPage } from "@/lib/github-types"

/**
 * The history of one branch, newest first: the first line of each message,
 * who wrote it and when, whether GitHub verified its signature, and the
 * commit on GitHub. Thirty at a time, **Show older** for the next.
 */
export function RepoCommits() {
  const { repo, path } = useRepo()
  const [branch, setBranch] = useState(repo.defaultBranch)
  const branches = usePoll(
    (signal) => get<GitHubBranchPage>(path("/branches"), { page: 1 }, signal),
    0,
    [repo.fullName],
  )
  const names = [
    repo.defaultBranch,
    ...(branches.data?.branches ?? [])
      .map((b) => b.name)
      .filter((name) => name !== repo.defaultBranch),
  ]
  const pages = usePages<GitHubCommit, GitHubCommitPage>(
    path("/commits"),
    { sha: branch },
    (p) => p.commits,
  )
  return (
    <Panel plain aria-label="Commits">
      <PanelHeader
        title="Commits"
        actions={
          <Select value={branch} onValueChange={setBranch}>
            <SelectTrigger
              aria-label="Branch"
              size="sm"
              className="w-48 font-mono max-sm:w-36"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {names.map((name) => (
                <SelectItem key={name} value={name} className="font-mono">
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        }
      />
      <PanelBody flush>
        <ListBody pages={pages} empty={`No commits on ${branch} yet.`} rows={8}>
          <RowList data-slot="github-commits">
            {pages.items.map((commit) => (
              <CommitRow key={commit.sha} commit={commit} />
            ))}
          </RowList>
        </ListBody>
        <ShowMore pages={pages} label="Show older" />
      </PanelBody>
    </Panel>
  )
}

function CommitRow({ commit }: { commit: GitHubCommit }) {
  const subject = commit.message.split("\n")[0]
  const when = commit.author.date ?? commit.committer?.date
  return (
    <Row
      data-sha={commit.sha}
      leading={
        <ForgeFace
          login={commit.author.login ?? commit.author.name}
          provider={commit.author.login ? "github" : undefined}
          size="md"
        />
      }
      title={<span title={commit.message}>{subject}</span>}
      subtitle={
        <>
          {commit.author.login ?? commit.author.name}
          {when && (
            <span className="numeric" title={timestamp(when)}>
              {" "}
              · {relativeTime(when)}
            </span>
          )}
        </>
      }
      trailing={
        <>
          {commit.verified && (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="inline-flex items-center text-success">
                  <ShieldCheck aria-label="Verified signature" className="size-3.5" />
                </span>
              </TooltipTrigger>
              <TooltipContent>GitHub verified its signature</TooltipContent>
            </Tooltip>
          )}
          <ShortSha sha={commit.sha} />
          <IconAction label={`Open ${commit.sha.slice(0, 7)} on GitHub`} asChild>
            <a href={commit.htmlUrl} target="_blank" rel="noreferrer">
              <ArrowUpRight />
            </a>
          </IconAction>
        </>
      }
    />
  )
}
