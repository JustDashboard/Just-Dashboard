"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { errorMessage, get } from "@/lib/api"
import { unavailableReason } from "@/lib/pull-requests"
import { usePoll } from "@/hooks/use-poll"
import type { GitPullRequest, ProjectPullRequests } from "@/lib/types"
import { ChoiceList } from "@/components/flow"
import { PullRequestRow } from "@/components/git/pull-request-row"
import { TestPullDialog } from "@/components/git/test-pull-dialog"
import { Modal } from "@/components/modal"
import { EmptyNote, LoadingRows, Notice } from "@/components/state"

/**
 * The header's way to a preview: which of the repository's open pull
 * requests to test. A list of choices in a dialog — each row lit, as
 * something you pick — and the one picked opens the test dialog in its
 * place, with the listing's own facts so nothing is read twice.
 *
 * Cancelling the test dialog comes back here rather than closing everything:
 * the reader who changed their mind about one pull request usually wants
 * another, not the menu again.
 */
export function PullRequestPicker({
  open,
  onOpenChange,
  projectId,
  projectName,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  projectName: string
}) {
  const router = useRouter()
  const [picked, setPicked] = useState<GitPullRequest>()
  // Read when opened, and kept live while the dialog behind it is up: a head
  // that moves under that dialog is answered by re-reading this listing,
  // which a poll switched off for the dialog's sake could not do.
  const listing = usePoll(
    (signal) => get<ProjectPullRequests>(`/deploy/${projectId}/pull-requests`, undefined, signal),
    0,
    [projectId],
    { enabled: open },
  )
  const info = listing.data
  const pulls = (info?.pulls ?? []).filter((pull) => pull.state === "open")

  const close = (next: boolean) => {
    if (!next) setPicked(undefined)
    onOpenChange(next)
  }

  return (
    <>
      <Modal
        open={open && !picked}
        onOpenChange={close}
        title="Test a pull request"
        description={`Pick one of ${projectName}'s open pull requests to build as a preview.`}
        size="md"
      >
        {listing.error && !info ? (
          <Notice title="Could not read this project's pull requests" tone="danger">
            {errorMessage(listing.error)}
          </Notice>
        ) : !info ? (
          <LoadingRows rows={3} />
        ) : !info.available ? (
          <EmptyNote>{unavailableReason(info)}</EmptyNote>
        ) : pulls.length === 0 ? (
          <EmptyNote>No open pull requests on {info.repository}.</EmptyNote>
        ) : (
          <ChoiceList aria-label="Open pull requests">
            {pulls.map((pull) => (
              <PullRequestRow
                key={pull.number}
                pull={pull}
                preview={info.previews.find((preview) => preview.number === pull.number)}
                verb={`Test pull request #${pull.number}`}
                onOpen={() => setPicked(pull)}
              />
            ))}
          </ChoiceList>
        )}
      </Modal>
      {picked && (
        <TestPullDialog
          open
          onOpenChange={(next) => !next && setPicked(undefined)}
          projectId={projectId}
          projectName={projectName}
          pull={picked}
          preview={info?.previews.find((preview) => preview.number === picked.number)}
          info={info}
          onStarted={(runId) => {
            close(false)
            router.push(`/deploy/${projectId}/runs/${runId}`)
          }}
          onRefresh={listing.refresh}
        />
      )}
    </>
  )
}
