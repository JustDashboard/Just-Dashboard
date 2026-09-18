"use client"

import { Trash } from "@/components/icons"
import type { DeploymentConfiguration } from "@/lib/types"
import { Field } from "@/components/form"
import { Group } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"

/**
 * The runtime mount editor, shared by Storage settings and the new-project
 * Advanced section: a mount is the same three fields and a Remove wherever a
 * plan asks for one, so it is named once rather than redrawn per caller.
 */
export type MountValue = NonNullable<DeploymentConfiguration["runtime"]["mounts"]>[number]

export function emptyMount(): MountValue {
  return { source: "", target: "", ownership: "linked" }
}

export function MountRows({
  mounts,
  onChange,
  readOnly,
  idPrefix = "mount",
  emptyLabel = "No persistent mounts. The runtime is currently stateless.",
  rowError,
}: {
  mounts: MountValue[]
  onChange: (next: MountValue[]) => void
  readOnly?: boolean
  idPrefix?: string
  emptyLabel?: string
  /** The message a save refused for this mount, when it named the mount but no sub-field. */
  rowError?: (index: number) => string | undefined
}) {
  if (mounts.length === 0) return <EmptyNote>{emptyLabel}</EmptyNote>
  return (
    <div className="space-y-3">
      {mounts.map((mount, index) => (
        <Group
          key={index}
          className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_9rem_auto_auto] lg:items-end"
        >
          <Field label="Source" htmlFor={`${idPrefix}-source-${index}`}>
            <Input
              id={`${idPrefix}-source-${index}`}
              value={mount.source}
              readOnly={readOnly}
              onChange={(event) =>
                onChange(
                  mounts.map((item, i) =>
                    i === index ? { ...item, source: event.target.value } : item,
                  ),
                )
              }
              className="font-mono"
            />
          </Field>
          <Field label="Container path" htmlFor={`${idPrefix}-target-${index}`}>
            <Input
              id={`${idPrefix}-target-${index}`}
              value={mount.target}
              readOnly={readOnly}
              onChange={(event) =>
                onChange(
                  mounts.map((item, i) =>
                    i === index ? { ...item, target: event.target.value } : item,
                  ),
                )
              }
              className="font-mono"
            />
          </Field>
          <Field label="Ownership" htmlFor={`${idPrefix}-ownership-${index}`}>
            <Select
              value={mount.ownership}
              onValueChange={(ownership: MountValue["ownership"]) =>
                onChange(mounts.map((item, i) => (i === index ? { ...item, ownership } : item)))
              }
              disabled={readOnly}
            >
              <SelectTrigger id={`${idPrefix}-ownership-${index}`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="managed">Managed</SelectItem>
                <SelectItem value="linked">Linked</SelectItem>
                <SelectItem value="observed">Observed</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Read only" htmlFor={`${idPrefix}-readonly-${index}`}>
            <Switch
              id={`${idPrefix}-readonly-${index}`}
              checked={Boolean(mount.readOnly)}
              onCheckedChange={(checked) =>
                onChange(
                  mounts.map((item, i) => (i === index ? { ...item, readOnly: checked } : item)),
                )
              }
              disabled={readOnly}
            />
          </Field>
          {!readOnly && (
            <IconAction
              label={`Remove mount ${mount.target || index + 1}`}
              onClick={() => onChange(mounts.filter((_, i) => i !== index))}
              className="text-destructive"
            >
              <Trash />
            </IconAction>
          )}
          {rowError?.(index) && (
            <p role="alert" className="text-hint text-destructive sm:col-span-2 lg:col-span-5">
              {rowError(index)}
            </p>
          )}
        </Group>
      ))}
    </div>
  )
}
