"use client"

import { useState } from "react"
import Link from "next/link"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { SmartStackUpgrade, SmartStackUpgradeStarted } from "@/lib/types"
import { FormNote } from "@/components/form"
import { INLINE_LINK } from "@/components/inline-link"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { listWords, upgradeRefusal, upgradeSentence } from "@/components/deploy/stack/stack-model"

/**
 * "Add the new apps", for a stack whose template gained apps after it was
 * deployed: FlareSolverr, for one made before it came with the stack. It
 * names them, confirms, and starts the release that runs them, then links to
 * that run. The pass after that release sets them up and connects them, as
 * it does every app.
 *
 * The press is a source change and a deploy, so it is an administrator's;
 * anyone else reads the sentence and who can do it.
 */
export function UpgradeStep({
  upgrade,
  projectId,
  canChange,
  first,
  onStarted,
  onSettled,
}: {
  upgrade: SmartStackUpgrade
  projectId: number
  canChange: boolean
  /** The page's one next step, so its command wears the brand. */
  first: boolean
  onStarted: (answer: SmartStackUpgradeStarted) => void
  /** Nothing was left to add after all: the page reads the stack again. */
  onSettled: () => void
}) {
  const [open, setOpen] = useState(false)
  const titles = listWords(upgrade.added.map((service) => service.title))
  return (
    <Notice
      data-slot="stack-upgrade"
      title={upgrade.saved ? `Start ${titles}` : `New in the stack: ${titles}`}
    >
      <p>{upgradeSentence(upgrade)}</p>
      {canChange ? (
        <div className="pt-1">
          <Button size="sm" variant={first ? "default" : "outline"} onClick={() => setOpen(true)}>
            {upgrade.saved ? "Deploy again" : `Add ${titles}`}
          </Button>
        </div>
      ) : (
        <p className="text-hint text-muted-foreground">An administrator can add it.</p>
      )}
      <UpgradeDialog
        open={open}
        onOpenChange={setOpen}
        upgrade={upgrade}
        projectId={projectId}
        onStarted={onStarted}
        onSettled={onSettled}
      />
    </Notice>
  )
}

/** The release "Add the new apps" started, and its run. */
export function UpgradeStarted({
  started,
  titles,
  projectId,
}: {
  started: Pick<SmartStackUpgradeStarted, "runId">
  /** The apps it brings, in words. */
  titles: string
  projectId: number
}) {
  return (
    <Notice data-slot="stack-upgrade" title={`A deployment is adding ${titles}`} live="status">
      <p>
        The stack is set up again once it is live.{" "}
        <Link href={`/deploy/${projectId}/runs/${started.runId}`} className={INLINE_LINK}>
          Watch the run
        </Link>
      </p>
    </Notice>
  )
}

function UpgradeDialog({
  open,
  onOpenChange,
  upgrade,
  projectId,
  onStarted,
  onSettled,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  upgrade: SmartStackUpgrade
  projectId: number
  onStarted: (answer: SmartStackUpgradeStarted) => void
  onSettled: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [failure, setFailure] = useState<unknown>()
  const titles = listWords(upgrade.added.map((service) => service.title))

  const close = (next: boolean) => {
    if (busy) return
    if (!next) {
      setRefusal(undefined)
      setFailure(undefined)
    }
    onOpenChange(next)
  }

  const submit = async () => {
    if (busy) return
    setBusy(true)
    setRefusal(undefined)
    setFailure(undefined)
    try {
      const answer = await post<SmartStackUpgradeStarted>(`/deploy/${projectId}/stack/upgrade`, {})
      setBusy(false)
      onOpenChange(false)
      onStarted(answer)
    } catch (caught) {
      setBusy(false)
      const words = caught instanceof ApiError ? upgradeRefusal(caught.code) : undefined
      if (caught instanceof ApiError && caught.code === "stack_upgrade_none") {
        notify.success(words ?? "The stack already runs every app.")
        onOpenChange(false)
        onSettled()
        return
      }
      if (words) setRefusal(words)
      else setFailure(caught)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={close}
      title={upgrade.saved ? `Start ${titles}` : `Add ${titles}`}
      footer={
        <>
          <Button variant="outline" onClick={() => close(false)} disabled={busy}>
            Cancel
          </Button>
          <Button pending={busy} onClick={() => void submit()}>
            {upgrade.saved ? "Deploy again" : "Add and deploy"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <ul aria-label="The apps added" className="divide-y divide-hairline">
          {upgrade.added.map((service) => (
            <li key={service.service} className="flex h-9 min-w-0 items-center gap-3">
              <span className="w-28 shrink-0 truncate text-body">{service.title}</span>
              <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
                {service.purpose}
              </span>
            </li>
          ))}
        </ul>
        <FormNote>
          {upgrade.saved
            ? "The deployment already names them; a deployment starts them."
            : `The deployment moves to version ${upgrade.version} of the stack's template and deploys. Nothing it has is removed: the library, every app's settings, the domains and the login stay.`}
        </FormNote>
        {refusal && (
          <Notice tone="warning" title="Nothing was added">
            {refusal}
          </Notice>
        )}
        {failure !== undefined && (
          <Notice tone="danger" title="The apps were not added">
            {failure instanceof Error ? failure.message : String(failure)}
          </Notice>
        )}
      </div>
    </Modal>
  )
}
