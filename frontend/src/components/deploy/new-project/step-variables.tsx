"use client"

import { useState } from "react"
import { Disclosure } from "@/components/form"
import { Notice } from "@/components/state"
import { VariableReferences } from "@/components/deploy/new-project/configure-advanced"
import { EnvironmentEditor } from "@/components/deploy/new-project/environment-editor"
import { detectedVariableDeclarations } from "@/components/deploy/deployment-defaults"
import {
  railsDatabaseRows,
  type ConfigureFlow,
  type EnvironmentRow,
  type FlowUpdate,
} from "@/components/deploy/new-project/draft"
import { SECTION_IDS } from "@/components/deploy/new-project/plan-sections"

/**
 * Step three: **what it needs to run.**
 *
 * Two different things wear the word "variable" in this product and they used
 * to sit eight sections apart on one screen: the values saved encrypted with
 * the draft — typed here, held in memory, never in the URL or
 * Web Storage — and the variables the *plan* declares, which name a stored or
 * generated value and carry the scopes deciding which build steps may read
 * them. The first is the screen; the second is the fold under it, open
 * already when a blueprint declared some, because a generated password and a
 * EULA acceptance are review material rather than a power-user setting.
 */
export function StepVariables({
  flow,
  onFlowChange,
  rows,
  onRowsChange,
  dotenv,
  onDotenvChange,
  platformSkipped,
  retainedKeys,
  onRemoveRetainedKey,
  suppliedVariables,
  referencesOpen,
}: {
  flow: ConfigureFlow
  onFlowChange: (next: FlowUpdate) => void
  rows: EnvironmentRow[]
  onRowsChange: (next: EnvironmentRow[] | ((rows: EnvironmentRow[]) => EnvironmentRow[])) => void
  dotenv: string
  onDotenvChange: (value: string) => void
  /** Names the paste sets that the deployment sets itself, which are left out. */
  platformSkipped: string[]
  retainedKeys: string[]
  onRemoveRetainedKey: (key: string) => void
  suppliedVariables: string[]
  referencesOpen: boolean
}) {
  const configuration = flow.configuration
  const setConfiguration = (next: typeof configuration) =>
    onFlowChange({ ...flow, configuration: next })
  const declared = configuration.variables.length
  // Seeded once, the way the build fold's own state is: the fold opens for the
  // same reason the sequence landed on this screen, and the first character
  // typed into the last empty reference answers that reason. Read on every
  // render it would shut itself under the reader's hands mid-word, because
  // `open` on a `<details>` is written again whenever the prop changes.
  const [openOnArrival] = useState(referencesOpen)
  const [referenceError, setReferenceError] = useState("")
  const changeReferences = (next: typeof configuration) => {
    const replacements = next.variables
      .filter((variable) => {
        const previous = configuration.variables.find((entry) => entry.name === variable.name)
        return (
          (variable.reference && variable.reference !== previous?.reference) ||
          (variable.generate && variable.generate !== previous?.generate)
        )
      })
      .map((variable) => variable.name)
    const pasted = new Set(
      Array.from(
        dotenv.matchAll(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=/gm),
        (match) => match[1],
      ),
    )
    const conflicts = replacements.filter((name) => pasted.has(name))
    if (conflicts.length) {
      setReferenceError(
        `Remove ${conflicts.join(", ")} from the pasted .env before changing its reference or generator.`,
      )
      return
    }
    setReferenceError("")
    if (replacements.length) {
      onRowsChange((current) => current.filter((row) => !replacements.includes(row.name)))
      for (const name of replacements) onRemoveRetainedKey(name)
    }
    setConfiguration(next)
  }

  // What each set-up row falls back to: the address it follows, or its default.
  const planned = Object.fromEntries(
    configuration.variables
      .filter((variable) => variable.sensitivity === "plain" && variable.value)
      .map((variable) => [variable.name, variable.value ?? ""]),
  )
  // Removing a detected row removes what the plan would have supplied for it
  // too, so a secret the operator deleted is not generated behind their back.
  const detectedNames = new Set(
    detectedVariableDeclarations(flow.candidate, flow.profile).map((variable) => variable.name),
  )
  const changeRows = (next: EnvironmentRow[]) => {
    const kept = new Set(next.map((row) => row.name))
    const removed = rows
      .filter((row) => row.detected && detectedNames.has(row.name) && !kept.has(row.name))
      .map((row) => row.name)
    onRowsChange(next)
    if (removed.length)
      setConfiguration({
        ...configuration,
        variables: configuration.variables.filter((variable) => !removed.includes(variable.name)),
      })
  }

  return (
    <>
      <EnvironmentEditor
        rows={rows}
        onRowsChange={changeRows}
        dotenv={dotenv}
        onDotenvChange={onDotenvChange}
        platformSkipped={platformSkipped}
        retainedKeys={retainedKeys}
        onRemoveRetainedKey={onRemoveRetainedKey}
        hostNetwork={configuration.runtime.hostNetwork}
        databases={flow.candidate?.databases}
        planned={planned}
        browserPrefixes={flow.candidate?.browserPrefixes}
        onConnectDatabase={(connection, url, variable, database) => {
          // Rails 8 reads a URL per database — cache, queue, cable — on the
          // same server; each follows the link as its own database name.
          const extras = url.startsWith("${{database.")
            ? railsDatabaseRows(connection.id, connection.database, database?.alsoVariables)
            : []
          const replaced = new Set([variable, ...extras.map((row) => row.name)])
          onRowsChange((current) => [
            ...current.filter((row) => row.name && !replaced.has(row.name)),
            { name: variable, value: url },
            ...extras,
          ])
          setConfiguration({
            ...configuration,
            dependencies: [
              ...configuration.dependencies.filter(
                (item) =>
                  item.resourceKind !== "database_connection" ||
                  item.resourceId !== String(connection.id),
              ),
              {
                kind: "database",
                ownership: "linked",
                resourceKind: "database_connection",
                resourceId: String(connection.id),
                config: {},
              },
            ],
          })
        }}
      />

      <Disclosure
        id={SECTION_IDS.variables}
        summary="Variable references & scopes"
        // Deliberately not "stored or generated value", which is how the
        // section inside words it: a fold's `facts` join its accessible name,
        // and a control named "…generated values" answers a test looking for
        // the environment row's own "Generate" button.
        facts={
          declared > 0
            ? `${declared} declared by the plan`
            : "Plan-declared names, not literal secrets"
        }
        open={openOnArrival}
      >
        {referenceError && (
          <Notice tone="warning" title="Remove the pasted value first">
            {referenceError}
          </Notice>
        )}
        <VariableReferences
          configuration={configuration}
          onChange={changeReferences}
          overriddenNames={suppliedVariables}
        />
      </Disclosure>
    </>
  )
}
