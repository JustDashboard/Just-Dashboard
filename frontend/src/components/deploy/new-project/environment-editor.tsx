"use client"

import { Plus, Trash } from "@/components/icons"
import { Disclosure, Field, FormNote, FormSection } from "@/components/form"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
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
 * browser storage, so Configure holds this in memory, where it survives a
 * walk to another page and nothing else, until submit).
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
          /* Two fields, and nothing in a third column. The remove used to
             stand in one, `items-end`, so it aligned to the bottom of
             whichever field happened to be carrying a hint — "from
             package.json" under a key, "Generated here" under a value — and
             sat a line below the inputs on every row that had one (§6). It
             is inside the value's own edge now, where there is nothing to
             mis-measure. */
          <div key={index} className="grid min-w-0 gap-3 sm:grid-cols-[1fr_1.5fr]">
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
            {/* Generate and the remove belong to the value, so they are
                inside its edge rather than beside it: "Generate" was a bare
                blue word up at the label, a rank the form uses for nothing
                else, and the remove was a third box in the row that only
                lined up with the inputs on the rows with no hint under them.
                One field, two things you can do to it. */}
            <Field
              label="Value"
              htmlFor={`env-value-${index}`}
              hint={row.generated ? "Generated here" : undefined}
            >
              <InputGroup>
                <InputGroupInput
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
                <InputGroupAddon align="inline-end" className="gap-0 p-0">
                  {canGenerateSecret(row.name) && !row.value && (
                    <InputGroupButton
                      className="text-brand"
                      onClick={() =>
                        update(index, { value: generateSecretValue(row.name), generated: true })
                      }
                    >
                      Generate
                    </InputGroupButton>
                  )}
                  <InputGroupButton
                    aria-label={`Remove variable ${row.name || index + 1}`}
                    onClick={() => onRowsChange(rows.filter((_, i) => i !== index))}
                  >
                    <Trash className="size-3.5" />
                  </InputGroupButton>
                </InputGroupAddon>
              </InputGroup>
            </Field>
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
        <Disclosure
          quiet
          summary="Paste .env"
          facts="KEY=value per line, merged with the rows above"
        >
          <Textarea
            aria-label="Environment variables"
            value={dotenv}
            onChange={(event) => onDotenvChange(event.target.value)}
            rows={4}
            placeholder={"API_KEY=…\nNEXT_PUBLIC_SITE_URL=https://…"}
            className="font-mono text-xs"
          />
        </Disclosure>
        <FormNote>
          Encrypted when saved. Available to the build command and at runtime. Values embedded into
          browser assets, including NEXT_PUBLIC_ and VITE_ values, are public.
        </FormNote>
      </div>
    </FormSection>
  )
}
