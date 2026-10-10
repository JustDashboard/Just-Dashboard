"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useSessionState } from "@/lib/view-state"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { ComposeStack, Container, DockerImage } from "@/lib/types"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import { Field } from "@/components/form"
import {
  ChoiceList,
  ChoiceRow,
  FlowActions,
  FlowPanel,
  FlowPanelBody,
  FlowPanelHeader,
} from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { deploymentName } from "@/components/deploy/vocabulary"
import { ProductGlyph, ProductLogo, hostProduct, imageProduct } from "@/components/product-logo"
import { CODE } from "@/components/deploy/run-evidence"
import { useSourceInspection } from "./use-source-inspection"
import {
  imageName,
  inspectAndPrepare,
  type ConfigureFlow,
} from "@/components/deploy/new-project/draft"
import {
  containersByImage,
  deployableStacks,
  imageStack,
  imageStackName,
  stackSource,
  usedBy,
  type ServerImage,
} from "@/components/deploy/new-project/server-sources"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

function referenceMark(reference: string) {
  const named = imageProduct(reference)
  return named !== "docker" ? named : (hostProduct(reference) ?? "docker")
}

/**
 * What this server already runs or holds, offered first: the Compose stacks
 * whose files are here, deployed from those files, and every image tag, alone
 * or several together as one stack. The free-text field is for anything in a
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
  // Who runs each image and which stacks have files here. Neither is what the
  // tab is for, so a failure of either leaves the images to choose from and
  // says nothing: the rows lose a description, the stacks are not offered.
  const containers = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    0,
  )
  const stacks = usePoll((signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal), 0)
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
  const [selected, setSelected] = useSessionState<string[]>("deploy.new.image.selected", [])
  // Which press is in flight — a row's tag, a stack, the selection or the
  // registry field — so the light runs round the thing that was pressed and
  // nothing else.
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()
  const inspection = useSourceInspection(onInspected)

  const run = async (key: string, prepare: () => Promise<ConfigureFlow>) => {
    if (busy) return
    setBusy(key)
    setFailure(undefined)
    try {
      await inspection.inspect(prepare)
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy("")
    }
  }

  // A tag already pulled onto this server needs no credential to reuse — the
  // picker only matters for a reference typed by hand, which is also the one
  // that might name a private registry.
  const doInspect = (chosen: string, withCredential = false) => {
    const trimmed = chosen.trim()
    if (!trimmed) return
    void run(withCredential ? "registry" : trimmed, () =>
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
  }

  const byImage = containersByImage(containers.data ?? [])
  const needle = filter.trim().toLowerCase()
  const tags = (images.data ?? [])
    .flatMap((image) =>
      image.repoTags.map((tag) => ({
        tag,
        size: image.size,
        created: image.created,
        containers: image.containers,
        users: byImage.get(image.id) ?? [],
      })),
    )
    .filter(
      ({ tag }) =>
        tag && tag !== "<none>:<none>" && (!needle || tag.toLowerCase().includes(needle)),
    )
    .sort((a, b) => a.tag.localeCompare(b.tag))
  const offeredStacks = deployableStacks(stacks.data ?? [], containers.data ?? []).filter(
    (stack) => !needle || stack.name.toLowerCase().includes(needle),
  )

  const total = tags.reduce((sum, entry) => sum + entry.size, 0)

  // In the order they were chosen, which is the order the stack lists them;
  // a tag removed from the server since it was ticked drops out here.
  const chosen: ServerImage[] = selected.flatMap((tag) => {
    const image = (images.data ?? []).find((entry) => entry.repoTags.includes(tag))
    return image ? [{ tag, containers: byImage.get(image.id) ?? [] }] : []
  })
  const toggle = (tag: string, on: boolean) =>
    setSelected((current) =>
      on
        ? [...current.filter((entry) => entry !== tag), tag]
        : current.filter((entry) => entry !== tag),
    )

  const deployStack = (stack: ComposeStack) => {
    const source = stackSource(stack)
    if (!source) return
    void run(`stack:${stack.name}`, () =>
      inspectAndPrepare(deploymentName(stack.name), "compose", source, {
        sourceLabel: `${stack.name} Compose stack`,
      }),
    )
  }

  const deployChosen = () => {
    if (chosen.length === 1) return doInspect(chosen[0].tag)
    void run("selection", () =>
      inspectAndPrepare(deploymentName(imageStackName(chosen)), "compose", imageStack(chosen), {
        sourceLabel: plural(chosen.length, "image"),
      }),
    )
  }

  return (
    // One measure, the page's own. Five of the six sources opened in a 672px
    // column centred under a full-width title and strip, so pressing a tab
    // moved the page's left edge and left a field of nothing on either side.
    <div className="grid min-w-0 gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[minmax(0,1fr)]">
      {/* The one surface on this screen that carries depth (§16): what runs is
          what the reader opened this tab to decide, and the registry field
          beside it is the fallback for a tag this host has not pulled. Two
          surfaces with depth would be two foregrounds, which is none. */}
      <FlowPanel className="min-w-0 xl:max-h-full xl:min-h-0 xl:self-start">
        <FlowPanelHeader
          title="Choose from this server"
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
            placeholder="Filter stacks and images on this server"
            aria-label="Filter stacks and images"
            // Its own height, not the column's: SearchInput takes a whole line
            // of a wrapping toolbar on a phone with `basis-full`, and in this
            // column that basis is the column's height, so the field sat in a
            // band of nothing and squeezed the list under it to a row and a half.
            containerClassName="shrink-0 max-sm:basis-auto sm:w-full"
          />
          {images.loading && <LoadingRows rows={4} />}
          {images.data && tags.length === 0 && offeredStacks.length === 0 && (
            <EmptyNote>
              {images.data.length === 0
                ? "This server has no images pulled yet. Name one in a registry below."
                : "Nothing on this server matches that filter."}
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
            {offeredStacks.length > 0 && (
              <div className="animate-rise space-y-2 pb-3">
                <p className="eyebrow">Compose stacks</p>
                <ChoiceList aria-label="Compose stacks on this server">
                  {offeredStacks.map((stack) => (
                    <ChoiceRow
                      key={stack.name}
                      verb={`Deploy the ${stack.name} stack`}
                      onSelect={() => deployStack(stack)}
                      busy={busy === `stack:${stack.name}`}
                      leading={<ProductLogo id="docker-compose" size="sm" />}
                      title={stack.name}
                      // Where the file is, because that is what is deployed:
                      // the stack as the file declares it, not as it runs now.
                      description={<span className="font-mono">{stack.configFiles[0]}</span>}
                      trailing={
                        <span className="numeric hidden text-hint text-muted-foreground sm:inline">
                          {plural(stack.total || stack.services.length, "service")}
                        </span>
                      }
                    />
                  ))}
                </ChoiceList>
              </div>
            )}
            {tags.length > 0 && (
              <div className="animate-rise space-y-2">
                {offeredStacks.length > 0 && <p className="eyebrow">Images</p>}
                <ChoiceList aria-label="Images on this server">
                  {tags.map(({ tag, size, created, containers: count, users }) => (
                    <ChoiceRow
                      key={tag}
                      verb={`Use ${tag}`}
                      onSelect={() => doInspect(tag)}
                      busy={busy === tag}
                      leading={
                        // Ticking is how two images become one project; the
                        // row's own press still deploys the one image alone.
                        <span className="flex items-center gap-3">
                          <Checkbox
                            aria-label={`Select ${tag}`}
                            checked={selected.includes(tag)}
                            onCheckedChange={(state) => toggle(tag, state === true)}
                          />
                          <ProductLogo id={imageProduct(tag)} size="sm" />
                        </span>
                      }
                      // Mono because a tag is read character by character: which of
                      // `app:1.0.9` and `app:1.09` this is decides what runs. The
                      // registry steps back and the tag takes a string's hue, so
                      // the repository is what the eye lands on and the version is
                      // what it reads next.
                      title={<ImageReference reference={tag} />}
                      description={
                        usedBy(users) ??
                        (count > 0
                          ? `Used by ${plural(count, "container")} on this server`
                          : undefined)
                      }
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
            )}
          </div>
        </FlowPanelBody>
        {/* Only once something is ticked: until then the rows are the advance
            and a command here would be a second one pointing nowhere. */}
        {chosen.length > 0 && (
          <FlowActions
            note={
              chosen.length > 1
                ? "One project: a Compose stack with a service for each image."
                : "Tick another image to deploy them together as one project."
            }
            secondary={
              <Button variant="ghost" onClick={() => setSelected([])}>
                Clear
              </Button>
            }
          >
            <Button
              pending={busy === "selection" || (chosen.length === 1 && busy === chosen[0].tag)}
              onClick={deployChosen}
            >
              {chosen.length > 1 ? `Deploy ${chosen.length} images together` : "Deploy this image"}
            </Button>
          </FlowActions>
        )}
      </FlowPanel>

      {/* The registry field is a task of its own, not a footnote under the
          list: an image that is not on this server yet is the other half of
          the answer to "which image", and at this width it can sit beside it.
          Beside it, it scrolls, and a scroll container clips the focus ring
          drawn outside each field; the padding makes the room and the margin
          takes it back, the same bleed as the Git column's, so the fields stay
          where they were. */}
      <Panel
        plain
        className="min-w-0 xl:-mx-3 xl:-my-1 xl:min-h-0 xl:overflow-y-auto xl:px-3 xl:py-1"
      >
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
            variant={tags.length > 0 || offeredStacks.length > 0 ? "outline" : "default"}
            pending={busy === "registry"}
            disabled={!reference.trim()}
            onClick={() => doInspect(reference, true)}
          >
            Continue
          </Button>
        </PanelBody>
      </Panel>
    </div>
  )
}

/** `ghcr.io/owner/app:1.2`, as its registry, its repository and its tag. */
function ImageReference({ reference }: { reference: string }) {
  const colon = reference.lastIndexOf(":")
  const tagged = colon > reference.lastIndexOf("/")
  const name = tagged ? reference.slice(0, colon) : reference
  const slash = name.indexOf("/")
  const registry = slash > 0 && /[.:]|^localhost$/.test(name.slice(0, slash))
  return (
    <span className="font-mono">
      {registry && <span className="text-muted-foreground">{name.slice(0, slash + 1)}</span>}
      {registry ? name.slice(slash + 1) : name}
      {tagged && <span className={CODE.string}>{reference.slice(colon)}</span>}
    </span>
  )
}
