"use client"

import { useEffect, useState } from "react"
import { useRouter } from "next/navigation"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { TemplatePreview, UserTemplateDetail } from "@/lib/types"
import { Field, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { TemplatePreviewFacts } from "@/components/deploy/template-preview"
import { templateDeployHref, templateId } from "@/components/deploy/templates"

/**
 * Save as template: what this environment's live release was deployed from,
 * as a template of this dashboard's own that the next deployment is made
 * from in the picker's Your templates. The server reads the plan the release
 * froze; its secrets become questions and its address the domain, so the
 * dialog says what the template will ask before anything is saved.
 */
export function SaveAsTemplate({
  projectId,
  environmentId,
  projectName,
}: {
  projectId: number
  environmentId: number
  projectName: string
}) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <SettingSection
        id="template"
        title="Template"
        state="What this environment's live release runs, kept as a template for the next deployment."
        actions={
          <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
            Save as template…
          </Button>
        }
      />
      {open && (
        <SaveAsTemplateDialog
          projectId={projectId}
          environmentId={environmentId}
          defaultName={projectName}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

function SaveAsTemplateDialog({
  projectId,
  environmentId,
  defaultName,
  onClose,
}: {
  projectId: number
  environmentId: number
  defaultName: string
  onClose: () => void
}) {
  const router = useRouter()
  const [name, setName] = useState(defaultName)
  const [description, setDescription] = useState("")
  const [preview, setPreview] = useState<TemplatePreview>()
  const [problem, setProblem] = useState<string>()
  const [checking, setChecking] = useState(true)
  const [saving, setSaving] = useState(false)
  const id = templateId(name)
  const taken = Boolean(preview?.exists && id === preview.definition.id)

  const request = (dryRun: boolean) => ({
    deploymentId: projectId,
    environmentId,
    name: name.trim(),
    description: description.trim() || undefined,
    dryRun: dryRun || undefined,
  })

  // What the template would ask is read once, as the dialog opens: the
  // definition depends on the release, not on the name being typed.
  useEffect(() => {
    let current = true
    post<TemplatePreview>("/deploy/templates/from-deployment", {
      deploymentId: projectId,
      environmentId,
      name: defaultName.trim() || "template",
      dryRun: true,
    })
      .then((answer) => current && setPreview(answer))
      .catch((err) => {
        if (!current) return
        if (err instanceof ApiError && err.status < 500) setProblem(err.message)
        else notify.error("Could not read this deployment's plan", err)
      })
      .finally(() => current && setChecking(false))
    return () => {
      current = false
    }
  }, [projectId, environmentId, defaultName])

  const save = async () => {
    setSaving(true)
    try {
      const created = await post<UserTemplateDetail>(
        "/deploy/templates/from-deployment",
        request(false),
      )
      notify.success(`${created.name} saved as a template`, {
        description: "Choose it under Your templates when you deploy something new.",
        action: { label: "Deploy", onClick: () => router.push(templateDeployHref(created.id)) },
      })
      onClose()
    } catch (err) {
      if (err instanceof ApiError && err.status < 500) setProblem(err.message)
      else notify.error("Could not save the template", err)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(next) => !next && onClose()}
      title="Save as template"
      description="Saves what this deployment runs as a template of your own."
      size="lg"
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button
            pending={saving}
            disabled={!id || taken || checking || (!preview && Boolean(problem))}
            onClick={() => void save()}
          >
            Save template
          </Button>
        </>
      }
    >
      <div className="min-w-0 space-y-4">
        <Field
          label="Name"
          htmlFor="save-template-name"
          hint={id ? `Saved as ${id}` : "Letters or digits; the id is made from it."}
          error={taken ? "A template already has this name; choose another." : undefined}
        >
          <Input
            id="save-template-name"
            value={name}
            onChange={(event) => {
              setName(event.target.value)
              if (preview) setProblem(undefined)
            }}
            autoComplete="off"
            maxLength={80}
          />
        </Field>
        <Field label="Description" htmlFor="save-template-description" hint="Optional.">
          <Textarea
            id="save-template-description"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            rows={2}
            maxLength={600}
          />
        </Field>
        {checking && (
          <div role="status" aria-label="Reading the live release's plan">
            <LoadingRows rows={3} />
          </div>
        )}
        {problem && (
          <Notice
            tone={preview ? "danger" : "warning"}
            title={preview ? "Not saved" : "This cannot be saved as a template"}
          >
            {problem}
          </Notice>
        )}
        {preview && <TemplatePreviewFacts definition={preview.definition} />}
        {!checking && !preview && !problem && (
          <FormNote>The live release&apos;s plan could not be read.</FormNote>
        )}
      </div>
    </Modal>
  )
}
