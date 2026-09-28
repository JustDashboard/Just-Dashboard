"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { DockerImage } from "@/lib/types"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import { Field } from "@/components/form"
import { ChoiceList, ChoiceRow, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { deploymentName } from "@/components/deploy/vocabulary"
import { ProductGlyph, ProductLogo, hostProduct, imageProduct } from "@/components/product-logo"
import { useSourceInspection } from "./use-source-inspection"
import {
  imageName,
  inspectAndPrepare,
  type ConfigureFlow,
} from "@/components/deploy/new-project/draft"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

function referenceMark(reference: string) {
  const named = imageProduct(reference)
  return named !== "docker" ? named : (hostProduct(reference) ?? "docker")
}

/**
 * Every image already pulled onto this server has a name the dashboard can
 * read, so those are offered first; the free-text field is for anything in a
 * registry that is not here yet.
 */
export function SourceImage({
  onInspected,
  initialReference,
}: {
  onInspected: (flow: ConfigureFlow) => void
  /** An image reference from the address, filled in on arrival. */
  initialReference?: string
}) {
  const images = usePoll((signal) => get<DockerImage[]>("/docker/images", undefined, signal), 0)
  const [filter, setFilter] = useSessionState("deploy.new.image.filter", "")
  const [reference, setReference] = useSessionState(
    "deploy.new.image.reference",
    "",
    initialReference,
  )
  const [credentialId, setCredentialId] = useSessionState<number | undefined>(
    "deploy.new.image.credential",
    undefined,
  )
  // Which press is in flight — a row's tag, or the registry field — so the
  // light runs round the thing that was pressed and nothing else.
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()
  const inspection = useSourceInspection(onInspected)

  // A tag already pulled onto this server needs no credential to reuse — the
  // picker only matters for a reference typed by hand, which is also the one
  // that might name a private registry.
  const doInspect = async (chosen: string, withCredential = false) => {
    const trimmed = chosen.trim()
    if (!trimmed || busy) return
    setBusy(withCredential ? "registry" : trimmed)
    setFailure(undefined)
    try {
      await inspection.inspect(() =>
        inspectAndPrepare(
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
      setBusy("")
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
    <div className="grid min-w-0 gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[minmax(0,1fr)]">
      {/* The one surface on this screen that carries depth (§16): which image
          runs is what the reader opened this tab to decide, and the registry
          field beside it is the fallback for a tag this host has not pulled.
          Two surfaces with depth would be two foregrounds, which is none. */}
      <FlowPanel className="min-w-0 xl:max-h-full xl:min-h-0 xl:self-start">
        <FlowPanelHeader
          title="Choose an image"
          actions={
            images.data && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(tags.length, "image")} · {bytes(total)}
              </span>
            )
          }
        />
        <FlowPanelBody className="flex min-h-0 flex-1 flex-col gap-4">
          {failure && <ErrorState error={failure} />}
          {images.error && <ErrorState error={images.error} />}
          <SearchInput
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="Filter images on this server"
            aria-label="Filter images"
            // Its own height, not the column's: SearchInput takes a whole line
            // of a wrapping toolbar on a phone with `basis-full`, and in this
            // column that basis is the column's height, so the field sat in a
            // band of nothing and squeezed the list under it to a row and a half.
            containerClassName="shrink-0 max-sm:basis-auto sm:w-full"
          />
          {images.loading && <LoadingRows rows={4} />}
          {images.data && tags.length === 0 && (
            <EmptyNote>
              {images.data.length === 0
                ? "This server has no images pulled yet. Name one in a registry below."
                : "No image on this server matches that filter."}
            </EmptyNote>
          )}
          {/* Every row carries its own lit edge now, and an edge flush with the
              side of a scroll container runs under the scrollbar. The container
              pads for the edges and pulls the padding back out, so the rows
              still start where the filter field above them does. */}
          <div
            className="-mx-3 max-h-[min(60vh,40rem)] overflow-y-auto px-3 xl:max-h-none xl:min-h-0 xl:flex-1"
            key={images.loading ? "loading" : "listed"}
          >
            <ChoiceList aria-label="Images on this server" className="animate-rise">
              {tags.map(({ tag, size, created }) => (
                <ChoiceRow
                  key={tag}
                  verb={`Use ${tag}`}
                  onSelect={() => void doInspect(tag)}
                  busy={busy === tag}
                  leading={<ProductLogo id={imageProduct(tag)} size="sm" />}
                  // Mono because a tag is read character by character: which of
                  // `app:1.0.9` and `app:1.09` this is decides what runs.
                  title={<span className="font-mono">{tag}</span>}
                  trailing={
                    <>
                      {created && (
                        <span className="numeric hidden text-hint text-muted-foreground sm:inline">
                          {relativeTime(created)}
                        </span>
                      )}
                      <span className="numeric w-16 text-right text-hint text-muted-foreground">
                        {bytes(size)}
                      </span>
                    </>
                  }
                />
              ))}
            </ChoiceList>
          </div>
        </FlowPanelBody>
      </FlowPanel>

      {/* The registry field is a task of its own, not a footnote under the
          list: an image that is not on this server yet is the other half of
          the answer to "which image", and at this width it can sit beside it. */}
      <Panel plain className="min-w-0 xl:min-h-0 xl:overflow-y-auto">
        <PanelHeader title="Pull from a registry" />
        <PanelBody className="space-y-3">
          <Field
            label="Image reference"
            htmlFor="image-reference"
            hint="A tag is resolved to an immutable digest during inspection."
          >
            {/* The reference as it is typed, drawn the way the rows above and
                the plan on the next screen draw it: `postgres:16` is Postgres
                here as it is there. Only a reference no product names falls
                back to where it is pulled from — GitHub's mark for ghcr.io,
                Quay's, an Amazon or Azure registry's — which is what decides
                the credential under it, and Docker Hub's whale for the rest. */}
            <InputGroup>
              <InputGroupAddon aria-hidden className="px-3">
                <ProductGlyph id={referenceMark(reference)} />
              </InputGroupAddon>
              <InputGroupInput
                id="image-reference"
                value={reference}
                onChange={(event) => setReference(event.target.value)}
                placeholder="ghcr.io/owner/app:tag"
                className="font-mono"
              />
            </InputGroup>
          </Field>
          <Field label="Credential" htmlFor="image-credential" hint="For a private image only.">
            <CredentialSelect
              id="image-credential"
              kind="registry"
              value={credentialId}
              onChange={setCredentialId}
            />
          </Field>
          {/* The brand marks whatever advances the screen (§16), and which
              thing that is depends on whether the list beside this one has
              anything in it. With images to choose from, the rows are the
              advance and their lit edges say so; this is the fallback, and a
              brand face here would be the only blue on the screen pointing at
              the secondary path. With no images pulled, there is nothing to
              choose and the registry field *is* the way forward. */}
          <Button
            className="h-11 w-full sm:h-9"
            variant={tags.length > 0 ? "outline" : "default"}
            pending={busy === "registry"}
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
