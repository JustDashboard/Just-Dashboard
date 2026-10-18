import type { BackupResource } from "@/lib/types"

export type BackupInputField =
  "sources" | "excludes" | "sqlitePaths" | "pauseContainers" | "databaseDumps"

export type BackupInputs = Record<BackupInputField, string[]>

export type ResourcePicks = {
  manual: BackupInputs
  picks: Record<string, BackupInputs>
}

const fields: BackupInputField[] = [
  "sources",
  "excludes",
  "sqlitePaths",
  "pauseContainers",
  "databaseDumps",
]

export function backupInputs(suggestion: Partial<BackupResource["suggest"]>): BackupInputs {
  return {
    sources: suggestion.sources ?? [],
    excludes: suggestion.excludes ?? [],
    sqlitePaths: suggestion.sqlitePaths ?? [],
    pauseContainers: suggestion.pauseContainers ?? [],
    databaseDumps: (suggestion.databaseDumps ?? []).map(String),
  }
}

export function startResourcePicks(inputs: BackupInputs, resourceKey?: string): ResourcePicks {
  return resourceKey
    ? { manual: backupInputs({}), picks: { [resourceKey]: inputs } }
    : { manual: inputs, picks: {} }
}

export function pickedInputs(state: ResourcePicks): BackupInputs {
  const values = backupInputs({})
  for (const field of fields) {
    values[field] = [
      ...new Set([...state.manual[field], ...Object.values(state.picks).flatMap((p) => p[field])]),
    ]
  }
  return values
}

/** Each resource owns its suggestion; a shared or manually supplied value has another owner. */
export function toggleResourcePick(
  state: ResourcePicks,
  resourceKey: string,
  suggestion: BackupInputs,
  on: boolean,
): ResourcePicks {
  const picks = { ...state.picks }
  if (on) picks[resourceKey] = suggestion
  else delete picks[resourceKey]
  return { ...state, picks }
}

/** Manual edits keep existing ownership for unchanged lines and own newly entered values. */
export function editPickedInput(
  state: ResourcePicks,
  field: BackupInputField,
  values: string[],
): ResourcePicks {
  const picked = Object.values(state.picks).flatMap((p) => p[field])
  const manual = values.filter((v) => state.manual[field].includes(v) || !picked.includes(v))
  const picks = Object.fromEntries(
    Object.entries(state.picks).map(([key, pick]) => [
      key,
      { ...pick, [field]: pick[field].filter((v) => values.includes(v)) },
    ]),
  )
  return { manual: { ...state.manual, [field]: manual }, picks }
}
