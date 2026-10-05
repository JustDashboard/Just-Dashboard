"use client"

import { useState } from "react"
import { ApiError, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DeploymentDraftSource, DeploymentEnvironmentConfiguration } from "@/lib/types"
import { FormNote } from "@/components/form"
import { ComposeFilesEditor } from "@/components/deploy/new-project/source-compose"
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
        mode: "compose_paste" satisfies DeploymentDraftSource["mode"],
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
          <ComposeFilesEditor
            documents={documents}
            onChange={setDocuments}
            upload={false}
            readOnly={!canEdit}
          />
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
