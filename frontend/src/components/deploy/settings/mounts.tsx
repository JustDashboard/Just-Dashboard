"use client"

import { ArrowRight, LockClosed, Servers, Trash } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { DeploymentConfiguration } from "@/lib/types"
import { Field } from "@/components/form"
import { EmptyState } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
  InputGroupToggle,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { MountMark } from "@/components/deploy/vocabulary"

/**
 * The runtime mount editor: a mount is the same three fields and a Remove
 * wherever a plan asks for one, so it is named once rather than redrawn per
 * caller.
 *
 * It was a framed box per mount inside a framed card — four labelled fields,
 * a lone "Read only" label over a switch on a baseline of its own, and a red
 * bin — and nothing on it said whether a source was a named volume or a
 * directory on this server, which is the one thing that decides what the
 * data is and where it lives. Each mount is one row now: drawn as what it
 * is (the folder in the colour Files gave it, or the volume as the product
 * whose data it keeps), the source with what kind of thing it names at its
 * end — live as it is typed, by the engine's own rule — an arrow into the
 * container, and the container path with read-only as the binary inside
 * its edge (§7). Who owns it is its second line; what the live release found
 * there is the Storage page's own list under the editor, said once.
 */
export type MountValue = NonNullable<DeploymentConfiguration["runtime"]["mounts"]>[number]

export function emptyMount(): MountValue {
  return { source: "", target: "", ownership: "linked" }
}

type Ownership = MountValue["ownership"]

/** What ownership means for a mount, a volume or a name when the project is removed — from the removal plan. */
export const OWNERSHIP: Record<Ownership, { word: string; hint: string }> = {
  managed: { word: "Managed", hint: "removed with the project" },
  linked: { word: "Linked", hint: "never removed" },
  observed: { word: "Observed", hint: "watched only; never removed" },
}

/**
 * Who owns a mount, a volume or a hostname, drawn one way wherever it is
 * asked: the word "Ownership" inside the trigger, so the value never floats
 * with nothing saying what it is, and what removal does to each choice as the
 * option's hint. A hostname can only be managed or linked, so its caller
 * narrows `options`.
 */
export function OwnershipSelect<T extends Ownership>({
  value,
  onChange,
  label,
  options = Object.keys(OWNERSHIP) as T[],
  disabled,
  className,
}: {
  value: T
  onChange: (value: T) => void
  /** The trigger's accessible name, naming what is owned. */
  label: string
  options?: T[]
  disabled?: boolean
  className?: string
}) {
  return (
    <Select value={value} onValueChange={(next) => onChange(next as T)} disabled={disabled}>
      {/* The body size at every width: this sits in a line of other small
          controls and readings, where the phone's 16px select face was the
          loudest word in the row. */}
      <SelectTrigger aria-label={label} className={cn("gap-1.5 max-sm:text-body", className)}>
        <span className="text-muted-foreground">Ownership</span>
        <SelectValue />
      </SelectTrigger>
      <SelectContent position="popper" align="start" className="min-w-72">
        {options.map((ownership) => (
          <SelectItem
            key={ownership}
            value={ownership}
            hint={<span aria-hidden>{OWNERSHIP[ownership].hint}</span>}
          >
            {OWNERSHIP[ownership].word}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

/** The engine's rule: an absolute source is a path on this server, anything else a volume. */
export function mountKind(source: string) {
  return source.startsWith("/") ? "host path" : "volume"
}

export function MountRows({
  mounts,
  onChange,
  readOnly,
  idPrefix = "mount",
  emptyLabel = "No persistent mounts. The runtime is currently stateless.",
  rowError,
  product,
}: {
  mounts: MountValue[]
  onChange: (next: MountValue[]) => void
  readOnly?: boolean
  idPrefix?: string
  emptyLabel?: string
  /** The message a save refused for this mount, when it named the mount but no sub-field. */
  rowError?: (index: number) => string | undefined
  /** The product whose data a named volume keeps — the container's image, when it names one. */
  product?: string
}) {
  if (mounts.length === 0)
    return <EmptyState icon={Servers} title="No persistent mounts" description={emptyLabel} />
  const change = (index: number, next: Partial<MountValue>) =>
    onChange(mounts.map((item, i) => (i === index ? { ...item, ...next } : item)))
  return (
    <ul aria-label="Mounts" className="@container divide-y divide-hairline">
      {mounts.map((mount, index) => {
        const source = mount.source.trim()
        return (
          <li
            key={index}
            className="grid min-w-0 grid-cols-[2rem_minmax(0,1fr)_auto] items-end gap-x-3 gap-y-3 py-4 first:pt-0 @min-[36rem]:grid-cols-[2rem_minmax(0,1fr)_0.875rem_minmax(0,1fr)_auto]"
          >
            <span className="row-start-1 mb-0.5 flex">
              <MountMark source={source} product={source ? product : undefined} />
            </span>
            <Field label="Source" htmlFor={`${idPrefix}-source-${index}`} className="row-start-1">
              <InputGroup>
                <InputGroupInput
                  id={`${idPrefix}-source-${index}`}
                  value={mount.source}
                  readOnly={readOnly}
                  placeholder="api-data or /srv/uploads"
                  onChange={(event) => change(index, { source: event.target.value })}
                  className="font-mono"
                />
                {source && (
                  <InputGroupAddon align="inline-end">
                    <InputGroupText>{mountKind(source)}</InputGroupText>
                  </InputGroupAddon>
                )}
              </InputGroup>
            </Field>
            <ArrowRight
              aria-hidden
              className="row-start-1 mb-3 hidden size-3.5 text-muted-foreground @min-[36rem]:block"
            />
            <Field
              label="Container path"
              htmlFor={`${idPrefix}-target-${index}`}
              className="col-[2/span_2] row-start-2 @min-[36rem]:col-[4] @min-[36rem]:row-start-1"
            >
              <InputGroup>
                <InputGroupInput
                  id={`${idPrefix}-target-${index}`}
                  value={mount.target}
                  readOnly={readOnly}
                  placeholder="/data"
                  onChange={(event) => change(index, { target: event.target.value })}
                  className="font-mono"
                />
                <InputGroupAddon align="inline-end" className="gap-0 p-0">
                  <InputGroupToggle
                    icon={LockClosed}
                    label="Read only"
                    aria-label="Read only"
                    pressed={Boolean(mount.readOnly)}
                    onPressedChange={(pressed) => change(index, { readOnly: pressed })}
                    disabled={readOnly}
                  />
                </InputGroupAddon>
              </InputGroup>
            </Field>
            <span className="col-start-3 row-start-1 flex @min-[36rem]:col-start-5">
              {!readOnly && (
                <IconAction
                  label={`Remove mount ${mount.target || index + 1}`}
                  onClick={() => onChange(mounts.filter((_, i) => i !== index))}
                  className="size-9 text-muted-foreground hover:text-destructive"
                >
                  <Trash />
                </IconAction>
              )}
            </span>
            {/* One shorthand per width: a start and a span given as two
                utilities lose the start to the wider width's span, which sets
                the whole grid-column and put this line under the mark. */}
            <OwnershipSelect
              value={mount.ownership}
              onChange={(ownership) => change(index, { ownership })}
              label={`Ownership of ${mount.target || `mount ${index + 1}`}`}
              disabled={readOnly}
              className="col-[2/span_2] row-start-3 @min-[36rem]:col-[2/span_4] @min-[36rem]:row-start-2"
            />
            {rowError?.(index) && (
              <p role="alert" className="col-span-full pl-11 text-hint text-destructive">
                {rowError(index)}
              </p>
            )}
          </li>
        )
      })}
    </ul>
  )
}
