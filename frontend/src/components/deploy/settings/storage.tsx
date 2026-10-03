"use client"

import { useState } from "react"
import { Archive, Plus } from "@/components/icons"
import { ApiError, get, refusedIndex } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  BackupResourceReport,
  DeploymentEnvironmentConfiguration,
  DeploymentOperations,
  DockerVolume,
} from "@/lib/types"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo } from "@/components/product-logo"
import { EmptyNote } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { MOUNT_STATUS, MountMark } from "@/components/deploy/vocabulary"
import { volumeProduct } from "@/components/deploy/service-product"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import { emptyMount, MountRows, type MountValue } from "@/components/deploy/settings/mounts"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { useProject } from "@/components/deploy/project-context"

/**
 * Storage — what the container keeps across releases.
 *
 * It was a framed card of framed boxes, one per mount, and a grey list of
 * evidence under it whose one link per row was an 11px underline — nothing
 * said whether a source was a Docker volume or a directory on this server,
 * how much it held, or whether anything backed it up, and a missing mount
 * was the only colour on the page. Now the editor draws each mount as what it
 * is (`MountRows`), and the live release's mounts are destinations — the
 * volume or the folder each one is — so they are cards you open rather than
 * lines to read, with a card to back up any volume nothing copies.
 *
 * Docker's volume sizes and the backup coverage report are two reads the page
 * may not be allowed to make. A card whose fact was refused or is still on its
 * way leaves it out rather than guessing at it.
 */

export function StorageSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  const project = useProject()
  const volumes = usePoll(
    (signal) => get<DockerVolume[]>("/docker/volumes/", undefined, signal),
    60000,
  )
  const coverage = usePoll(
    (signal) => get<BackupResourceReport>("/backups/resources", undefined, signal),
    60000,
  )
  const facts: StorageFacts = {
    operations: project.operations,
    volumes: volumes.data,
    coverage: coverage.data,
  }
  return (
    <SettingsPage state={state}>
      {(configuration) => (
        <StorageForm configuration={configuration} save={state.save} {...facts} />
      )}
    </SettingsPage>
  )
}

type StorageFacts = {
  operations?: DeploymentOperations
  volumes?: DockerVolume[]
  coverage?: BackupResourceReport
}

/** A named volume's own record, and whether a backup job protects it. */
function volumeFacts(name: string, { volumes, coverage }: StorageFacts) {
  const volume = volumes?.find((item) => item.name === name)
  const resource = coverage?.resources.find((item) => item.kind === "volume" && item.name === name)
  return { volume, resource }
}

function StorageForm({
  configuration,
  save,
  ...facts
}: StorageFacts & {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const project = useProject()
  // Readings beside a mount's name where its column has room, under it where not.
  const [column, columnWidth] = useColumnWidth()
  const wide = columnWidth >= 480
  const draft = useSettingDraft<MountValue[]>(
    `deploy.${project.projectId}.settings.storage`,
    configuration.runtime.mounts ?? [],
  )
  const mounts = draft.value
  const [saving, setSaving] = useState(false)
  const [mountError, setMountError] = useState<{ index: number; message: string }>()
  const storage = facts.operations?.storage

  // The container that keeps its data in a volume names it, as on Runtime.
  const product = volumeProduct(
    configuration.runtime.image || facts.operations?.runtime.services[0]?.image,
    project.detail.deployment.sourceKind,
    project.product,
  )

  const onSave = async () => {
    setSaving(true)
    setMountError(undefined)
    try {
      await save({ runtime: (latest) => ({ ...latest.runtime, mounts }) })
      return true
    } catch (error) {
      const index =
        error instanceof ApiError ? refusedIndex(error.field, "runtime.mounts") : undefined
      if (error instanceof ApiError && index !== undefined) {
        setMountError({ index, message: error.message })
      } else {
        notify.error("Could not save storage", error)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  // Volumes that no backup job copies, with where their data sits so Backups
  // can open its form on that path.
  const unprotected = facts.coverage
    ? [...new Set((configuration.runtime.mounts ?? []).map((mount) => mount.source))]
        .filter((source) => !source.startsWith("/"))
        .map((source) => ({ source, ...volumeFacts(source, facts) }))
        .filter(({ volume, resource }) => volume && !resource?.protected)
    : []

  return (
    <>
      <SettingForm
        name="Persistent mounts"
        onSave={onSave}
        dirty={draft.dirty}
        changes={draft.changes}
        saving={saving}
        canEdit={canAdmin}
        onDiscard={() => {
          setMountError(undefined)
          draft.discard()
        }}
      >
        <SettingSection
          title="Persistent mounts"
          status={settingStatus({ dirty: draft.dirty, refused: Boolean(mountError) })}
          actions={
            canAdmin && (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => draft.set([...mounts, emptyMount()])}
              >
                <Plus className="size-3.5" /> Add mount
              </Button>
            )
          }
        >
          <MountRows
            mounts={mounts}
            onChange={draft.set}
            readOnly={!canAdmin}
            product={product}
            rowError={(index) => (mountError?.index === index ? mountError.message : undefined)}
          />
        </SettingSection>
      </SettingForm>

      <SettingSection title="In the live release">
        <div ref={column} className="min-w-0">
          {!storage || storage.status !== "available" ? (
            <EmptyNote className="px-0 text-left">
              {storage?.reason ?? "Storage evidence is unavailable."}
            </EmptyNote>
          ) : storage.mounts.length === 0 ? (
            <EmptyNote className="px-0 text-left">
              This release declares no persistent storage.
            </EmptyNote>
          ) : (
            <ChoiceList aria-label="In the live release">
              {storage.mounts.map((mount, index) => {
                const status = MOUNT_STATUS[mount.status]
                const size =
                  mount.kind === "volume"
                    ? volumeFacts(mount.source, facts).volume?.size
                    : undefined
                const readings = (
                  <>
                    <Status tone={status.tone} label={status.label} />
                    {/* The editor's word for the same thing, not the engine's "bind". */}
                    <Tag>{mount.kind === "volume" ? "volume" : "host path"}</Tag>
                    {mount.readOnly && <Tag>read-only</Tag>}
                  </>
                )
                return (
                  <ChoiceRow
                    key={`${mount.source}-${mount.target}`}
                    index={index}
                    href={mount.deepLink}
                    disabled={!mount.deepLink}
                    verb={`Open the ${mount.kind === "volume" ? "volume" : "path"} ${mount.source}`}
                    leading={
                      <MountMark
                        source={mount.source}
                        product={mount.kind === "volume" ? product : undefined}
                      />
                    }
                    title={<span className="font-mono">{mount.target}</span>}
                    description={
                      <>
                        <span className="font-mono">{mount.source}</span>
                        {size ? <span className="numeric"> · {bytes(size)}</span> : null}
                        <span> · {mount.ownership}</span>
                      </>
                    }
                    trailing={wide ? readings : undefined}
                  >
                    {(!wide || mount.detail) && (
                      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 sm:pl-11">
                        {!wide && readings}
                        {mount.detail && (
                          <span className="text-hint text-muted-foreground">{mount.detail}</span>
                        )}
                      </div>
                    )}
                  </ChoiceRow>
                )
              })}
              {/* The row is a link and its title the verb, so where it goes
                  needs no second clause; why it is here does. */}
              {unprotected.map(({ source, volume }) => (
                <ChoiceRow
                  key={`protect-${source}`}
                  href={`/backups?source=${encodeURIComponent(volume!.mountpoint)}`}
                  verb={`Back up ${source}`}
                  leading={<ProductLogo size="sm" fallback={Archive} />}
                  title={`Back up ${source}`}
                  description="No backup job copies it"
                />
              ))}
            </ChoiceList>
          )}
        </div>
      </SettingSection>
    </>
  )
}
