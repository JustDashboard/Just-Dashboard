"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Cross, External, RefreshClockwise, RotateCounterClockwise } from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import {
  canTest,
  cleanupFailed,
  previewHeld,
  previewOutOfDate,
  previewStatus,
} from "@/lib/pull-requests"
import type {
  DeploymentEngineRun,
  DeploymentPreview,
  GitPullRequest,
  ProjectPullRequests,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { SourceMerge, SourcePull } from "@/components/git/glyphs"
import { ForgeFace } from "@/components/git/marks"
import { MergePullDialog, type MergeResult } from "@/components/git/merge-pull-dialog"
import { openPreview } from "@/components/git/pull-request-row"
import { TestPullDialog } from "@/components/git/test-pull-dialog"
import type { Verb } from "@/components/verbs"

/**
 * What can be done to a pull request from a deploy project, declared once
 * (§13): the Overview's rows and the header's picker read this, so the two
 * cannot offer a project's pull requests two different sets of verbs.
 *
 * Every verb here acts as the dashboard's own account — the project has no
 * checkout whose owner could act instead — through the project's own
 * pull-request routes. Testing a head is the administrator's approval of it,
 * so it takes `system.admin`; closing a preview tears an environment down,
 * so it takes the destructive capability and asks first, without a phrase:
 * the pull request stays where it is and a second test builds it again.
 *
 * The dialogs the verbs open are returned as `dialogs`, for the caller to
 * draw once beside its list.
 */
export function usePullRequestVerbs({
  projectId,
  projectName,
  info,
  refresh,
}: {
  projectId: number
  projectName: string
  /** The project's pull requests and previews, as the Overview polls them. */
  info?: ProjectPullRequests
  /** Reads the listing again after a verb changes what it says. */
  refresh: () => void
}) {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [testing, setTesting] = useState<{
    pull: GitPullRequest
    preview?: DeploymentPreview | null
  }>()
  const [merging, setMerging] = useState<GitPullRequest>()
  // The request in flight, so no row offers a second one until it answers.
  const [busy, setBusy] = useState<string>()
  const base = `/deploy/${projectId}/pull-requests`

  const previewOf = (pull: GitPullRequest) =>
    pull.preview ?? info?.previews.find((preview) => preview.number === pull.number)

  const subject = (pull: GitPullRequest, preview: DeploymentPreview) => ({
    mark: <ForgeFace login={pull.author} provider="github" />,
    name: `#${pull.number} ${pull.title}`,
    facts: (
      <FormFact label="Preview" mono>
        {preview.environmentSlug}
      </FormFact>
    ),
  })

  const close = (pull: GitPullRequest, preview: DeploymentPreview) =>
    confirm({
      title: `Close preview of #${pull.number}`,
      confirmLabel: "Close preview",
      subject: subject(pull, preview),
      description: (
        <>
          <p>
            Its containers, storage and tailnet address are removed, and the production variables
            copied into it are dropped.
          </p>
          <p>The pull request stays open on GitHub. Testing it again builds a new preview.</p>
        </>
      ),
      action: async () => {
        setBusy(`close:${pull.number}`)
        try {
          await post(`${base}/${pull.number}/preview/close`, {})
        } finally {
          setBusy(undefined)
        }
      },
      onDone: refresh,
    })

  // A failed removal is retried as the run it was, through the run's own
  // retry route, rather than closed a second time: the close route would
  // only find the preview already closed and point back at this run.
  const retryCleanup = async (
    pull: GitPullRequest,
    run: NonNullable<DeploymentPreview["lastRun"]>,
  ) => {
    setBusy(`retry:${pull.number}`)
    try {
      const created = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/runs/${run.id}/retry`,
        {},
      )
      router.push(`/deploy/${projectId}/runs/${created.id}`)
    } catch (error) {
      notify.error("Could not retry the cleanup", error)
      setBusy(undefined)
    }
  }

  const merged = (pull: GitPullRequest, result?: MergeResult) => {
    refresh()
    const preview = previewOf(pull)
    const closing = preview?.state === "open"
    const automatic = result?.production?.automatic ?? info?.production.automatic ?? false
    notify.info(
      closing
        ? automatic
          ? "Merged. The preview is closing and production redeploys automatically."
          : "Merged. The preview is closing. Auto-deploy is off — deploy production when ready."
        : automatic
          ? "Merged. Production redeploys automatically."
          : "Merged. Auto-deploy is off — deploy production when ready.",
    )
  }

  const verbsFor = (pull: GitPullRequest): Verb[] => {
    const preview = previewOf(pull)
    const reading = previewStatus(preview)
    const open = preview?.state === "open"
    const verbs: Verb[] = []
    // Held while a preview is ready, building, closing or waiting on a failed
    // cleanup: the one rule the Git page's two surfaces hold the verb by too.
    if (can("system.admin") && pull.state === "open" && !previewHeld(preview)) {
      const update = previewOutOfDate(preview)
      verbs.push({
        key: "test",
        label: update ? "Update preview" : "Test this pull request",
        icon: update ? RefreshClockwise : SourcePull,
        disabled: !canTest(pull) || Boolean(busy),
        run: () => setTesting({ pull, preview }),
      })
    }
    if (open && preview?.address?.published) {
      verbs.push({
        key: "open-preview",
        label: "Open preview",
        icon: External,
        run: () => openPreview(preview),
      })
    }
    if (preview && cleanupFailed(preview) && preview.lastRun && can("service.control")) {
      const run = preview.lastRun
      verbs.push({
        key: "retry-cleanup",
        label: "Retry cleanup",
        icon: RotateCounterClockwise,
        progressive: "Retrying…",
        disabled: Boolean(busy),
        run: () => void retryCleanup(pull, run),
      })
    } else if (open && preview && reading?.label !== "Closing" && can("destructive")) {
      verbs.push({
        key: "close-preview",
        label: "Close preview",
        icon: Cross,
        danger: true,
        progressive: "Closing…",
        disabled: Boolean(busy),
        run: () => close(pull, preview),
      })
    }
    if (can("service.control") && pull.state === "open") {
      verbs.push({
        key: "merge",
        label: "Merge",
        icon: SourceMerge,
        disabled: pull.draft || Boolean(busy),
        run: () => setMerging(pull),
      })
    }
    // A preview listed without its repository has no page to open.
    if (pull.url) {
      verbs.push({
        key: "github",
        label: "Open on GitHub",
        icon: External,
        run: () => window.open(pull.url, "_blank", "noopener"),
      })
    }
    return verbs
  }

  const dialogs = (
    <>
      {dialog}
      {testing && (
        <TestPullDialog
          open
          onOpenChange={(next) => !next && setTesting(undefined)}
          projectId={projectId}
          projectName={projectName}
          pull={testing.pull}
          preview={testing.preview}
          info={info}
          onStarted={(runId) => router.push(`/deploy/${projectId}/runs/${runId}`)}
          onRefresh={refresh}
        />
      )}
      {merging && (
        <MergePullDialog
          open
          onOpenChange={(next) => !next && setMerging(undefined)}
          endpoint={`${base}/${merging.number}/merge`}
          headSha={merging.headSha}
          pull={merging}
          onMerged={(result) => merged(merging, result)}
        />
      )}
    </>
  )

  return { verbsFor, previewOf, dialogs, busy: Boolean(busy) }
}
