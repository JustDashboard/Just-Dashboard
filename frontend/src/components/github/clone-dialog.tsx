"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { CloudDownload, GitBranch } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { githubRepoApi, localCheckoutsOf, localGitHref } from "@/lib/github"
import type { GitHubCloneStart, GitHubRepository } from "@/lib/github-types"
import type { GitRepo } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Modal } from "@/components/modal"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { cloneFolderProblem } from "@/components/github/list-model"

export type CloneToServerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The connection the clone is authorised through. */
  connection: number
  repository: Pick<GitHubRepository, "fullName" | "name" | "defaultBranch" | "private">
  /** The new checkout's path, once the clone finished and Local Git can see it. */
  onCloned?: (path: string) => void
}

/**
 * A GitHub repository cloned onto this server, where Local Git finds it.
 *
 * It lands in one of Local Git's roots (`/git/roots`), in a folder that must
 * not exist yet, named by the server's own rule; the server runs `git clone`
 * as a job, as the root's owner, and the dialog streams it. A private
 * repository is fetched with a one-time, read-only token for that one
 * repository, which the server revokes after; the checkout keeps no
 * credential, so later pulls and pushes use this server's own git sign-in
 * (Local Git's GitHub sign-in). Nothing is cloned until the reader presses
 * Clone, and a clone that fails keeps the form for another try.
 */
export function CloneToServerDialog(props: CloneToServerProps) {
  if (!props.open) return null
  return <CloneBody {...props} />
}

function CloneBody({ open, onOpenChange, connection, repository, onCloned }: CloneToServerProps) {
  const roots = usePoll((signal) => get<{ roots: string[] }>("/git/roots", undefined, signal), 0)
  const [parent, setParent] = useState("")
  const [name, setName] = useState(repository.name)
  const [branch, setBranch] = useState(repository.defaultBranch)
  const [shallow, setShallow] = useState(false)
  const [starting, setStarting] = useState(false)
  const [refused, setRefused] = useState<string>()
  const [target, setTarget] = useState<string>()
  const [landed, setLanded] = useState<GitRepo>()
  const job = useJobConsole()

  const rootList = useMemo(() => roots.data?.roots ?? [], [roots.data])
  const destination = parent || rootList[0] || ""
  const problem = cloneFolderProblem(name.trim())
  const running = job.job?.status === "running" || starting
  const succeeded = job.job?.status === "succeeded"

  const clone = async () => {
    setStarting(true)
    setRefused(undefined)
    setLanded(undefined)
    try {
      const start = await post<GitHubCloneStart>(
        githubRepoApi(connection, repository.fullName, "/clone"),
        {
          parent: destination,
          name: name.trim(),
          branch: branch.trim() || undefined,
          depth: shallow ? 1 : undefined,
        },
      )
      setTarget(start.target)
      await job.open(start.jobId)
    } catch (error) {
      setRefused(errorMessage(error))
    } finally {
      setStarting(false)
    }
  }

  // Once git is done, Local Git is asked again: the checkout is offered only
  // when its discovery lists it, by path and by remote, never assumed.
  const status = job.job?.status
  useEffect(() => {
    if (status !== "succeeded" || !target) return
    let alive = true
    get<{ available: boolean; repos: GitRepo[] }>("/git/")
      .then((list) => {
        if (!alive) return
        const found =
          list.repos.find((repo) => repo.path === target) ??
          localCheckoutsOf(list.repos, repository.fullName).find((repo) => repo.path === target)
        if (found) {
          setLanded(found)
          onCloned?.(found.path)
        }
      })
      .catch(() => {})
    return () => {
      alive = false
    }
    // onCloned is the caller's; the edge into success is what matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status, target, repository.fullName])

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !starting && onOpenChange(next)}
      title="Clone to server"
      description={`Clone ${repository.fullName} into a folder on this server.`}
      size="lg"
      footer={
        succeeded ? (
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Close
            </Button>
            {landed && (
              <Button asChild>
                <Link href={localGitHref(landed.path)}>
                  <GitBranch className="size-4" />
                  Open in Local Git
                </Link>
              </Button>
            )}
          </>
        ) : (
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={starting}>
              {running ? "Close" : "Cancel"}
            </Button>
            <Button
              onClick={() => void clone()}
              disabled={!destination || Boolean(problem) || running}
              pending={running}
            >
              <CloudDownload className="size-4" />
              Clone
            </Button>
          </>
        )
      }
    >
      <div className="space-y-4">
        {refused && (
          <Notice title="Not cloned" tone="danger" live="alert">
            <span className="break-words whitespace-pre-wrap">{refused}</span>
          </Notice>
        )}
        <FieldRow>
          <Field label="Into">
            <Select value={destination} onValueChange={setParent} disabled={running || succeeded}>
              <SelectTrigger className="w-full font-mono" aria-label="Into">
                <SelectValue
                  placeholder={roots.data ? "No Local Git roots" : "Reading the roots…"}
                />
              </SelectTrigger>
              <SelectContent>
                {rootList.map((root) => (
                  <SelectItem key={root} value={root} className="font-mono">
                    {root}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Folder" htmlFor="github-clone-name" error={name ? problem : undefined}>
            <Input
              id="github-clone-name"
              autoComplete="off"
              spellCheck={false}
              value={name}
              disabled={running || succeeded}
              onChange={(event) => setName(event.target.value)}
              className="font-mono"
            />
          </Field>
        </FieldRow>
        <FieldRow>
          <Field label="Branch" htmlFor="github-clone-branch">
            <Input
              id="github-clone-branch"
              autoComplete="off"
              spellCheck={false}
              value={branch}
              disabled={running || succeeded}
              onChange={(event) => setBranch(event.target.value)}
              placeholder={repository.defaultBranch}
              className="font-mono"
            />
          </Field>
          <div className="flex items-end">
            <OptionList className="w-full">
              <OptionRow
                title="Latest commit only"
                checked={shallow}
                onCheckedChange={setShallow}
                disabled={running || succeeded}
              />
            </OptionList>
          </div>
        </FieldRow>
        {destination && !problem && (
          <FormNote>
            Lands at{" "}
            <span className="font-mono text-foreground">
              {destination.replace(/\/+$/, "")}/{name.trim()}
            </span>
            .{" "}
            {repository.private
              ? "Fetched with a one-time read-only token; later pulls use this server's own git sign-in."
              : "Public, so no token is used."}
          </FormNote>
        )}
        <JobConsole
          job={job.job}
          lines={job.lines}
          onCancel={job.running ? job.cancel : undefined}
          className="max-h-72"
        />
      </div>
    </Modal>
  )
}
