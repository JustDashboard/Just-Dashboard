"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DeploymentDraft } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FormNote } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { DEPLOYMENT_NAME } from "@/components/deploy/vocabulary"

const NAME_ERROR =
  "Use 1–64 letters, numbers, dots, dashes, or underscores, starting with a letter or number."

/**
 * Copy a project's source, build and runtime settings into a new draft
 * without touching the original — the fast path for a staging twin, or for
 * trying a risky change without editing what is already live.
 *
 * The new draft opens on the configure step with every setting filled in, so
 * the operator reviews and deploys it exactly like a project they just
 * detected — it lands nowhere until they do.
 */
export function DuplicateProjectDialog({
  open,
  onOpenChange,
  projectId,
  name,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  name: string
}) {
  const router = useRouter()
  // A lazy initializer rather than an effect: the project this dialog names
  // is fixed for its whole lifetime (one shell, one project), so the only
  // moment that needs a fresh suggestion is a reopen after Cancel — handled
  // by `close` below — not a prop change while mounted.
  const [value, setValue] = useState(() => `${name}-copy`)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  const close = () => {
    onOpenChange(false)
    setValue(`${name}-copy`)
    setError(undefined)
  }

  const duplicate = async () => {
    const trimmed = value.trim()
    if (!DEPLOYMENT_NAME.test(trimmed)) {
      setError(NAME_ERROR)
      return
    }
    setBusy(true)
    setError(undefined)
    try {
      const draft = await post<DeploymentDraft>(`/deploy/${projectId}/duplicate`, {
        name: trimmed,
      })
      onOpenChange(false)
      router.push(`/deploy/new?draft=${draft.id}`)
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 409 && caught.code === "name_taken") {
        setError("That name is already used.")
      } else if (caught instanceof ApiError && caught.field === "name") {
        setError(caught.message)
      } else {
        notify.error("Could not duplicate project", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && !next && close()}
      title={`Duplicate ${name}`}
      description="Copy this project's settings into a new draft."
      size="sm"
      footer={
        <>
          <Button variant="outline" onClick={close} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => void duplicate()} pending={busy}>
            Duplicate project
          </Button>
        </>
      }
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault()
          void duplicate()
        }}
      >
        <Field label="Name" htmlFor="duplicate-name" error={error}>
          <Input
            id="duplicate-name"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            onBlur={() => {
              if (value && !DEPLOYMENT_NAME.test(value)) setError(NAME_ERROR)
            }}
            autoComplete="off"
            spellCheck={false}
            aria-invalid={Boolean(error)}
          />
        </Field>
        <FormNote>
          Copies its source, build and runtime settings, and its variable names and scopes. Secret
          values, domains and storage names are not copied — the new project gets its own.
        </FormNote>
      </form>
    </Modal>
  )
}
