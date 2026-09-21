"use client"

import { Disclosure } from "@/components/form"
import { VariableReferences } from "@/components/deploy/new-project/configure-advanced"
import { EnvironmentEditor } from "@/components/deploy/new-project/environment-editor"
import type {
  ConfigureFlow,
  EnvironmentRow,
  FlowUpdate,
} from "@/components/deploy/new-project/draft"
import { SECTION_IDS } from "@/components/deploy/new-project/plan-sections"

/**
 * Step three: **what it needs to run.**
 *
 * Two different things wear the word "variable" in this product and they used
 * to sit eight sections apart on one screen: the values imported into the
 * project once it exists — typed here, held in memory, never in the URL or
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
  referencesOpen,
}: {
  flow: ConfigureFlow
  onFlowChange: (next: FlowUpdate) => void
  rows: EnvironmentRow[]
  onRowsChange: (next: EnvironmentRow[] | ((rows: EnvironmentRow[]) => EnvironmentRow[])) => void
  dotenv: string
  onDotenvChange: (value: string) => void
  referencesOpen: boolean
}) {
  const configuration = flow.configuration
  const setConfiguration = (next: typeof configuration) =>
    onFlowChange({ ...flow, configuration: next })
  const declared = configuration.variables.length

  return (
    <>
      <EnvironmentEditor
        rows={rows}
        onRowsChange={onRowsChange}
        dotenv={dotenv}
        onDotenvChange={onDotenvChange}
        hostNetwork={configuration.runtime.hostNetwork}
        databases={flow.candidate?.databases}
        onConnectDatabase={(connection, url, variable) => {
          onRowsChange((current) => [
            ...current.filter((row) => row.name && row.name !== variable),
            { name: variable, value: url },
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
        open={referencesOpen}
      >
        <VariableReferences configuration={configuration} onChange={setConfiguration} />
      </Disclosure>
    </>
  )
}
