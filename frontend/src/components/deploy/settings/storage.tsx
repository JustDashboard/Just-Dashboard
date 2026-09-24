"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import { Archive, Plus } from "@/components/icons"
import { ApiError, get, refusedIndex } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  BackupResourceReport,
  DeploymentEnvironmentConfiguration,
  DeploymentOperations,
  DockerVolume,
  FilePlaces,
} from "@/lib/types"
import { FolderColourProvider } from "@/components/files/file-icon"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
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
 * was the only colour on the page. It opens on four readings now: how many
 * mounts and of which kind, whether the live release found each one, how
 * much the volumes hold, and how many of them a backup job covers — the one
 * that is amber when the answer is "not all", because persistent storage is
 * not a backup. The editor draws each mount as what it is (`MountRows`), and
 * the live release's mounts are destinations — the volume or the folder
 * each one is — so they are cards you open rather than lines to read, with
 * a card to back up any volume nothing copies.
 *
 * The four readings come from three places the page may not be allowed to
 * read: Docker's volume sizes, the backup coverage report and the Files
 * page's folder colours. Each one refused leaves its reading out rather than
 * guessing at it; one still being read is a dash until it lands, never the
 * refusal's sentence.
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
  // The colour a folder was given in Files, so a host path here is the folder
  // the operator already knows by sight.
  const places = usePoll((signal) => get<FilePlaces>("/files/places", undefined, signal), 0)
  // A poll that fails after it has answered keeps its last answer, so only a
  // read that never answered is a refusal — and one still in flight is
  // neither, which is what "—" says until it lands.
  const facts: StorageFacts = {
    operations: project.operations,
    volumes: volumes.data,
    volumesRefused: !volumes.data && Boolean(volumes.error),
    coverage: coverage.data,
    coverageRefused: !coverage.data && Boolean(coverage.error),
  }
  return (
    <FolderColourProvider colours={places.data?.colours ?? {}}>
      <SettingsPage
        state={state}
        readings={(configuration) => <StorageReadings configuration={configuration} {...facts} />}
      >
        {(configuration) => (
          <StorageForm configuration={configuration} save={state.save} {...facts} />
        )}
      </SettingsPage>
    </FolderColourProvider>
  )
}

type StorageFacts = {
  operations?: DeploymentOperations
  volumes?: DockerVolume[]
  volumesRefused: boolean
  coverage?: BackupResourceReport
  coverageRefused: boolean
}

/** A named volume's own record, and whether a backup job protects it. */
function volumeFacts(name: string, { volumes, coverage }: StorageFacts) {
  const volume = volumes?.find((item) => item.name === name)
  const resource = coverage?.resources.find((item) => item.kind === "volume" && item.name === name)
  return { volume, resource }
}

function StorageReadings({
  configuration,
  ...facts
}: StorageFacts & { configuration: DeploymentEnvironmentConfiguration }) {
  const mounts = configuration.runtime.mounts ?? []
  const named = [
    ...new Set(mounts.map((mount) => mount.source).filter((source) => !source.startsWith("/"))),
  ]
  const paths = mounts.filter((mount) => mount.source.startsWith("/")).length
  const readOnly = mounts.filter((mount) => mount.readOnly).length

  const storage = facts.operations?.storage
  const live = storage?.status === "available" ? storage.mounts : undefined
  const present = live?.filter((mount) => mount.status === "present").length ?? 0
  const missing = live?.find((mount) => mount.status === "missing")

  const sized = named
    .map((name) => volumeFacts(name, facts).volume)
    .filter((volume): volume is DockerVolume => Boolean(volume))
  const total = sized.reduce((sum, volume) => sum + volume.size, 0)
  const largest = [...sized].sort((a, b) => b.size - a.size)[0]

  const covered = named.filter((name) => volumeFacts(name, facts).resource?.protected).length
  const coveredBy = named
    .flatMap((name) => volumeFacts(name, facts).resource?.coveredBy ?? [])
    .map((job) => job.jobName)

  return (
    <StatGrid columns={facts.coverageRefused ? 3 : 4} dense>
      <StatTile
        label="Mounts"
        value={mounts.length}
        hint={
          mounts.length === 0
            ? "The runtime is stateless"
            : [
                // Counted per mount, so the words add up to the figure above
                // them even where two mounts share one volume.
                mounts.length - paths > 0 && plural(mounts.length - paths, "volume"),
                paths > 0 && plural(paths, "host path"),
                readOnly > 0 && `${readOnly} read-only`,
              ]
                .filter(Boolean)
                .join(" · ")
        }
      />
      <StatTile
        label="In the live release"
        value={live ? `${present} of ${live.length}` : "—"}
        tone={missing ? "danger" : live && live.length > 0 ? "success" : "default"}
        hint={
          !live
            ? (storage?.reason ?? "Not observed yet")
            : missing
              ? `${missing.target} is missing`
              : live.length > 0
                ? "Every mount is present"
                : "The release declares none"
        }
      />
      <StatTile
        label="Volume data"
        value={!facts.volumes || total === 0 ? "—" : bytes(total)}
        hint={
          !facts.volumes
            ? facts.volumesRefused
              ? "Docker's volumes are not readable here"
              : undefined
            : named.length === 0
              ? "No named volume"
              : total === 0
                ? "Docker reports no size"
                : sized.length === 1
                  ? `In ${sized[0].name}`
                  : largest && `Most in ${largest.name} · ${bytes(largest.size)}`
        }
      />
      {!facts.coverageRefused && (
        <StatTile
          label="Backed up"
          value={facts.coverage && named.length > 0 ? `${covered} of ${named.length}` : "—"}
          tone={
            !facts.coverage || named.length === 0
              ? "default"
              : covered < named.length
                ? "warning"
                : "success"
          }
          hint={
            !facts.coverage
              ? undefined
              : named.length === 0
                ? "No named volume to back up"
                : covered < named.length
                  ? "Persistent storage is not a backup"
                  : `By ${[...new Set(coveredBy)].join(", ")}`
          }
        />
      )}
    </StatGrid>
  )
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

  const onSave = async (event: FormEvent) => {
    event.preventDefault()
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
        onSubmit={(event) => void onSave(event)}
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
              {unprotected.map(({ source, volume }) => (
                <ChoiceRow
                  key={`protect-${source}`}
                  href={`/backups?source=${encodeURIComponent(volume!.mountpoint)}`}
                  verb={`Back up ${source}`}
                  leading={<ProductLogo size="sm" fallback={Archive} />}
                  title={`Back up ${source}`}
                  description="No job copies it — opens Backups with this volume chosen"
                />
              ))}
            </ChoiceList>
          )}
        </div>
      </SettingSection>
    </>
  )
}
