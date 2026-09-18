"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DeploymentEngineRun } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FormNote } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** A full Git object id; anything else is a name the remote is asked about. */
const OBJECT_ID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/i

/**
 * Deploy a version other than the branch's tip: a tag for a release, an
 * older commit to bisect a regression, another branch to try it in
 * production for an hour.
 *
 * The configured branch is untouched. Automatic deployments keep following
 * it, and the next push builds as before; this is one run of one version,
 * with the saved build settings and variables.
 */
export function DeployVersionDialog({
  open,
  onOpenChange,
  projectId,
  environmentId,
  branch,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  environmentId: number
  branch?: string
}) {
  const router = useRouter()
  const [value, setValue] = useState("")
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const trimmed = value.trim()

  const deploy = async () => {
    if (!trimmed) {
      setError("Enter a branch, a tag or a commit id.")
      return
    }
    setBusy(true)
    setError(undefined)
    try {
      const body = OBJECT_ID.test(trimmed)
        ? { operation: "deploy", sourceRevision: trimmed.toLowerCase() }
        : { operation: "deploy", ref: trimmed }
      const run = await post<DeploymentEngineRun>(
        `/deploy/${projectId}/environments/${environmentId}/runs`,
        body,
      )
      onOpenChange(false)
      setValue("")
      router.push(`/deploy/${projectId}/runs/${run.id}`)
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "ref_not_found") {
        setError("The remote has no branch or tag with that name.")
      } else if (caught instanceof ApiError && caught.code === "ref_not_applicable") {
        setError("Only a Git project can deploy a specific version.")
      } else {
        notify.error("Could not start deployment", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Deploy a specific version"
      description="Build a branch, a tag or a commit instead of the configured branch."
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => void deploy()} pending={busy}>
            Deploy
          </Button>
        </>
      }
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          void deploy()
        }}
      >
        <Field
          label="Branch, tag or commit"
          htmlFor="deploy-version"
          hint={
            branch
              ? `Builds that version once. Automatic deployments keep following ${branch}.`
              : "Builds that version once; the configured branch is not changed."
          }
          error={error}
        >
          <Input
            id="deploy-version"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="v1.4.2"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={Boolean(error)}
          />
        </Field>
        <FormNote>
          A full commit id deploys that exact commit; any other name is looked up on the remote. The
          saved build settings and variables apply.
        </FormNote>
      </form>
    </Modal>
  )
}
