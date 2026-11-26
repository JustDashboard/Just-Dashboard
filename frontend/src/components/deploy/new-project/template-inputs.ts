import type {
  BlueprintDetail,
  BlueprintInput,
  DeploymentDraftSource,
  WorkloadProfile,
} from "@/lib/types"

/** Match the catalogue's numeric, format and acceptance rules before creating a draft. */
export function templateInputErrors(inputs: BlueprintInput[], values: Record<string, string>) {
  const errors: Record<string, string> = {}
  for (const input of inputs) {
    if (input.kind === "secret") continue
    const raw = values[input.name] ?? input.default ?? ""
    const value = raw.trim()
    if (input.required && (input.kind === "accept" ? value !== "true" : !value)) {
      errors[input.name] =
        input.kind === "accept" ? "Accept this agreement to continue." : "Required."
      continue
    }
    if (!value) continue
    if (input.kind === "text") {
      // The server tests the value as typed, so this does too. A pattern this
      // engine cannot read is left to the server, which wrote it.
      if (input.pattern && matchesPattern(input.pattern, raw) === false)
        errors[input.name] = "This is not in the expected format."
      continue
    }
    if (input.kind !== "number" && input.kind !== "memory") continue
    const number = Number(value)
    if (!/^[+-]?\d+$/.test(value) || !Number.isSafeInteger(number))
      errors[input.name] = "Enter a whole number."
    else if (input.minimum !== undefined && number < input.minimum)
      errors[input.name] = `Use at least ${input.minimum}.`
    else if (input.maximum !== undefined && number > input.maximum)
      errors[input.name] = `Use at most ${input.maximum}.`
  }
  return errors
}

function matchesPattern(pattern: string, value: string) {
  try {
    return new RegExp(pattern).test(value)
  } catch {
    return undefined
  }
}

/**
 * The draft source a template is deployed as, and the profile it is created
 * under: one reviewed image is a blueprint source, and a template of several
 * services runs through the Compose release path, rendered by the server from
 * the definition. Secrets are never sent from here; an input nobody touched is
 * left for the definition's own default.
 */
export function templateDraftSource(
  definition: Pick<BlueprintDetail, "id" | "version" | "profile" | "inputs" | "services">,
  values: Record<string, string>,
): { profile: WorkloadProfile; source: DeploymentDraftSource } {
  const stack = (definition.services?.length ?? 0) > 0
  return {
    profile: definition.profile === "game" ? "game" : stack ? "compose" : "service",
    source: {
      kind: stack ? "compose" : "blueprint",
      mode: "blueprint",
      blueprintId: definition.id,
      blueprintVersion: definition.version,
      blueprintInputs: Object.fromEntries(
        (definition.inputs ?? [])
          .filter((input) => input.kind !== "secret" && input.name in values)
          .map((input) => [input.name, values[input.name]]),
      ),
    },
  }
}
