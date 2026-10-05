"use client"

import { useState } from "react"
import { ApiError, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type {
  DeploymentComposeDocument,
  DeploymentDraftSource,
  DeploymentEnvironmentConfiguration,
} from "@/lib/types"
import { Plus, Trash } from "@/components/icons"
import { FormNote } from "@/components/form"
import { Group } from "@/components/panel"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { SettingForm, SettingSection } from "@/components/deploy/settings/setting-card"

export function ComposeSourceSettings({
  projectId,
  environmentId,
  configuration,
  canEdit,
  onSaved,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  canEdit: boolean
  onSaved: () => void
}) {
  const source = configuration.source!
  const saved = source.composeFiles ?? []
  // YAML can contain a value that validation refuses. Keep edits in this
  // component, never in session storage alongside remembered settings.
  const [documents, setDocuments] = useState(saved)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const editable = saved.length > 0 && saved.every((document) => Boolean(document.content))
  const dirty = JSON.stringify(documents) !== JSON.stringify(saved)

  const save = async () => {
    setSaving(true)
    setError(undefined)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/source`, {
        ...source,
        revision: configuration.revision,
        mode: (source.mode === "recovered_snapshot"
          ? "recovered_snapshot"
          : "compose_paste") satisfies DeploymentDraftSource["mode"],
        composeFiles: documents.map((document, order) => ({ ...document, order })),
      })
      onSaved()
      notify.success("Compose source saved", {
        description: "The running services are unchanged. Deploy changes applies this revision.",
      })
      return true
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "Could not save the Compose source.")
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Compose source"
      onSave={save}
      dirty={dirty}
      changes={dirty ? 1 : 0}
      saving={saving}
      canEdit={canEdit && editable}
      onDiscard={() => {
        setDocuments(saved)
        setError(undefined)
      }}
      error={error}
    >
      <SettingSection id="compose-source" title="Compose source">
        <FormNote>
          Each service keeps its image, command, environment, ports, limits and storage in these
          files. Private values remain in Variables; keep their existing variable references. Saving
          inspects the YAML and creates a pending source revision. The running services change only
          when you deploy it.
        </FormNote>
        {editable ? (
          <ComposeFilesEditor documents={documents} onChange={setDocuments} readOnly={!canEdit} />
        ) : (
          <FormNote>
            This source reads its Compose files from{" "}
            {source.localPath || source.url || "a repository"}. Edit those original files, then
            deploy their updated source. Inline files recovered during adoption can be edited here.
          </FormNote>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * The Compose documents a stack was pasted or uploaded with: path, then
 * content. It lived with `/deploy/new`'s Compose tab, which is gone — a stack
 * is made from a repository's Compose file or adopted from the server now —
 * and this page is where saved documents are still edited.
 */
function ComposeFilesEditor({
  documents,
  onChange,
  readOnly,
}: {
  documents: DeploymentComposeDocument[]
  onChange: (documents: DeploymentComposeDocument[]) => void
  readOnly: boolean
}) {
  const update = (index: number, field: "path" | "content", value: string) =>
    onChange(
      documents.map((document, i) => (i === index ? { ...document, [field]: value } : document)),
    )
  const remove = (index: number) =>
    onChange(
      documents.filter((_, i) => i !== index).map((document, order) => ({ ...document, order })),
    )
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="eyebrow">Compose files</p>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={readOnly}
          onClick={() =>
            onChange([
              ...documents,
              { path: `compose.${documents.length + 1}.yml`, content: "", order: documents.length },
            ])
          }
        >
          <Plus className="size-3.5" />
          Add file
        </Button>
      </div>
      {documents.map((document, index) => (
        <Group key={`${document.order}-${index}`}>
          <div className="mb-2 flex items-center gap-2">
            <Input
              value={document.path}
              readOnly={readOnly}
              onChange={(event) => update(index, "path", event.target.value)}
              aria-label={`Compose file ${index + 1} path`}
              className="font-mono sm:text-xs"
            />
            <IconAction
              label={`Remove ${document.path}`}
              disabled={readOnly || documents.length === 1}
              onClick={() => remove(index)}
            >
              <Trash />
            </IconAction>
          </div>
          <Textarea
            value={document.content}
            readOnly={readOnly}
            onChange={(event) => update(index, "content", event.target.value)}
            aria-label={`${document.path} content`}
            rows={10}
            className="min-h-48 resize-y font-mono sm:text-xs"
            placeholder={"services:\n  web:\n    image: nginx:alpine"}
          />
        </Group>
      ))}
    </div>
  )
}
