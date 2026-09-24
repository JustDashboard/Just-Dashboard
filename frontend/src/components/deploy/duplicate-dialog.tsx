"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Check, Minus } from "@/components/icons"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DeploymentDraft } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FieldRow, FormFact, FormFacts } from "@/components/form"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { BranchChip } from "@/components/git/marks"
import { useProject } from "@/components/deploy/project-context"
import { ProjectMark } from "@/components/deploy/project-mark"
import { DEPLOYMENT_NAME, sourceLine, sourceProduct } from "@/components/deploy/vocabulary"

const NAME_ERROR =
  "Use 1–64 letters, numbers, dots, dashes, or underscores, starting with a letter or number."

/** What the new draft takes from this project, and what it starts without. */
const COPIED = ["Source & branch", "Build settings", "Runtime settings", "Variable names & scopes"]
const FRESH = ["Secret values", "Domains", "Storage volume names"]

/**
 * Copy a project's source, build and runtime settings into a new draft
 * without touching the original — the fast path for a staging twin, or for
 * trying a risky change without editing what is already live.
 *
 * The new draft opens on the configure step with every setting filled in, so
 * the operator reviews and deploys it exactly like a project they just
 * detected — it lands nowhere until they do.
 *
 * It says what it copies from as data under the name — the project drawn as
 * itself, where its source lives, the branch, the release — and what crosses
 * over and what starts fresh as two short lists, which were a paragraph the
 * reader had to parse to find out whether their secrets went with it.
 */
export function DuplicateProjectDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const router = useRouter()
  const project = useProject()
  const { deployment, project: record } = project.detail
  const name = record.name
  // A lazy initializer rather than an effect: the project this dialog names
  // is fixed for its whole lifetime (one shell, one project), so the only
  // moment that needs a fresh suggestion is a reopen after Cancel — handled
  // by `close` below — not a prop change while mounted.
  const [value, setValue] = useState(() => `${name}-copy`)
  const [error, setError] = useState<string>()
  // Once the field has been left with a bad name, it is checked as it is
  // corrected rather than only on the next blur.
  const [checking, setChecking] = useState(false)
  const [busy, setBusy] = useState(false)
  const source = sourceLine(deployment, record)
  const product = sourceProduct(deployment, project.configuration?.source)
  const git = deployment.sourceKind === "git" || deployment.sourceKind === "local"

  const close = () => {
    onOpenChange(false)
    setValue(`${name}-copy`)
    setError(undefined)
    setChecking(false)
  }

  const duplicate = async () => {
    const trimmed = value.trim()
    if (!DEPLOYMENT_NAME.test(trimmed)) {
      setError(NAME_ERROR)
      setChecking(true)
      return
    }
    setBusy(true)
    setError(undefined)
    try {
      const draft = await post<DeploymentDraft>(`/deploy/${project.projectId}/duplicate`, {
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
      size="md"
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
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault()
          void duplicate()
        }}
      >
        <div className="flex min-w-0 items-center gap-3">
          <ProjectMark deployment={deployment} product={project.product} size="sm" />
          <FormFacts className="min-w-0 flex-1">
            <FormFact label="From" mono={source.mono || git}>
              <span className="inline-flex min-w-0 items-center gap-1.5">
                {hasProductLogo(product) && <ProductGlyph id={product} />}
                <span className="truncate">{deployment.sourceRepository || source.primary}</span>
              </span>
            </FormFact>
            {git && deployment.sourceRef && (
              <FormFact label="Branch">
                <BranchChip branch={deployment.sourceRef} className="max-w-40" />
              </FormFact>
            )}
            {project.liveRelease && (
              <FormFact label="Release">
                <span className="numeric">#{project.liveRelease.number}</span>
              </FormFact>
            )}
          </FormFacts>
        </div>

        <Field label="Name" htmlFor="duplicate-name" error={error}>
          <Input
            id="duplicate-name"
            value={value}
            onChange={(event) => {
              setValue(event.target.value)
              if (checking)
                setError(DEPLOYMENT_NAME.test(event.target.value.trim()) ? undefined : NAME_ERROR)
            }}
            onBlur={() => {
              if (value && !DEPLOYMENT_NAME.test(value)) {
                setError(NAME_ERROR)
                setChecking(true)
              }
            }}
            autoComplete="off"
            spellCheck={false}
            aria-invalid={Boolean(error)}
          />
        </Field>

        <FieldRow>
          <CopyList title="Copied" items={COPIED} copied />
          <CopyList title="Starts fresh" items={FRESH} />
        </FieldRow>
      </form>
    </Modal>
  )
}

function CopyList({ title, items, copied }: { title: string; items: string[]; copied?: boolean }) {
  const Mark = copied ? Check : Minus
  return (
    <section aria-label={title} className="space-y-2">
      <p className="eyebrow">{title}</p>
      <ul className="space-y-1.5">
        {items.map((item) => (
          <li key={item} className="flex items-center gap-2 text-xs">
            <Mark
              aria-hidden
              className={
                copied
                  ? "size-3.5 shrink-0 text-success"
                  : "size-3.5 shrink-0 text-muted-foreground"
              }
            />
            <span className={copied ? undefined : "text-muted-foreground"}>{item}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}
