"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import { bytes } from "@/lib/format"
import type { DockerImage } from "@/lib/types"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import { Field } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
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
    .flatMap((image) => image.repoTags.map((tag) => ({ tag, size: image.size })))
    .filter(
      ({ tag }) =>
        tag && tag !== "<none>:<none>" && (!needle || tag.toLowerCase().includes(needle)),
    )
    .sort((a, b) => a.tag.localeCompare(b.tag))

  return (
    <div className="mx-auto w-full max-w-2xl space-y-4">
      {failure && <ErrorState error={failure} />}
      <Panel plain>
        <PanelHeader title="Choose an image" />
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
          <RowList aria-label="Images on this server" className="max-h-80 overflow-y-auto">
            {tags.map(({ tag, size }) => (
              <Row
                key={tag}
                mono
                title={tag}
                trailing={
                  <>
                    <span className="numeric text-micro text-muted-foreground">{bytes(size)}</span>
                    <Button
                      size="xs"
                      variant="outline"
                      aria-label={`Use ${tag}`}
                      pending={busy}
                      onClick={() => void doInspect(tag)}
                    >
                      Use
                    </Button>
                  </>
                }
              />
            ))}
          </RowList>
          <div className="space-y-3 border-t border-hairline pt-4">
            <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
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
              <div className="flex items-end">
                <Button
                  className="h-11 sm:h-9"
                  pending={busy}
                  disabled={!reference.trim()}
                  onClick={() => void doInspect(reference, true)}
                >
                  Continue
                </Button>
              </div>
            </div>
            <Field label="Credential" htmlFor="image-credential" hint="For a private image only.">
              <CredentialSelect
                id="image-credential"
                kind="registry"
                value={credentialId}
                onChange={setCredentialId}
              />
            </Field>
          </div>
        </PanelBody>
      </Panel>
    </div>
  )
}
