"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { DockerImage } from "@/lib/types"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import { Field } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ROW_BLEED, RowList } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { deploymentName } from "@/components/deploy/vocabulary"
import {
  imageName,
  inspectAndPrepare,
  type ConfigureFlow,
} from "@/components/deploy/new-project/draft"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * Every image already pulled onto this server has a name the dashboard can
 * read, so those are offered first; the free-text field is for anything in a
 * registry that is not here yet.
 */
export function SourceImage({ onInspected }: { onInspected: (flow: ConfigureFlow) => void }) {
  const images = usePoll((signal) => get<DockerImage[]>("/docker/images", undefined, signal), 0)
  const [filter, setFilter] = useSessionState("deploy.new.image.filter", "")
  const [reference, setReference] = useSessionState("deploy.new.image.reference", "")
  const [credentialId, setCredentialId] = useSessionState<number | undefined>(
    "deploy.new.image.credential",
    undefined,
  )
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Error>()

  // A tag already pulled onto this server needs no credential to reuse — the
  // picker only matters for a reference typed by hand, which is also the one
  // that might name a private registry.
  const doInspect = async (chosen: string, withCredential = false) => {
    const trimmed = chosen.trim()
    if (!trimmed) return
    setBusy(true)
    setFailure(undefined)
    try {
      onInspected(
        await inspectAndPrepare(
          deploymentName(imageName(trimmed)),
          "image",
          {
            kind: "image",
            mode: "image_reference",
            image: trimmed,
            credentialId: withCredential ? credentialId : undefined,
          },
          { sourceLabel: trimmed },
        ),
      )
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy(false)
    }
  }

  const needle = filter.trim().toLowerCase()
  const tags = (images.data ?? [])
    .flatMap((image) =>
      image.repoTags.map((tag) => ({ tag, size: image.size, created: image.created })),
    )
    .filter(
      ({ tag }) =>
        tag && tag !== "<none>:<none>" && (!needle || tag.toLowerCase().includes(needle)),
    )
    .sort((a, b) => a.tag.localeCompare(b.tag))

  const total = tags.reduce((sum, entry) => sum + entry.size, 0)

  return (
    // One measure, the page's own. Five of the six sources opened in a 672px
    // column centred under a full-width title and strip, so pressing a tab
    // moved the page's left edge and left a field of nothing on either side.
    <div className="grid min-w-0 gap-x-10 gap-y-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
      {failure && <ErrorState error={failure} className="xl:col-span-2" />}
      <Panel plain className="min-w-0">
        <PanelHeader
          title="Choose an image"
          actions={
            images.data && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(tags.length, "image")} · {bytes(total)}
              </span>
            )
          }
        />
        <PanelBody className="space-y-4">
          {images.error && <ErrorState error={images.error} />}
          <SearchInput
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Filter images on this server"
            aria-label="Filter images"
            containerClassName="sm:w-full"
          />
          {images.loading && <LoadingRows rows={4} />}
          {images.data && tags.length === 0 && (
            <EmptyNote>
              {images.data.length === 0
                ? "This server has no images pulled yet. Name one in a registry below."
                : "No image on this server matches that filter."}
            </EmptyNote>
          )}
          {/* Padded by the rows' own bleed, or the negative margins overflow
              and the list grows a sideways scrollbar. */}
          <div
            className="-mx-3 max-h-[min(60vh,40rem)] overflow-y-auto px-3"
            key={images.loading ? "loading" : "listed"}
          >
            <RowList aria-label="Images on this server" className="animate-rise">
              {tags.map(({ tag, size, created }) => (
                // §12's shape: the name is the control and carries the verb,
                // the row around it is a convenience for the pointer. A single
                // button wrapping the row would read its size and age out as
                // part of the control's name.
                <li key={tag} data-slot="row" className="min-w-0">
                  <div
                    onClick={() => void doInspect(tag)}
                    className={`group flex min-w-0 cursor-pointer items-center gap-3 px-5 py-3 text-left ${ROW_BLEED} transition-colors hover:bg-row-hover`}
                  >
                    <button
                      type="button"
                      aria-label={`Use ${tag}`}
                      onClick={(event) => {
                        event.stopPropagation()
                        void doInspect(tag)
                      }}
                      className="min-w-0 flex-1 truncate rounded-sm text-left font-mono text-body focus-ring"
                    >
                      {tag}
                    </button>
                    <span className="flex shrink-0 items-center gap-3">
                      {created && (
                        <span className="numeric hidden text-hint text-muted-foreground sm:inline">
                          {relativeTime(created)}
                        </span>
                      )}
                      <span className="numeric w-16 text-right text-hint text-muted-foreground">
                        {bytes(size)}
                      </span>
                    </span>
                  </div>
                </li>
              ))}
            </RowList>
          </div>
        </PanelBody>
      </Panel>

      {/* The registry field is a task of its own, not a footnote under the
          list: an image that is not on this server yet is the other half of
          the answer to "which image", and at this width it can sit beside it. */}
      <Panel plain className="min-w-0 xl:sticky xl:top-6 xl:self-start">
        <PanelHeader title="Pull from a registry" />
        <PanelBody className="space-y-3">
          <Field
            label="Image reference"
            htmlFor="image-reference"
            hint="A tag is resolved to an immutable digest during inspection."
          >
            <Input
              id="image-reference"
              value={reference}
              onChange={(event) => setReference(event.target.value)}
              placeholder="ghcr.io/owner/app:tag"
              className="font-mono"
            />
          </Field>
          <Field label="Credential" htmlFor="image-credential" hint="For a private image only.">
            <CredentialSelect
              id="image-credential"
              kind="registry"
              value={credentialId}
              onChange={setCredentialId}
            />
          </Field>
          <Button
            className="h-11 w-full sm:h-9"
            pending={busy}
            disabled={!reference.trim()}
            onClick={() => void doInspect(reference, true)}
          >
            Continue
          </Button>
        </PanelBody>
      </Panel>
    </div>
  )
}
