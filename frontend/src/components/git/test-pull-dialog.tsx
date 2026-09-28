"use client"

import { useState } from "react"
import { Play, Warning } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { plural, shortSha } from "@/lib/format"
import { heldReason } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type {
  DeploymentPreview,
  DeploymentPreviewAddress,
  GitPullRequest,
  ProjectPullRequests,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FormFact, FormFacts, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"

/** What `POST /deploy/{id}/pull-requests/{n}/preview` answers with. */
export type PreviewStarted = {
  preview: DeploymentPreview
  runId: number
  copied: string[]
  skipped: string[]
  address?: DeploymentPreviewAddress
  variablesFromProduction: boolean
}

const FORK_WARNING =
  "This pull request comes from a fork. Its code, including its Dockerfile and build steps, runs on this server once you approve it."
const VARIABLES_NOTE =
  "This copies production's current values, including secrets, into the preview. The preview can reach anything those secrets can."

/**
 * Testing a pull request: building its exact head as a preview environment
 * of a deploy project, reachable only on the operator's tailnet.
 *
 * A dialog rather than a confirm because two things are decided here and
 * neither is a yes. Whether production's variables go into the preview is a
 * choice about where its secrets may travel — default on for a branch of the
 * repository, refused outright for a fork — and a fork's code has to be read
 * before it is run, which the checkbox under the warning is the record of.
 * Pressing the button *is* the administrator's approval of that head sha:
 * the server refuses any other commit, so what the dialog shows is what
 * gets built.
 *
 * The facts the decision needs — the tailnet, the port, whether the project
 * can have previews at all — come from the project's pull-request summary.
 * A caller already holding it passes it in; the Git page, which has only
 * the checkout's own listing, lets the dialog fetch it.
 */
export function TestPullDialog({
  open,
  onOpenChange,
  projectId,
  projectName,
  pull,
  preview,
  info: given,
  onStarted,
  onRefresh,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  projectName: string
  pull: GitPullRequest
  /** The preview this pull request already has, when it is being updated rather than created. */
  preview?: DeploymentPreview | null
  /** The project's summary, where the caller has it; fetched here otherwise. */
  info?: ProjectPullRequests
  onStarted: (runId: number, result: PreviewStarted) => void
  /** The pull request moved on under the dialog: the caller's listing is stale. */
  onRefresh?: () => void
}) {
  const fetched = usePoll(
    (signal) => get<ProjectPullRequests>(`/deploy/${projectId}/pull-requests`, undefined, signal),
    0,
    [projectId],
    { enabled: open && !given },
  )
  const info = given ?? fetched.data
  // The freshest word on the pull request and its preview. The caller's row
  // was read before the dialog opened; a head that moved since — which the
  // server's refusal says — is answered by re-reading the listing, and its
  // entry for this number is what the facts and the request are built from,
  // so the second press sends the head the reader is now looking at.
  const live = info?.pulls.find((candidate) => candidate.number === pull.number) ?? pull
  const built = info?.previews.find((candidate) => candidate.number === pull.number) ?? preview
  const fork = Boolean(live.fork)
  const keeping = Boolean(built?.variablesCopiedRevision)
  // Undefined until touched, so the default follows the pull request rather
  // than the first render: a fork opened after a branch starts off, not on.
  const [copy, setCopy] = useState<boolean>()
  // The head the fork was reviewed at: a review is of one commit, so a head
  // that moved on wants reading again before it runs here.
  const [reviewedHead, setReviewedHead] = useState<string>()
  const reviewed = Boolean(live.headSha) && reviewedHead === live.headSha
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const copyVariables = !fork && (copy ?? true)

  // Closing is where the form resets, rather than opening: setting state on
  // the way in is a second render before anything is on screen.
  const change = (next: boolean) => {
    if (busy) return
    if (!next) {
      setCopy(undefined)
      setReviewedHead(undefined)
      setError(undefined)
    }
    onOpenChange(next)
  }

  const blocker = !info
    ? undefined
    : info.compose
      ? "Previews are not available for Compose projects: a stack cannot be isolated from the one it copies."
      : info.localCheckout
        ? "This project deploys a local checkout; previews need a remote Git source."
        : info.internalPort === 0
          ? "The runtime plan has no port to publish."
          : !info.tailnet.running
            ? info.tailnet.detail || "Tailscale is not running on this host."
            : undefined
  // Nothing to build while the preview is ready at this commit, building,
  // closing or waiting on a failed cleanup. The rows hold their verb for the
  // same readings; the header's picker, which lists every open pull request,
  // lands here with them.
  const held = heldReason(built)

  const host = info?.tailnet.hostname
  const port = built?.address?.kind === "tailnet" ? built.address.port : undefined
  const where = host
    ? port
      ? `${info?.tailnet.httpsEnabled ? "https" : "http"}://${host}:${port}`
      : host
    : undefined

  const ready =
    Boolean(live.headSha) && Boolean(info) && !blocker && !held && (!fork || reviewed) && !busy

  const build = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const result = await post<PreviewStarted>(
        `/deploy/${projectId}/pull-requests/${pull.number}/preview`,
        { revision: live.headSha, copyVariables, acceptFork: fork },
      )
      const said: string[] = []
      if (result.address?.url) said.push(`Reachable at ${result.address.url} once it is live.`)
      if (result.skipped?.length > 0)
        said.push(
          `${plural(result.skipped.length, "variable")} that reference other environments stayed out.`,
        )
      notify.success(`Building a preview of #${pull.number}`, {
        description: said.length > 0 ? said.join(" ") : undefined,
      })
      onStarted(result.runId, result)
      setBusy(false)
      change(false)
    } catch (err) {
      // The head moved between the listing and the press: the server names
      // the commit it sees now, and the listing behind the dialog is stale.
      if (err instanceof ApiError && err.code === "pull_request_head_changed") {
        notify.warning("The pull request moved on", { description: err.message })
        onRefresh?.()
        fetched.refresh()
      }
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={change}
      title={`Test #${pull.number}`}
      description={`Build ${live.head} as a preview of ${projectName}, reachable on your tailnet.`}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => change(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={build} disabled={!ready} pending={busy}>
            <Play className="size-4" />
            Build preview
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <FormFacts>
          <FormFact label="project">{projectName}</FormFact>
          <FormFact label="head" mono>
            {live.headSha ? shortSha(live.headSha) : "unknown"}
          </FormFact>
          {live.author && <FormFact label="by">{live.author}</FormFact>}
          <FormFact label="from" mono>
            {live.head}
          </FormFact>
          <FormFact label="into" mono>
            {live.base}
          </FormFact>
          {where && <FormFact label="reachable only">on your tailnet at {where}</FormFact>}
        </FormFacts>
        <p className="truncate text-body" title={live.title}>
          {live.title}
        </p>

        {error && (
          <Notice title="The preview could not be started" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}
        {fetched.error && !given && (
          <Notice title="Could not read this project's pull requests" tone="danger">
            {errorMessage(fetched.error)}
          </Notice>
        )}
        {!info && !fetched.error && <LoadingRows rows={3} />}
        {blocker && (
          <Notice title="This project cannot have a preview yet" tone="warning" icon={Warning}>
            {blocker}
          </Notice>
        )}
        {!blocker && held && (
          <Notice title="Nothing to build right now" tone="warning" icon={Warning}>
            {held}
          </Notice>
        )}
        {!live.headSha && (
          <FormNote tone="warning">
            The listing did not carry this pull request&apos;s head commit, so there is no exact
            revision to build. Open it on the Git page to test it.
          </FormNote>
        )}
        {info && info.releaseTasks > 0 && (
          <Notice title="Release tasks don't run in previews">
            {plural(info.releaseTasks, "release task")} configured for production will be skipped.
          </Notice>
        )}

        {fork && (
          <Notice title="Code from outside this repository" tone="warning" icon={Warning}>
            {FORK_WARNING}
            <label className="mt-2 flex cursor-pointer items-center gap-2 text-body text-foreground">
              <Checkbox
                checked={reviewed}
                disabled={busy}
                onCheckedChange={(v) => setReviewedHead(v ? live.headSha : undefined)}
                aria-label="I have reviewed this fork's changes"
              />
              I have reviewed this fork&apos;s changes
            </label>
          </Notice>
        )}

        <label className="flex cursor-pointer items-start gap-2.5">
          <Checkbox
            className="mt-0.5"
            checked={copyVariables}
            disabled={fork || busy}
            onCheckedChange={(v) => setCopy(Boolean(v))}
            aria-label={
              keeping
                ? "Keep production variables in this preview"
                : "Copy production variables into this preview"
            }
          />
          <span className="min-w-0">
            <span className="block text-body font-medium">
              {keeping
                ? "Keep production variables in this preview"
                : "Copy production variables into this preview"}
            </span>
            <span className="mt-0.5 block text-hint leading-relaxed text-muted-foreground">
              {VARIABLES_NOTE}
              {fork &&
                " Refused for a fork: production's secrets never go into code from another repository."}
            </span>
          </span>
        </label>
      </div>
    </Modal>
  )
}
