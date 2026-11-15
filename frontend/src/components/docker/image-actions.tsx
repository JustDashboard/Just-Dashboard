"use client"

import { useRef, useState } from "react"
import { Copy, Download, GitTag, Trash } from "@/components/icons"
import { del, get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DockerImage, ImageDetail } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { PullDialog, TagDialog, usePull } from "@/components/docker/image-pull"
import { imageNames, imagePhrase, primaryTag, shortId } from "@/components/docker/images"
import type { ConfirmFn } from "@/components/docker/shared"
import type { Verb } from "@/components/verbs"

/** An image as its verbs need it: its handles, and whether any container was made from it. */
export type NamedImage = Pick<DockerImage, "id" | "repoTags" | "repoDigests"> & { inUse: boolean }

/**
 * What can be done to an image, declared once for its row in the table and
 * for its own page (§13), with the two dialogs those verbs open.
 *
 * Pull and Remove are the two pressed often enough to be glyphs on a row;
 * naming, copying and tagging are words in its menu. Remove stays drawn while
 * a container uses the image, disabled, so a row does not change shape with
 * its state: Docker refuses it, and forcing it would leave a container whose
 * image is gone and which cannot start again.
 */
export function useImageActions({
  confirm,
  onChanged,
  onPulled,
  onRemoved,
}: {
  confirm: ConfirmFn
  /** An image was pulled or given a name, or turned out to be in use: read again whatever draws it. */
  onChanged: () => void
  /**
   * The reader closed the dialog of a pull that ended well. The tag it pulled
   * may point at another image now, and a page about the old one can follow
   * it; not before the dialog is closed, which still holds what Docker said.
   */
  onPulled?: () => void
  onRemoved: (id: string) => void
}) {
  const { can } = useAuth()
  // null is closed; a string (possibly empty) opens the dialog seeded with it.
  const [pullSeed, setPullSeed] = useState<string | null>(null)
  const [tagging, setTagging] = useState<{ id: string; name: string } | null>(null)
  const pull = usePull()
  const pulled = useRef(false)

  const remove = (image: NamedImage) => {
    const names = imageNames(image)
    const others = names.slice(1)
    confirm({
      title: "Delete image",
      confirmLabel: "Delete",
      description: (
        <>
          <p>
            Deletes <b>{imagePhrase(image)}</b>
            {others.length > 0 && (
              <>
                {" "}
                and its other {others.length === 1 ? "name" : "names"},{" "}
                <span className="font-mono">{others.join(", ")}</span>
              </>
            )}
            .
          </p>
          <p>
            No container is using it. Anything that needs it later has to pull or rebuild it — which
            for an image built here and never pushed means rebuilding from source.
          </p>
        </>
      ),
      action: async (phrase) => {
        const path = `/docker/images/${encodeURIComponent(image.id)}`
        // Docker refuses an id that more than one name points at unless the
        // removal is forced, and a forced removal also takes an image a
        // stopped container was made from. So it is forced only for an image
        // with several names, and only after reading again that nothing uses
        // it: the answer on screen can be half a minute old.
        const several = names.length > 1
        if (several) {
          const now = await get<ImageDetail>(path)
          if (now.usedBy.length > 0) {
            notify.error(
              "Not deleted",
              `${plural(now.usedBy.length, "container")} ${now.usedBy.length === 1 ? "uses" : "use"} this image now.`,
            )
            onChanged()
            return "reported"
          }
        }
        await del(path, { confirm: phrase, query: several ? { force: "true" } : undefined })
        onRemoved(image.id)
      },
    })
  }

  const verbsFor = (image: NamedImage): Verb[] => {
    const tag = primaryTag(image)
    const verbs: Verb[] = []
    if (can("service.control") && tag && image.repoDigests.length > 0) {
      verbs.push({
        key: "pull",
        label: "Pull",
        icon: Download,
        inline: true,
        run: () => setPullSeed(tag),
      })
    }
    if (can("destructive")) {
      verbs.push({
        key: "remove",
        label: "Remove",
        icon: Trash,
        inline: true,
        danger: true,
        disabled: image.inUse,
        run: () => remove(image),
      })
    }
    if (can("service.control")) {
      verbs.push({
        key: "tag",
        label: "Tag…",
        icon: GitTag,
        run: () => setTagging({ id: image.id, name: tag ?? shortId(image.id) }),
      })
    }
    if (tag) {
      verbs.push({
        key: "copy-name",
        label: "Copy name",
        icon: Copy,
        run: () => void copyText(tag, "Name copied"),
      })
    }
    verbs.push({
      key: "copy-id",
      label: "Copy ID",
      icon: Copy,
      run: () => void copyText(image.id, "ID copied"),
    })
    return verbs
  }

  const dialogs = (
    <>
      <PullDialog
        open={pullSeed !== null}
        initial={pullSeed ?? ""}
        pull={pull}
        onClose={() => {
          setPullSeed(null)
          if (!pulled.current) return
          pulled.current = false
          onPulled?.()
        }}
        onDone={() => {
          pulled.current = true
          onChanged()
        }}
      />
      <TagDialog image={tagging} onClose={() => setTagging(null)} onTagged={onChanged} />
    </>
  )

  return {
    verbsFor,
    dialogs,
    /** Opens the pull dialog, seeded with a reference or empty. */
    openPull: (seed: string) => setPullSeed(seed),
    /** The reference a pull is moving right now, so whatever draws it can say so. */
    pulling: pull.ref,
  }
}
