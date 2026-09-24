"use client"

import { Plus, Trash } from "@/components/icons"
import { Disclosure, Field, FormNote, FormSection } from "@/components/form"
import { Tag } from "@/components/tag"
import { IconAction } from "@/components/icon-action"
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
import { ProductGlyph } from "@/components/product-logo"
import { DATABASE_ENGINE_LABELS, HOSTED_DATABASE_LABELS } from "@/components/deploy/vocabulary"
import {
  browserInlined,
  canGenerateSecret,
  generateSecretValue,
  pointsAtLocalhost,
  rowNeedsOperator,
} from "@/components/deploy/deployment-defaults"
import type { EnvironmentRow } from "@/components/deploy/new-project/draft"

/**
 * Key/value rows plus a pasted block, the way `quick-deploy.tsx` handed
 * environment text to the draft: encrypted separately from the plan and
 * committed with the project (§1 rule 15 — secrets never enter the URL or
 * browser storage, so Configure holds this in memory, where it survives a
 * walk to another page and nothing else, until submit).
 */
export function EnvironmentEditor({
  rows,
  onRowsChange,
  dotenv,
  onDotenvChange,
  retainedKeys = [],
  onRemoveRetainedKey,
  onConnectDatabase,
  hostNetwork,
  databases = [],
  planned = {},
  browserPrefixes,
}: {
  rows: EnvironmentRow[]
  onRowsChange: (rows: EnvironmentRow[]) => void
  dotenv: string
  onDotenvChange: (value: string) => void
  retainedKeys?: string[]
  onRemoveRetainedKey?: (key: string) => void
  onConnectDatabase: (
    connection: DbConnection,
    url: string,
    variable: string,
    database?: DeploymentDetectedDatabase,
  ) => void
  hostNetwork?: boolean
  /** The engines detection found the source connecting to, each offered as one button. */
  databases?: DeploymentDetectedDatabase[]
  /** The value each set-up row falls back to: its bound address or its default. */
  planned?: Record<string, string>
  /** The prefixes the framework compiles into browser code; conventions when unknown. */
  browserPrefixes?: string[]
}) {
  const update = (index: number, patch: Partial<EnvironmentRow>) =>
    onRowsChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)))
  const unset = rows.filter(
    (row) => rowNeedsOperator(row) && !retainedKeys.includes(row.name),
  ).length
  // A suggestion is spent once its variable holds a value, whether the
  // sheet filled it or the operator typed it — unless the value points at
  // localhost, which inside the container is the app itself, so a pasted
  // development .env does not hide the database the app still needs.
  const pending = databases.filter(
    (database) =>
      !retainedKeys.includes(database.variable) &&
      !rows.some(
        (row) =>
          row.name === database.variable && row.value && !pointsAtLocalhost(row.name, row.value),
      ),
  )

  return (
    <FormSection title="Environment variables">
      <div className="space-y-3">
        {retainedKeys.length > 0 && (
          <div className="space-y-2">
            <FormNote>
              These values are saved encrypted with this setup. Enter a value with the same key to
              replace one.
            </FormNote>
            {retainedKeys.map((name) => (
              <div key={name} className="flex items-center justify-between gap-2">
                <Tag mono>{name}</Tag>
                <IconAction
                  label={`Remove saved variable ${name}`}
                  onClick={() => onRemoveRetainedKey?.(name)}
                >
                  <Trash />
                </IconAction>
              </div>
            ))}
          </div>
        )}
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
              label={
                row.browser || browserInlined(row.name, browserPrefixes) ? (
                  <span className="inline-flex items-center gap-1.5">
                    Key
                    <Tag tone="warning">public</Tag>
                  </span>
                ) : (
                  "Key"
                )
              }
              htmlFor={`env-key-${index}`}
              hint={keyHint(row, browserPrefixes)}
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
              hint={valueHint(row, planned[row.name])}
            >
              <InputGroup>
                <InputGroupInput
                  id={`env-value-${index}`}
                  type="password"
                  autoComplete="new-password"
                  value={row.value}
                  placeholder={valuePlaceholder(row, planned[row.name])}
                  className="font-mono"
                  onChange={(event) =>
                    update(index, { value: event.target.value, generated: undefined })
                  }
                />
                <InputGroupAddon align="inline-end" className="gap-0 p-0">
                  {canGenerateSecret(row.name) && !row.value && row.setup !== "generate" && (
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
            {pending.map((database) => {
              const engine = provisionEngine(database)
              const label = DATABASE_ENGINE_LABELS[engine] ?? engine
              return (
                <li
                  key={database.engine}
                  className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 text-hint text-muted-foreground"
                >
                  {/* The engine as itself, the mark it carries in the sheet that
                      connects it and on the Databases page it lands on. */}
                  <span className="inline-flex items-center gap-1.5">
                    <ProductGlyph id={database.engine} />
                    <Tag>{DATABASE_ENGINE_LABELS[database.engine] ?? database.engine}</Tag>
                  </span>
                  <span className="min-w-0 truncate">
                    <span className="font-mono">{database.variable}</span> · {database.evidence}
                  </span>
                  {/* A driver that speaks only its provider's protocol never
                      talks to a database started here, so the one honest
                      offer is the provider's own connection string. */}
                  {database.hosted ? (
                    <span className="basis-full">
                      Speaks {HOSTED_DATABASE_LABELS[database.hosted] ?? database.hosted}: paste its
                      connection string into <span className="font-mono">{database.variable}</span>.
                    </span>
                  ) : (
                    <ProjectDatabase
                      target={hostNetwork ? "host" : "container"}
                      onConnect={(connection, url, variable) =>
                        onConnectDatabase(connection, url, variable, database)
                      }
                      label={`Add ${label}`}
                      initialEngine={engine}
                      initialVariable={database.variable}
                      format={database.format}
                    />
                  )}
                </li>
              )
            })}
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
            className="font-mono sm:text-xs"
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

/** The quick-setup engine for a suggestion: a schema that needs pgvector or PostGIS gets that server. */
function provisionEngine(database: DeploymentDetectedDatabase) {
  if (database.engine !== "postgres") return database.engine
  if (database.extensions?.includes("vector")) return "pgvector"
  if (database.extensions?.includes("postgis")) return "postgis"
  return database.engine
}

function keyHint(row: EnvironmentRow, browserPrefixes?: string[]) {
  const source = row.detected && row.source ? `from ${row.source}` : undefined
  if (row.browser || browserInlined(row.name, browserPrefixes))
    return [source, "compiled into the browser bundle"].filter(Boolean).join(" · ")
  return source
}

/**
 * What a set-up row falls back to reads as its placeholder, so an empty field
 * says what it will hold. The generated secret is the exception: the server
 * mints it at commit and it is never shown here.
 */
function valuePlaceholder(row: EnvironmentRow, planned?: string) {
  if (row.value) return undefined
  switch (row.setup) {
    case "generate":
      return "Generated when the project is created"
    case "domain":
      return planned || "Follows the project's domain"
    case "default":
      return planned || row.example || "Enter a value"
    case "paste":
      return "Paste the value"
  }
  return row.example || "Enter a value"
}

function valueHint(row: EnvironmentRow, planned?: string) {
  if (row.value && pointsAtLocalhost(row.name, row.value))
    return (
      <span className="text-warning">
        Points at localhost, which inside the container is the app itself.
      </span>
    )
  if (row.generated) return "Generated here"
  if (row.value) return undefined
  if (row.setup === "domain" && !planned)
    return <span className="text-warning">Add a domain, or enter the public address.</span>
  if (row.setup) return row.reason ? capitalize(row.reason) : undefined
  if (row.localhostIn)
    return (
      <span className="text-warning">{row.localhostIn} sets a localhost address; set it here.</span>
    )
  if (row.required) return "Required: read at start-up with no default."
  return undefined
}

function capitalize(text: string) {
  return text.charAt(0).toUpperCase() + text.slice(1) + (text.endsWith(".") ? "" : ".")
}
