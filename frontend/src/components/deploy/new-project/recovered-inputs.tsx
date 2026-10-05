"use client"

import { Disclosure, Field, FormNote, FormSection } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { Tag } from "@/components/tag"
import { recoveredEnvironmentGroups, type RecoveredInput } from "@/lib/workload-import"
import type { EnvironmentRow } from "./draft"

/** Captured aliases belong to the source recipe; replacements address their original app key. */
export function RecoveredInputs({
  inputs,
  retainedKeys,
  rows,
  onRowsChange,
}: {
  inputs: RecoveredInput[]
  retainedKeys: string[]
  rows: EnvironmentRow[]
  onRowsChange: (next: EnvironmentRow[] | ((current: EnvironmentRow[]) => EnvironmentRow[])) => void
}) {
  const groups = recoveredEnvironmentGroups(inputs)
  if (groups.length === 0) return null

  const inputRow = (input: RecoveredInput) => {
    const replacement = rows.find((row) => row.name === input.storageKey && !row.detected)
    const retained = retainedKeys.includes(input.storageKey)
    const label = `${input.service ? `${input.service} · ` : ""}${input.name}`
    return (
      <div key={input.storageKey} className="min-w-0 space-y-2">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
          <span className="min-w-0 font-mono text-body break-all">{input.name}</span>
          <div className="flex shrink-0 items-center gap-2">
            <Tag>
              {replacement
                ? "Replacement"
                : retained
                  ? input.empty
                    ? "Empty · retained"
                    : "Retained"
                  : "Missing"}
            </Tag>
            <Button
              type="button"
              size="xs"
              variant="ghost"
              aria-label={`${replacement ? "Keep captured value for" : "Replace"} ${label}`}
              onClick={() =>
                onRowsChange((current) => [
                  ...current.filter((row) => row.name !== input.storageKey),
                  ...(replacement ? [] : [{ name: input.storageKey, value: "" }]),
                ])
              }
            >
              {replacement ? "Keep captured" : "Replace"}
            </Button>
          </div>
        </div>
        {replacement && (
          <Field
            label={`New value for ${label}`}
            htmlFor={`recovered-${input.storageKey}`}
            hint="Saved encrypted when you continue. An empty replacement sets an empty value."
          >
            <Textarea
              id={`recovered-${input.storageKey}`}
              value={replacement.value}
              autoComplete="off"
              spellCheck={false}
              rows={2}
              className="font-mono"
              onChange={(event) =>
                onRowsChange((current) =>
                  current.map((row) =>
                    row.name === input.storageKey ? { ...row, value: event.target.value } : row,
                  ),
                )
              }
            />
          </Field>
        )}
      </div>
    )
  }

  return (
    <FormSection title="Recovered environment">
      <FormNote>
        The original application keys and values are already captured. Values stay encrypted on the
        server. Replace a value only when you want the next Deploy changes to use something
        different.
      </FormNote>
      {groups.map(({ service, application, imageDefaults }) => (
        <div key={service} className="min-w-0 space-y-3">
          <p className="font-mono text-body font-medium">{service}</p>
          <div className="space-y-3">{application.map(inputRow)}</div>
          {imageDefaults.length > 0 && (
            <Disclosure
              summary={`${service} image defaults`}
              facts={`${imageDefaults.length} retained`}
            >
              <div className="space-y-3">{imageDefaults.map(inputRow)}</div>
            </Disclosure>
          )}
        </div>
      ))}
    </FormSection>
  )
}
