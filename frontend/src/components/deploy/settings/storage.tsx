"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { Plus } from "@/components/icons"
import { ApiError, refusedIndex } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import type { DeploymentEnvironmentConfiguration, DeploymentStorageMount } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { emptyMount, MountRows, type MountValue } from "@/components/deploy/settings/mounts"
import { useProject } from "@/components/deploy/project-context"

const MOUNT_STATUS: Record<DeploymentStorageMount["status"], { label: string; tone: DotTone }> = {
  present: { label: "Present", tone: "running" },
  missing: { label: "Missing", tone: "danger" },
  unavailable: { label: "Not observed", tone: "unknown" },
}

export function StorageSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        // No `key={configuration.revision}`: see the note in build.tsx — a
        // save must not remount this form and discard an in-flight edit
        // (an added mount row typed while the request that added it is
        // still resolving) the moment its own `refresh()` lands.
        <StorageForm configuration={configuration} save={state.save} />
      )}
    </ConfigurationState>
  )
}

function StorageForm({
  configuration,
  save,
}: {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const project = useProject()
  // Kept for the tab under the revision it was read from (see build.tsx).
  const [mounts, setMounts] = useSessionState<MountValue[]>(
    `deploy.${project.projectId}.settings.storage@${configuration.revision}`,
    configuration.runtime.mounts ?? [],
  )
  const [saving, setSaving] = useState(false)
  const [mountError, setMountError] = useState<{ index: number; message: string }>()
  const storage = project.operations?.storage

  const onSave = async () => {
    setSaving(true)
    setMountError(undefined)
    try {
      await save({ runtime: { ...configuration.runtime, mounts } })
      notify.success("Storage saved")
    } catch (error) {
      const index =
        error instanceof ApiError ? refusedIndex(error.field, "runtime.mounts") : undefined
      if (error instanceof ApiError && index !== undefined) {
        setMountError({ index, message: error.message })
      } else {
        notify.error("Could not save storage", error)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-6">
      <PendingChanges pending={configuration.pending} />
      <SettingCard
        title="Persistent mounts"
        actions={
          canAdmin && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => setMounts([...mounts, emptyMount()])}
            >
              <Plus className="size-3.5" /> Add mount
            </Button>
          )
        }
        note="Applies on the next deployment."
        action={
          canAdmin && (
            <Button size="sm" onClick={onSave} pending={saving}>
              Save
            </Button>
          )
        }
      >
        <MountRows
          mounts={mounts}
          onChange={setMounts}
          readOnly={!canAdmin}
          rowError={(index) => (mountError?.index === index ? mountError.message : undefined)}
        />
      </SettingCard>

      <Panel plain>
        <PanelHeader title="Storage evidence" />
        <PanelBody>
          {!storage || storage.status !== "available" ? (
            <EmptyNote>{storage?.reason ?? "Storage evidence is unavailable."}</EmptyNote>
          ) : storage.mounts.length === 0 ? (
            <EmptyNote>This release declares no persistent storage.</EmptyNote>
          ) : (
            <ul aria-label="Storage evidence" className="divide-y divide-hairline">
              {storage.mounts.map((mount) => (
                <li key={mount.target} className="min-w-0 space-y-1.5 py-3 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="min-w-0 truncate font-mono text-body">{mount.target}</span>
                    <Status
                      tone={MOUNT_STATUS[mount.status].tone}
                      label={MOUNT_STATUS[mount.status].label}
                    />
                    <Tag>{mount.kind}</Tag>
                    {mount.readOnly && <Tag>read-only</Tag>}
                    <Tag>{mount.ownership}</Tag>
                  </div>
                  <p className="truncate text-hint text-muted-foreground">{mount.source}</p>
                  {mount.detail && (
                    <p className="text-hint text-muted-foreground">{mount.detail}</p>
                  )}
                  {mount.deepLink && (
                    <Link
                      href={mount.deepLink}
                      className="inline-flex min-h-9 items-center text-hint underline underline-offset-4 focus-ring"
                    >
                      Open {mount.kind === "volume" ? "the volume" : "the path"}
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}
