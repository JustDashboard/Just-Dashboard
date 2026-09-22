import type { BlueprintInput } from "@/lib/types"

/** Match the catalogue's numeric and acceptance rules before creating a draft. */
export function templateInputErrors(inputs: BlueprintInput[], values: Record<string, string>) {
  const errors: Record<string, string> = {}
  for (const input of inputs) {
    if (input.kind === "secret") continue
    const value = (values[input.name] ?? input.default ?? "").trim()
    if (input.required && (input.kind === "accept" ? value !== "true" : !value)) {
      errors[input.name] =
        input.kind === "accept" ? "Accept this agreement to continue." : "Required."
      continue
    }
    if (!value || (input.kind !== "number" && input.kind !== "memory")) continue
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
