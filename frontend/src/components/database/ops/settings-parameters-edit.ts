import {
  boolWords,
  isOn,
  parameterProblem,
  readableValue,
  type Parameter,
} from "@/components/database/ops/settings-parameters-model"

/**
 * What the Parameters page holds while parameters are edited in their rows:
 * the value each control starts from, which rows differ from the server, what
 * each would send, and the one line a row says under its control. No React.
 *
 * A parameter was changed in a dialog with a Save of its own, one at a time.
 * They are edited in their rows now and saved together by the page's bar, so
 * the page holds a draft per parameter and sends one request for each.
 */

/** A row's draft: a value typed or chosen, or the wish to go back to the default. */
export type ParameterEdit = { value: string } | { reset: true }

export type ParameterEdits = Record<string, ParameterEdit>

/**
 * What a parameter's control shows before it is touched: the engine's own
 * word for a yes/no, nothing for a secret (it is never sent back), and the
 * value as the server prints it otherwise.
 */
export function startValue(parameter: Parameter): string {
  if (parameter.secret) return ""
  if (parameter.type === "bool") {
    const words = boolWords(parameter.value)
    return isOn(parameter.value) ? words.on : words.off
  }
  return parameter.value
}

/**
 * What the control shows now: the draft, the default while a reset is staged,
 * a value just saved until the list is read again (`held`), or the start.
 */
export function shownValue(
  parameter: Parameter,
  edit: ParameterEdit | undefined,
  held?: string,
): string {
  if (edit && "reset" in edit) return parameter.default ?? ""
  if (edit) return edit.value
  return held ?? startValue(parameter)
}

export type ParameterChange = {
  parameter: Parameter
  edit: ParameterEdit
  /** Why the server would refuse it, said before it is sent. */
  problem?: string
}

/**
 * The rows whose draft differs from what the server has, in the list's
 * order. A value typed back to what it was is not a change, and a reset of a
 * parameter already at its default is none either.
 */
export function parameterChanges(
  parameters: readonly Parameter[],
  edits: ParameterEdits,
  held: Record<string, string> = {},
): ParameterChange[] {
  const changes: ParameterChange[] = []
  for (const parameter of parameters) {
    const edit = edits[parameter.name]
    if (!edit) continue
    if ("reset" in edit) {
      if (parameter.changed) changes.push({ parameter, edit })
      continue
    }
    if (edit.value === (held[parameter.name] ?? startValue(parameter))) continue
    changes.push({ parameter, edit, problem: parameterProblem(parameter, edit.value) })
  }
  return changes
}

/** What `PUT /databases/{id}/settings` is sent for one row. */
export function settingBody(
  change: Pick<ParameterChange, "parameter" | "edit">,
): { name: string; value: string } | { name: string; reset: true } {
  return "reset" in change.edit
    ? { name: change.parameter.name, reset: true }
    : { name: change.parameter.name, value: change.edit.value.trim() }
}

/** What `PUT /databases/{id}/redis/config` is sent for one row. */
export function configBody(
  change: Pick<ParameterChange, "parameter" | "edit">,
  rewrite: boolean,
): { name: string; value: string; rewrite?: true } {
  return {
    name: change.parameter.name,
    value: "reset" in change.edit ? "" : change.edit.value.trim(),
    ...(rewrite ? { rewrite: true as const } : {}),
  }
}

/**
 * The line under a row's value: the value in a unit a person thinks in, then
 * the default when the row is not at it. One line, so a row is one height
 * whatever it has to say.
 */
export function valueFacts(parameter: Parameter, shown: string): string {
  const readable = readableValue({ value: shown.trim(), unit: parameter.unit })
  const differs =
    parameter.default !== undefined && !parameter.secret && shown.trim() !== parameter.default
  return [
    readable,
    differs && (parameter.changed || shown !== startValue(parameter))
      ? `default ${parameter.default || "empty"}`
      : undefined,
  ]
    .filter(Boolean)
    .join(" · ")
}

/** A parameter's bounds as the page says them: "64 to 2147483647", or nothing. */
export function rangeWords(parameter: Pick<Parameter, "min" | "max">): string | undefined {
  return parameter.min !== undefined || parameter.max !== undefined
    ? `${parameter.min ?? "…"} to ${parameter.max ?? "…"}`
    : undefined
}
