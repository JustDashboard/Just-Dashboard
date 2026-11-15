"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Archive, Copy, FolderOpen, Trash } from "@/components/icons"
import { del } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { VolumeDetail } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmFn } from "@/components/docker/shared"
import { holderOf, standing, type Backup } from "@/components/docker/volumes"
import type { Verb } from "@/components/verbs"

/**
 * What can be done to a volume, declared once for its row and its page (§13).
 * Remove is the one glyph on a row; it stays drawn while something mounts the
 * volume, disabled with the reason as its name, because Docker refuses it and
 * a control that vanishes says nothing. Backing it up hands its path to the
 * Backups form.
 */
export function useVolumeVerbs({
  confirm,
  backupOf,
  onRemoved,
}: {
  confirm: ConfirmFn
  /** What the Backups page says about a volume; undefined when it could not be read. */
  backupOf: (name: string) => Backup | undefined
  onRemoved: (volume: VolumeDetail) => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const [removing, setRemoving] = useState<string>()

  const remove = (volume: VolumeDetail) =>
    confirm({
      title: "Delete volume",
      confirmLabel: "Delete",
      description: (
        <>
          <p className="text-destructive">
            Everything stored in <b>{volume.name}</b>
            {volume.size > 0 && <> — {bytes(volume.size)} —</>} is destroyed permanently.
          </p>
          <p>
            {standing(volume) === "down"
              ? `It is what ${holderOf(volume).name} left when it was taken down; deploying that stack again would start it empty.`
              : "Nothing mounts it. A volume outlives the container that created it, so this is often the data from something that was removed and rebuilt — look at what is in it first if you are not sure."}
          </p>
        </>
      ),
      action: async (phrase) => {
        setRemoving(volume.name)
        try {
          await del(`/docker/volumes/${encodeURIComponent(volume.name)}`, { confirm: phrase })
          notify.success(`${volume.name} deleted`)
          onRemoved(volume)
        } finally {
          setRemoving(undefined)
        }
        return "reported"
      },
    })

  const verbsFor = (volume: VolumeDetail): Verb[] => {
    const verbs: Verb[] = []
    const held = volume.usedBy.length > 0
    if (can("destructive")) {
      verbs.push({
        key: "remove",
        label: held ? `Remove — mounted by ${plural(volume.usedBy.length, "container")}` : "Remove",
        icon: Trash,
        inline: true,
        danger: true,
        disabled: held || removing === volume.name,
        run: () => remove(volume),
      })
    }
    if (
      can("system.admin") &&
      volume.driver === "local" &&
      backupOf(volume.name)?.state !== "protected"
    ) {
      verbs.push({
        key: "backup",
        label: "Back up…",
        icon: Archive,
        run: () => router.push(`/backups?source=${encodeURIComponent(volume.mountpoint)}`),
      })
    }
    verbs.push({
      key: "files",
      label: "Open in Files",
      icon: FolderOpen,
      run: () => router.push(`/files?path=${encodeURIComponent(volume.mountpoint)}`),
    })
    verbs.push({
      key: "copy-name",
      label: "Copy name",
      icon: Copy,
      run: () => void copyText(volume.name, "Name copied"),
    })
    verbs.push({
      key: "copy-path",
      label: "Copy path",
      icon: Copy,
      run: () => void copyText(volume.mountpoint, "Path copied"),
    })
    return verbs
  }

  return { verbsFor, removing }
}
