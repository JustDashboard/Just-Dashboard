"use client"

import { External, Layers } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { ImageDetail, ImageUpdateStatus } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { LiveBytes } from "@/components/overview/readings"
import { ShellWords } from "@/components/deploy/run-evidence"
import { Hint, Term } from "@/components/docker/explain"
import {
  FRESHNESS_COLOR,
  FRESHNESS_LABEL,
  imageVersion,
  layerInstruction,
  shortId,
  splitReference,
  type Freshness,
} from "@/components/docker/images"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Disclosure } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Well } from "@/components/panel"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import type { Verb } from "@/components/verbs"
import { cn } from "@/lib/utils"

/** The Images line's hue on the disk bar, as the table's size column draws it. */
const SIZE_HUE = "var(--chart-1)"

/**
 * One image's sheet, opened from its row and addressed as `?image=`.
 *
 * It opens on the image as the product it is and four readings — how much of
 * the disk it is, how many layers carry that, how old it is, which release —
 * and then the two answers the old sheet held furthest down: who runs it, each
 * a way to that container, and where the size went, each layer a bar of its
 * share beside the Dockerfile instruction that wrote it, in build order.
 */
export function ImageSheet({
  imageId,
  state,
  update,
  verbs,
  onOpenChange,
}: {
  imageId: string | null
  state?: Freshness
  update?: ImageUpdateStatus
  verbs: (detail: ImageDetail) => Verb[]
  onOpenChange: (open: boolean) => void
}) {
  const {
    data: detail,
    error,
    loading,
  } = usePoll<ImageDetail>(
    (signal) =>
      get<ImageDetail>(`/docker/images/${encodeURIComponent(imageId ?? "")}`, undefined, signal),
    0,
    [imageId],
    { enabled: imageId !== null },
  )
  const tag = detail?.repoTags[0]
  const actions = detail ? verbs(detail) : []

  return (
    <SidePanel
      open={imageId !== null}
      onOpenChange={onOpenChange}
      width="xl"
      initialFocus="body"
      title={
        <>
          <ProductLogo id={tag ? imageProduct(tag) : undefined} fallback={Layers} size="sm" />
          <span className="min-w-0 truncate font-mono">
            {detail ? (tag ?? "untagged") : "Image"}
          </span>
        </>
      }
      description={detail ? `Image ${shortId(detail.id)}` : undefined}
      actions={
        detail && (
          <>
            {state && <FreshnessStatus state={state} />}
            <Status
              tone={detail.usedBy.some((c) => c.state === "running") ? "running" : "stopped"}
              label={
                detail.usedBy.length
                  ? `used by ${plural(detail.usedBy.length, "container")}`
                  : "not in use"
              }
            />
            <span className="font-mono text-hint text-muted-foreground">{shortId(detail.id)}</span>
            <span className="font-mono text-hint text-muted-foreground">
              {detail.os ?? "?"}/{detail.architecture ?? "?"}
            </span>
          </>
        )
      }
      footer={
        actions.length > 0 && (
          <>
            {actions.map((verb) => (
              <Button
                key={verb.key}
                size="sm"
                variant={
                  verb.danger
                    ? "destructive"
                    : verb.key === "pull" && state === "outdated"
                      ? "default"
                      : "outline"
                }
                disabled={verb.disabled}
                onClick={verb.run}
              >
                <verb.icon className="size-4" />
                {verb.label}
              </Button>
            ))}
          </>
        )
      }
    >
      {error && <ErrorState error={error} />}
      {loading && !detail && <LoadingRows rows={6} />}
      {detail && (
        <div data-slot="image-readout" className="animate-rise space-y-7">
          <Readings detail={detail} update={update} />
          <Reference detail={detail} />
          <UsedBy detail={detail} />
          <LayerShares detail={detail} />
          <Runs detail={detail} />
          <Labels labels={detail.labels} />
          {detail.env.length > 0 && (
            <section className="space-y-2">
              <p className="eyebrow">Environment baked into the image</p>
              <Well className="max-h-48 whitespace-pre-wrap">{detail.env.join("\n")}</Well>
            </section>
          )}
        </div>
      )}
    </SidePanel>
  )
}

function FreshnessStatus({ state }: { state: Freshness }) {
  if (state === "outdated") return <Status verdict="warning" label="Update available" />
  if (state === "current") return <Status verdict="ok" label="Current" />
  return (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
      <span
        aria-hidden
        className="size-1.5 rounded-full"
        style={{ background: FRESHNESS_COLOR[state] }}
      />
      <span className="text-muted-foreground">{FRESHNESS_LABEL[state]}</span>
    </span>
  )
}

/** Four readings across the top, as the package and process sheets open. */
function Readings({ detail, update }: { detail: ImageDetail; update?: ImageUpdateStatus }) {
  const sized = detail.layers.filter((l) => l.size > 0).length
  const version = imageVersion(detail) ?? splitReference(detail.repoTags[0] ?? "").tag
  return (
    <div className="grid min-w-0 grid-cols-2 gap-x-6 gap-y-5 border-b border-hairline pb-6 sm:grid-cols-4">
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">On disk</p>
        <p className="numeric text-2xl font-semibold tracking-tight" style={{ color: SIZE_HUE }}>
          <LiveBytes value={detail.size} />
        </p>
      </div>
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">Layers</p>
        <p className="numeric text-2xl font-semibold tracking-tight">{sized}</p>
        <p className="truncate text-hint text-muted-foreground">
          of {plural(detail.layers.length, "instruction")}
        </p>
      </div>
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">Built</p>
        <p className="truncate text-title font-medium" title={timestamp(detail.created)}>
          {relativeTime(detail.created)}
        </p>
        {update?.checkedAt && (
          <p className="truncate text-hint text-muted-foreground">
            registry asked {relativeTime(update.checkedAt)}
          </p>
        )}
      </div>
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">Version</p>
        <p className="truncate font-mono text-title" title={version}>
          {version ?? "—"}
        </p>
      </div>
    </div>
  )
}

/**
 * How this image is named, and what follows from that: a registry tag can be
 * pulled and checked; a digest reference cannot go out of date; an image built
 * here has no registry copy to fall back on; a dangling one is reachable only
 * by its id.
 */
function Reference({ detail }: { detail: ImageDetail }) {
  const props = [
    detail.dangling && <Tag key="untagged">untagged</Tag>,
    detail.kind === "digest" && (
      <Tag key="digest" tone="success">
        digest pinned
      </Tag>
    ),
    detail.movingTag && (
      <Tag key="moving" tone="warning">
        moving tag
      </Tag>
    ),
    detail.localBuild && !detail.dangling && <Tag key="local">built here</Tag>,
  ].filter(Boolean)
  return (
    <section className="space-y-2.5">
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <p className="eyebrow">Reference</p>
        {props}
      </div>
      {detail.repoTags.length > 1 && (
        <p className="text-body text-muted-foreground">
          Also tagged{" "}
          {detail.repoTags.slice(1).map((t, i) => (
            <span key={t}>
              {i > 0 && ", "}
              <span className="font-mono text-foreground">{t}</span>
            </span>
          ))}
          . Removing one tag leaves the image; it goes when the last tag does.
        </p>
      )}
      {detail.movingTag && (
        <Notice title="Moving tag" tone="warning">
          <p>
            <span className="font-mono">{detail.ref}</span> may resolve to another image in the
            future — two servers running &ldquo;the same&rdquo; tag can be running different
            software, and there is no version to roll back to.
          </p>
          {detail.repoDigests.length > 0 && (
            <p className="mt-1">Pin by digest to make an update something you choose.</p>
          )}
        </Notice>
      )}
      {detail.localBuild && !detail.dangling && (
        <Hint>
          This copy was built on this server and never pushed, so there is no registry copy to pull
          back. Deleting it means rebuilding from source.
        </Hint>
      )}
      {detail.dangling && (
        <Hint>
          Every tag this image had has been taken by a rebuild, so nothing refers to it by name. It
          is reachable only by its id and is what a cleanup sweep removes first.
        </Hint>
      )}
      {detail.repoDigests.length > 0 && (
        <Disclosure quiet summary="Registry digests">
          <ul className="space-y-0.5 font-mono text-hint break-all text-muted-foreground">
            {detail.repoDigests.map((digest) => (
              <li key={digest}>{digest}</li>
            ))}
          </ul>
          <p className="mt-1.5 text-hint text-muted-foreground">
            Using one of these in place of the tag is what &ldquo;pin the digest&rdquo; means: the
            reference then names one exact image and cannot move.
          </p>
        </Disclosure>
      )}
    </section>
  )
}

/** The containers made from this image, each a way to its page. */
function UsedBy({ detail }: { detail: ImageDetail }) {
  return (
    <section className="space-y-2">
      <p className="eyebrow">Used by</p>
      {detail.usedBy.length === 0 ? (
        <Hint className="leading-relaxed">
          No container was created from this image. Safe to delete unless you are keeping it
          deliberately.
        </Hint>
      ) : (
        <ChoiceList aria-label="Containers using this image">
          {detail.usedBy.map((c) => (
            <ChoiceRow
              key={c.id}
              href={`/docker/containers/${c.id}`}
              verb={`Open ${c.name}`}
              title={c.name}
              description={
                [c.stack && `${c.stack} stack`, c.service && `service ${c.service}`]
                  .filter(Boolean)
                  .join(" · ") || shortId(c.id)
              }
              trailing={<Status state={c.state} label={c.state} />}
            />
          ))}
        </ChoiceList>
      )}
    </section>
  )
}

/** The `--tag-*` hue an instruction's verb takes: what kind of step it was. */
const VERB_HUE: Record<string, string> = {
  RUN: "var(--tag-blue)",
  COPY: "var(--tag-green)",
  ADD: "var(--tag-green)",
  ENV: "var(--tag-violet)",
  ARG: "var(--tag-violet)",
  LABEL: "var(--tag-violet)",
  CMD: "var(--tag-cyan)",
  ENTRYPOINT: "var(--tag-cyan)",
}

/**
 * Where the size went: every layer that holds bytes, in the order it was built
 * — the base image first — as a bar of its share of the largest beside the
 * instruction that wrote it. The steps that only set metadata fold under the
 * list: they are most of the history and none of the size.
 */
function LayerShares({ detail }: { detail: ImageDetail }) {
  const built = [...detail.layers].reverse()
  const sized = built.filter((l) => l.size > 0)
  const empty = built.filter((l) => l.size === 0)
  const largest = Math.max(...sized.map((l) => l.size), 1)
  return (
    <section className="space-y-3">
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <p className="eyebrow">Where the size went</p>
        <span className="numeric text-hint text-muted-foreground">
          {plural(sized.length, "layer")} · {bytes(detail.size)}
        </span>
      </div>
      <ol className="space-y-2.5">
        {sized.map((layer, index) => {
          const step = layerInstruction(layer.createdBy || layer.comment || "")
          return (
            <li
              key={`${layer.id}-${index}`}
              className="grid min-w-0 grid-cols-[4.5rem_minmax(0,1fr)] items-start gap-x-3 gap-y-1"
            >
              <span className="numeric pt-px text-right text-body font-medium">
                {bytes(layer.size, layer.size >= 1024 * 1024 ? 1 : 0)}
              </span>
              <span className="min-w-0 space-y-1.5">
                <span className="block h-1.5 overflow-hidden rounded-full bg-meter-track">
                  <span
                    className="block h-full rounded-full"
                    style={{
                      width: `${Math.max((layer.size / largest) * 100, 1.5)}%`,
                      background: SIZE_HUE,
                    }}
                  />
                </span>
                <Instruction step={step} />
              </span>
            </li>
          )
        })}
      </ol>
      {empty.length > 0 && (
        <Disclosure quiet summary={`${plural(empty.length, "step")} that add nothing to the size`}>
          <ol className="space-y-1">
            {empty.map((layer, index) => (
              <li key={`${layer.id}-empty-${index}`} className="min-w-0">
                <Instruction step={layerInstruction(layer.createdBy || layer.comment || "")} />
              </li>
            ))}
          </ol>
        </Disclosure>
      )}
    </section>
  )
}

function Instruction({ step }: { step: { verb: string; rest: string } }) {
  return (
    <span className="block min-w-0 font-mono text-xs leading-relaxed break-all text-muted-foreground">
      {step.verb && (
        <span
          className="mr-1.5 font-medium"
          style={{ color: VERB_HUE[step.verb] ?? "var(--tag-slate)" }}
        >
          {step.verb}
        </span>
      )}
      {step.verb === "RUN" ? (
        <ShellWords command={step.rest} className="inline" />
      ) : (
        <span className={cn(step.verb ? "text-foreground/80" : undefined)}>{step.rest}</span>
      )}
    </span>
  )
}

/** What a container made from this image runs, as whom, from where, and what it expects. */
function Runs({ detail }: { detail: ImageDetail }) {
  const command = [...detail.entrypoint, ...detail.command].join(" ")
  return (
    <section className="space-y-3">
      <p className="eyebrow">How it runs</p>
      {command && (
        <Well className="whitespace-pre-wrap">
          <ShellWords command={command} />
        </Well>
      )}
      <DetailList className="gap-y-2">
        <Detail label="Runs as" className="text-body">
          {detail.user || "root"}
        </Detail>
        <Detail label="Working dir" className="font-mono text-body">
          {detail.workingDir || "/"}
        </Detail>
        {detail.exposedPorts.length > 0 && (
          <Detail label="Serves on">
            <span className="flex flex-wrap gap-1.5">
              {detail.exposedPorts.map((p) => (
                <Tag key={p} mono>
                  {p}
                </Tag>
              ))}
            </span>
          </Detail>
        )}
        {detail.volumePaths.length > 0 && (
          <Detail label="Stores at">
            <span className="flex flex-wrap gap-1.5">
              {detail.volumePaths.map((p) => (
                <Tag key={p} mono>
                  {p}
                </Tag>
              ))}
            </span>
          </Detail>
        )}
      </DetailList>
      {detail.volumePaths.length > 0 && (
        <Hint className="leading-relaxed">
          Without a <Term name="volume">volume</Term> mounted there, Docker creates an unnamed one —
          the data survives, under a name nobody will recognise later.
        </Hint>
      )}
    </section>
  )
}

const OCI = "org.opencontainers.image."

/** The publisher's own statement about the image, where it made one. */
function Labels({ labels }: { labels: Record<string, string> }) {
  const entries = Object.entries(labels ?? {})
  if (entries.length === 0) return null
  const oci = entries.filter(([k]) => k.startsWith(OCI))
  const rest = entries.filter(([k]) => !k.startsWith(OCI))
  return (
    <section className="space-y-2">
      <p className="eyebrow">Labels</p>
      {oci.length > 0 && (
        <DetailList className="gap-y-2">
          {oci.map(([key, value]) => {
            const name = key.slice(OCI.length)
            const link = /^https?:\/\//.test(value)
            return (
              <Detail key={key} label={name[0].toUpperCase() + name.slice(1)} className="text-body">
                {link ? (
                  <a
                    href={value}
                    target="_blank"
                    rel="noreferrer noopener"
                    className="inline-flex min-w-0 items-center gap-1 break-all hover:underline"
                  >
                    {value}
                    <External className="size-3 shrink-0 text-muted-foreground" />
                  </a>
                ) : (
                  <span className="break-all">{value}</span>
                )}
              </Detail>
            )
          })}
        </DetailList>
      )}
      {rest.length > 0 && (
        <Disclosure quiet summary={plural(rest.length, "other label")}>
          <Well className="max-h-48 whitespace-pre-wrap">
            {rest.map(([k, v]) => `${k}=${v}`).join("\n")}
          </Well>
        </Disclosure>
      )}
    </section>
  )
}
