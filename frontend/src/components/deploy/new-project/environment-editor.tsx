"use client"

import { Plus, Trash } from "@/components/icons"
import { Field, FormNote, FormSection } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import type { DbConnection, DeploymentDetectedDatabase } from "@/lib/types"
import { ProjectDatabase } from "@/components/deploy/project-database"
import { DATABASE_ENGINE_LABELS } from "@/components/deploy/vocabulary"
import { canGenerateSecret, generateSecretValue } from "@/components/deploy/deployment-defaults"
import type { EnvironmentRow } from "@/components/deploy/new-project/draft"

/**
 * Key/value rows plus a pasted block, the way `quick-deploy.tsx` handed
 * environment text to the draft: never part of the plan, only ever imported
 * once the project exists (§1 rule 15 — secrets never enter the URL or
 * browser storage, so this stays in component state until submit).
 */
export function EnvironmentEditor({
  rows,
  onRowsChange,
  dotenv,
  onDotenvChange,
  onConnectDatabase,
  hostNetwork,
  databases = [],
}: {
  rows: EnvironmentRow[]
  onRowsChange: (rows: EnvironmentRow[]) => void
  dotenv: string
  onDotenvChange: (value: string) => void
  onConnectDatabase: (connection: DbConnection, url: string, variable: string) => void
  hostNetwork?: boolean
  /** The engines detection found the source connecting to, each offered as one button. */
  databases?: DeploymentDetectedDatabase[]
}) {
  const update = (index: number, patch: Partial<EnvironmentRow>) =>
    onRowsChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)))
  const unset = rows.filter((row) => row.detected && !row.value).length
  // A suggestion is spent once its variable holds a value, whether the
  // sheet filled it or the operator typed it.
  const pending = databases.filter(
    (database) => !rows.some((row) => row.name === database.variable && row.value),
  )

  return (
    <FormSection title="Environment variables">
      <div className="space-y-3">
        {rows.map((row, index) => (
          <div
            key={index}
            className="grid grid-cols-[1fr_1.5fr_auto] items-end gap-2 sm:grid-cols-[1fr_1.5fr_auto]"
          >
            <Field
              label="Key"
              htmlFor={`env-key-${index}`}
              hint={row.detected && row.source ? `from ${row.source}` : undefined}
            >
              <Input
                id={`env-key-${index}`}
                value={row.name}
                autoComplete="off"
                spellCheck={false}
                placeholder="DATABASE_URL"
                className="font-mono"
                onChange={(event) => update(index, { name: event.target.value })}
              />
            </Field>
            <Field
              label="Value"
              htmlFor={`env-value-${index}`}
              hint={row.generated ? "Generated here" : undefined}
              trailing={
                canGenerateSecret(row.name) && !row.value ? (
                  <button
                    type="button"
                    className="rounded-sm text-hint text-brand focus-ring hover:underline"
                    onClick={() =>
                      update(index, { value: generateSecretValue(row.name), generated: true })
                    }
                  >
                    Generate
                  </button>
                ) : undefined
              }
            >
              <Input
                id={`env-value-${index}`}
                type="password"
                autoComplete="new-password"
                value={row.value}
                placeholder={row.example || "Enter a value"}
                className="font-mono"
                onChange={(event) =>
                  update(index, { value: event.target.value, generated: undefined })
                }
              />
            </Field>
            <IconAction
              label={`Remove variable ${row.name || index + 1}`}
              onClick={() => onRowsChange(rows.filter((_, i) => i !== index))}
            >
              <Trash />
            </IconAction>
          </div>
        ))}
        {unset > 0 && (
          <FormNote>
            {unset === 1
              ? "1 detected variable has no value yet and will not be set."
              : `${unset} detected variables have no value yet and will not be set.`}
          </FormNote>
        )}
        {pending.length > 0 && (
          <ul aria-label="Detected databases" className="space-y-2">
            {pending.map((database) => (
              <li
                key={database.engine}
                className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 text-hint text-muted-foreground"
              >
                <Tag>{DATABASE_ENGINE_LABELS[database.engine] ?? database.engine}</Tag>
                <span className="min-w-0 truncate">
                  <span className="font-mono">{database.variable}</span> · {database.evidence}
                </span>
                <ProjectDatabase
                  target={hostNetwork ? "host" : "container"}
                  onConnect={onConnectDatabase}
                  label={`Add ${DATABASE_ENGINE_LABELS[database.engine] ?? database.engine}`}
                  initialEngine={database.engine}
                  initialVariable={database.variable}
                />
              </li>
            ))}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onRowsChange([...rows, { name: "", value: "" }])}
          >
            <Plus className="size-3.5" /> Add variable
          </Button>
          <ProjectDatabase
            target={hostNetwork ? "host" : "container"}
            onConnect={onConnectDatabase}
          />
        </div>
        <details>
          <summary className="cursor-pointer rounded-sm py-2 text-xs text-muted-foreground focus-ring">
            Paste .env
          </summary>
          <Textarea
            aria-label="Environment variables"
            value={dotenv}
            onChange={(event) => onDotenvChange(event.target.value)}
            rows={4}
            placeholder={"API_KEY=…\nNEXT_PUBLIC_SITE_URL=https://…"}
            className="mt-2 font-mono text-xs"
          />
        </details>
        <FormNote>
          Encrypted when saved. Available to the build command and at runtime. Values embedded into
          browser assets, including NEXT_PUBLIC_ and VITE_ values, are public.
        </FormNote>
      </div>
    </FormSection>
  )
}
