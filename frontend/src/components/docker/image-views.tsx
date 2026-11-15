"use client"

import { Box, Copy, External, Layers, Trash } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { ImageDetail } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ShellWords } from "@/components/deploy/run-evidence"
import { Term } from "@/components/docker/explain"
import {
  imageNames,
  imageSteps,
  imageVersion,
  shortId,
  type ImageStep,
} from "@/components/docker/images"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Disclosure, InfoTip } from "@/components/form"
import { rowReveal } from "@/components/icon-action"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { RankList, SplitBar } from "@/components/rank-list"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * The views of one image, under its picture: where its size went, who runs
 * it, how it was built and how a container made from it runs. Each was a
 * block of the sheet that used to open over the table.
 */

/** A section's count, at the far end of its title line. */
function Count({ children }: { children: React.ReactNode }) {
  return <span className="numeric text-hint text-muted-foreground">{children}</span>
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
const hueOf = (verb: string) => VERB_HUE[verb] ?? "var(--tag-slate)"

/**
 * One instruction on one line, its verb in its hue, opening the whole of it.
 *
 * A build step can be six hundred characters of shell. Wrapped, each row was
 * as tall as its command and the list could not be read down; cut to a line,
 * the row keeps its height and the command is a press away, coloured as a
 * command, with what it added and when.
 */
function Instruction({ step, of }: { step: ImageStep; of: number }) {
  const words = [step.verb, step.rest].filter(Boolean).join(" ") || "no instruction recorded"
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Step ${step.step}: ${words}`}
          className="block max-w-full min-w-0 truncate rounded-sm text-left font-mono text-xs focus-ring hover:underline"
        >
          {step.verb && (
            <span className="mr-1.5 font-medium" style={{ color: hueOf(step.verb) }}>
              {step.verb}
            </span>
          )}
          <span className={cn(!step.verb && !step.rest && "text-muted-foreground")}>
            {step.rest || (step.verb ? "" : words)}
          </span>
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[32rem] space-y-3">
        <p className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 text-body">
          <span className="font-medium">
            Step {step.step} of {of}
          </span>
          <span className="numeric text-hint text-muted-foreground">
            {step.size > 0 ? `adds ${bytes(step.size)}` : "adds nothing to the size"}
            {Date.parse(step.created) > 0 && ` · ${timestamp(step.created)}`}
          </span>
        </p>
        <Well className="max-h-64 break-all whitespace-pre-wrap">
          {step.verb && (
            <span className="mr-1.5 font-medium" style={{ color: hueOf(step.verb) }}>
              {step.verb}
            </span>
          )}
          {step.verb === "RUN" ? (
            <ShellWords command={step.rest} className="inline" />
          ) : (
            step.rest || words
          )}
        </Well>
        {step.comment && step.comment !== step.rest && (
          <p className="font-mono text-hint break-all text-muted-foreground">{step.comment}</p>
        )}
      </PopoverContent>
    </Popover>
  )
}

/**
 * Where the size went: one strip of the whole image, a span per layer in the
 * hue of the instruction that wrote it, and under it the layers that hold
 * bytes in the order they were built, the base image first, each with its
 * size and its share. The steps that only set metadata are most of the
 * history and none of the size: they are in History.
 */
export function ImageLayers({ detail }: { detail: ImageDetail }) {
  const steps = imageSteps(detail.layers)
  const sized = steps.filter((step) => step.size > 0)
  const total = sized.reduce((sum, step) => sum + step.size, 0)
  return (
    <Panel plain className="animate-rise" data-slot="image-layers">
      <PanelHeader
        title="Where the size went"
        actions={
          <Count>
            {plural(sized.length, "layer")} · {bytes(detail.size)}
          </Count>
        }
      />
      {sized.length === 0 ? (
        <p className="py-4 text-body text-muted-foreground">
          Docker kept no history for this image, so its size cannot be told apart by layer.
        </p>
      ) : (
        <div className="flex min-w-0 flex-col gap-3 pt-4">
          <SplitBar
            label={`${plural(sized.length, "layer")} of ${bytes(detail.size)}, in build order`}
            parts={sized.map((step) => ({
              key: String(step.step),
              count: step.size,
              color: hueOf(step.verb),
            }))}
          />
          <RankList
            items={sized.map((step) => ({
              key: String(step.step),
              swatch: hueOf(step.verb),
              label: <Instruction step={step} of={steps.length} />,
              value: bytes(step.size, step.size >= 1024 * 1024 ? 1 : 0),
              share: total > 0 ? step.size / total : undefined,
            }))}
          />
        </div>
      )}
    </Panel>
  )
}

/** The containers made from this image, each a way to its page. */
export function ImageContainers({
  detail,
  onRemove,
}: {
  detail: ImageDetail
  /** Removes the image, for an account that may: the next step when nothing uses it. */
  onRemove?: () => void
}) {
  const tag = imageNames(detail)[0]
  const product = tag ? imageProduct(tag) : undefined
  if (detail.usedBy.length === 0) {
    return (
      <EmptyState
        icon={Box}
        className="animate-rise"
        title="No container uses this image"
        description={`Removing it frees up to ${bytes(detail.size)}.`}
        action={
          onRemove && (
            <Button size="sm" variant="outline" onClick={onRemove}>
              <Trash className="size-4" />
              Remove
            </Button>
          )
        }
      />
    )
  }
  return (
    <Panel plain className="animate-rise" data-slot="image-containers">
      <PanelHeader title="Containers" actions={<Count>{detail.usedBy.length}</Count>} />
      <ChoiceList aria-label="Containers using this image" className="pt-4">
        {detail.usedBy.map((container) => (
          <ChoiceRow
            key={container.id}
            href={`/docker/containers/${container.id}`}
            verb={`Open ${container.name}`}
            leading={<ProductLogo id={product} fallback={Box} size="sm" />}
            title={container.name}
            description={
              <span className="font-mono">
                {[container.stack, container.service].filter(Boolean).join(" · ") ||
                  shortId(container.id)}
              </span>
            }
            trailing={<Status state={container.state} label={container.state} />}
          />
        ))}
      </ChoiceList>
    </Panel>
  )
}

/**
 * How the image was built: every instruction in build order, the ones that
 * set only metadata included, with what each added. A table, because it is
 * read down its columns: which step, when, how much.
 */
export function ImageHistory({ detail, wide }: { detail: ImageDetail; wide: boolean }) {
  const steps = imageSteps(detail.layers)
  if (steps.length === 0) {
    return (
      <EmptyState
        icon={Layers}
        className="animate-rise"
        title="No build history"
        description="Docker kept no record of the steps that made this image."
      />
    )
  }
  return (
    <Panel className="animate-rise" data-slot="image-history">
      <PanelHeader
        title="History"
        actions={<Count>{plural(steps.length, "step")}</Count>}
        className="min-h-11"
      />
      <Table className="table-fixed">
        <TableHeader>
          <TableRow>
            <TableHead className="w-12">Step</TableHead>
            <TableHead>Instruction</TableHead>
            {wide && <TableHead className="w-32">Built</TableHead>}
            <TableHead className="w-24 text-right">Adds</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {steps.map((step) => (
            <TableRow key={step.step}>
              <TableCell className="numeric text-muted-foreground">{step.step}</TableCell>
              <TableCell>
                <Instruction step={step} of={steps.length} />
              </TableCell>
              {wide && (
                <TableCell className="text-muted-foreground" title={timestamp(step.created)}>
                  {Date.parse(step.created) > 0 ? relativeTime(step.created) : "—"}
                </TableCell>
              )}
              <TableCell
                className={cn("numeric text-right", step.size === 0 && "text-muted-foreground")}
              >
                {step.size > 0 ? bytes(step.size, step.size >= 1024 * 1024 ? 1 : 0) : "—"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  )
}

const OCI = "org.opencontainers.image."

/**
 * What a container made from this image runs, how the image is named, and
 * what its publisher wrote on it.
 *
 * The sheet said what each kind of reference means in a paragraph under it.
 * Here each is a word with its explanation one hover away, so the section is
 * the facts.
 */
export function ImageConfiguration({ detail }: { detail: ImageDetail }) {
  const command = [...detail.entrypoint, ...detail.command].join(" ")
  const names = imageNames(detail)
  const version = imageVersion(detail)
  const labels = Object.entries(detail.labels ?? {})
  const oci = labels.filter(([key]) => key.startsWith(OCI) && key !== `${OCI}version`)
  const rest = labels.filter(([key]) => !key.startsWith(OCI))
  return (
    <div className="flex min-w-0 animate-rise flex-col gap-8" data-slot="image-configuration">
      <Panel plain>
        <PanelHeader title="How it runs" />
        <div className="flex min-w-0 flex-col gap-4 pt-4">
          {command && (
            <Well className="max-h-28 break-all whitespace-pre-wrap">
              <ShellWords command={command} />
            </Well>
          )}
          <DetailList className="gap-y-2">
            <Detail label="Runs as" className="font-mono">
              {detail.user || "root"}
            </Detail>
            <Detail label="Working dir" className="font-mono break-all">
              {detail.workingDir || "/"}
            </Detail>
            {detail.exposedPorts.length > 0 && (
              <Detail label="Serves on">
                <span className="flex flex-wrap gap-1.5">
                  {detail.exposedPorts.map((port) => (
                    <Tag key={port} mono>
                      {port}
                    </Tag>
                  ))}
                </span>
              </Detail>
            )}
            {detail.volumePaths.length > 0 && (
              <Detail
                label={
                  <span className="inline-flex items-center gap-1.5">
                    Stores at
                    <InfoTip label="What happens to these paths">
                      Without a volume mounted there, Docker creates an unnamed one: the data
                      survives, under a name nobody will recognise later.
                    </InfoTip>
                  </span>
                }
              >
                <span className="flex flex-wrap gap-1.5">
                  {detail.volumePaths.map((path) => (
                    <Tag key={path} mono>
                      {path}
                    </Tag>
                  ))}
                </span>
              </Detail>
            )}
            <Detail label="Platform" className="font-mono">
              {detail.os ?? "?"}/{detail.architecture ?? "?"}
            </Detail>
            {version && (
              <Detail label="Version" className="font-mono">
                {version}
              </Detail>
            )}
            {detail.author && <Detail label="Author">{detail.author}</Detail>}
          </DetailList>
        </div>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Names"
          actions={<Kinds detail={detail} />}
          // The kinds are words with a hover each: they wrap under the title
          // on a phone rather than pushing it off its line.
          className="gap-y-1.5"
        />
        <DetailList className="gap-y-2 pt-4">
          <Detail label="Id">
            <button
              type="button"
              onClick={() => void copyText(detail.id, "ID copied")}
              aria-label="Copy the image's id"
              className="group inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm text-left font-mono break-all focus-ring hover:text-foreground"
            >
              {detail.id}
              <Copy aria-hidden className={cn("size-3 shrink-0", rowReveal())} />
            </button>
          </Detail>
          <Detail label={<Term name="tag">Tags</Term>}>
            {names.length === 0 ? (
              <span className="text-muted-foreground">none</span>
            ) : (
              <ul className="space-y-1 font-mono">
                {names.map((name) => (
                  <li key={name} className="break-all">
                    {name}
                  </li>
                ))}
              </ul>
            )}
          </Detail>
          {detail.repoDigests.length > 0 && (
            <Detail
              label={
                <span className="inline-flex items-center gap-1.5">
                  Digests
                  <InfoTip label="What a digest is for">
                    Using one of these in place of the tag pins the reference: it then names one
                    exact image and cannot move.
                  </InfoTip>
                </span>
              }
            >
              <ul className="space-y-1 font-mono text-muted-foreground">
                {detail.repoDigests.map((digest) => (
                  <li key={digest} className="break-all">
                    {digest}
                  </li>
                ))}
              </ul>
            </Detail>
          )}
        </DetailList>
      </Panel>

      {labels.length > 0 && (
        <Panel plain>
          <PanelHeader title="Labels" actions={<Count>{labels.length}</Count>} />
          <div className="flex min-w-0 flex-col gap-3 pt-4">
            {oci.length > 0 && (
              <DetailList className="gap-y-2">
                {oci.map(([key, value]) => {
                  const name = key.slice(OCI.length)
                  const link = /^https?:\/\//.test(value)
                  return (
                    <Detail key={key} label={name[0].toUpperCase() + name.slice(1)}>
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
            {rest.length > 0 &&
              (oci.length > 0 ? (
                <Disclosure quiet summary={plural(rest.length, "other label")}>
                  <LabelWell labels={rest} />
                </Disclosure>
              ) : (
                <LabelWell labels={rest} />
              ))}
          </div>
        </Panel>
      )}

      {detail.env.length > 0 && (
        <Panel plain>
          <PanelHeader title="Environment" actions={<Count>{detail.env.length}</Count>} />
          <Well className="mt-4 max-h-64 break-all whitespace-pre-wrap">
            {detail.env.join("\n")}
          </Well>
        </Panel>
      )}
    </div>
  )
}

function LabelWell({ labels }: { labels: [string, string][] }) {
  return (
    <Well className="max-h-48 break-all whitespace-pre-wrap">
      {labels.map(([key, value]) => `${key}=${value}`).join("\n")}
    </Well>
  )
}

/**
 * What kind of reference names the image, and what follows from it: a moving
 * tag can point elsewhere tomorrow, a digest cannot, a local build has no
 * registry copy, a dangling layer has no name at all.
 */
function Kinds({ detail }: { detail: ImageDetail }) {
  const kinds: { key: string; word: React.ReactNode; why: React.ReactNode }[] = []
  const words: Record<string, string> = {
    untagged: "untagged",
    digest: "digest pinned",
    moving: "a moving tag",
    local: "built here",
  }
  if (detail.dangling) {
    kinds.push({
      key: "untagged",
      word: <Tag>untagged</Tag>,
      why: "Every tag this image had was taken by a rebuild or a pull, so nothing refers to it by name. It is reachable only by its id, and is what a cleanup removes first.",
    })
  }
  if (detail.kind === "digest") {
    kinds.push({
      key: "digest",
      word: <Tag tone="success">digest pinned</Tag>,
      why: "It is named by its digest, which is one exact image and cannot move.",
    })
  }
  if (detail.movingTag) {
    kinds.push({
      key: "moving",
      word: <Tag tone="warning">moving tag</Tag>,
      why: (
        <>
          <span className="font-mono">{detail.ref}</span> may resolve to another image in the
          future: two servers running the same tag can be running different software, and there is
          no version to roll back to.
          {detail.repoDigests.length > 0 &&
            " Pin by digest to make an update something you choose."}
        </>
      ),
    })
  }
  if (detail.localBuild && !detail.dangling) {
    kinds.push({
      key: "local",
      word: <Tag>built here</Tag>,
      why: "This copy was built on this server and never pushed, so no registry holds one to pull back. Deleting it means rebuilding from source.",
    })
  }
  if (kinds.length === 0) return null
  return (
    <>
      {kinds.map((kind) => (
        <span key={kind.key} className="inline-flex items-center gap-1.5">
          {kind.word}
          <InfoTip label={`What ${words[kind.key]} means`}>{kind.why}</InfoTip>
        </span>
      ))}
    </>
  )
}
