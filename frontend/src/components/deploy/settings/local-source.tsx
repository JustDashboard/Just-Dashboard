"use client"

import { useState } from "react"
import { ApiError, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DeploymentEnvironmentConfiguration } from "@/lib/types"
import { Field, FormNote } from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"

export function LocalSourceSettings({
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
  // A path can reveal private account names. Keep source edits in memory,
  // like captured Compose documents, rather than browser remembered settings.
  const [directory, setDirectory] = useState(source.localPath ?? "")
  const [subdirectory, setSubdirectory] = useState(source.subdirectory ?? "")
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const changes =
    Number(directory !== (source.localPath ?? "")) +
    Number(subdirectory !== (source.subdirectory ?? ""))

  const save = async () => {
    setSaving(true)
    setError(undefined)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/source`, {
        ...source,
        kind: "local",
        mode: "local_directory",
        resourceId: undefined,
        revision: configuration.revision,
        localPath: directory.trim(),
        subdirectory: subdirectory.trim() || undefined,
      })
      onSaved()
      notify.success("Build source saved", {
        description: "The running app is unchanged. Deploy changes builds this source.",
      })
      return true
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "Could not save the build source.")
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Build source"
      onSave={save}
      dirty={changes > 0}
      changes={changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={() => {
        setDirectory(source.localPath ?? "")
        setSubdirectory(source.subdirectory ?? "")
        setError(undefined)
      }}
      error={error}
    >
      <SettingSection
        id="source"
        title="Build source"
        status={settingStatus({
          dirty: changes > 0,
          refused: Boolean(error),
          notLive: configuration.pending?.changes.some((change) => change.kind === "source"),
        })}
      >
        <FormNote>
          {source.mode === "recovered_snapshot"
            ? "Deploy changes builds the verified source snapshot captured during import. Choose a directory below to attach future source changes. "
            : "Choose a separate directory for new code. "}
          The original source is retained for baseline rollback. Saving inspects the directory and
          creates a pending source revision; the running app changes only when you deploy it.
        </FormNote>
        <Field
          label="Build directory"
          htmlFor="local-source-directory"
          hint="An absolute directory allowed by this server's deployment roots."
        >
          <Input
            id="local-source-directory"
            value={directory}
            onChange={(event) => setDirectory(event.target.value)}
            readOnly={!canEdit}
            disabled={saving}
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        <Field
          label="Subdirectory"
          htmlFor="local-source-subdirectory"
          hint="Relative to the build directory. Leave blank to use its root."
        >
          <Input
            id="local-source-subdirectory"
            value={subdirectory}
            onChange={(event) => setSubdirectory(event.target.value)}
            readOnly={!canEdit}
            disabled={saving}
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        {Boolean(source.excludePaths?.length) && (
          <div className="space-y-2">
            <FormNote>
              Retained data stays in its linked storage. These paths are excluded from the build
              copy and remain excluded when you select another directory.
            </FormNote>
            <ul aria-label="Retained data excluded from builds" className="space-y-1">
              {source.excludePaths!.map((path) => (
                <li key={path} className="font-mono text-hint break-all text-muted-foreground">
                  {path}
                </li>
              ))}
            </ul>
          </div>
        )}
      </SettingSection>
    </SettingForm>
  )
}
