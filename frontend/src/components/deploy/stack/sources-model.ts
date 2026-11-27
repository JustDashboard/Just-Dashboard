import type {
  SmartStackSourceAdded,
  SmartStackSourceDefinition,
  SmartStackSourceField,
  SmartStackSourcePrivacy,
} from "@/lib/types"

/**
 * The reads and words of a stack's media-source picker
 * (`/deploy/[id]/stack/sources`), kept apart from the page so they are
 * unit-tested (sources-model.test.js). The sources are Prowlarr's indexers,
 * and the page never names them so: a person adds a source of films and
 * series, and Prowlarr is where the stack keeps it.
 */

/** How open a source is, as one word. */
export const PRIVACY_WORD: Record<SmartStackSourcePrivacy, string> = {
  public: "Public",
  semiPrivate: "Semi-private",
  private: "Private",
}

/** The two kinds the picker filters by: a semi-private source asks for an account too. */
export type PrivacyFilter = "all" | "public" | "private"

export const PRIVACY_FILTERS: { value: PrivacyFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "public", label: "Public" },
  { value: "private", label: "Private" },
]

export function privacyFilter(value: string | null | undefined): PrivacyFilter {
  return value === "public" || value === "private" ? value : "all"
}

/**
 * The definitions a search and a filter leave, in the catalogue's order (the
 * server sorts it by name). The search reads the name, the definition's id
 * and its description, without case.
 */
export function filterDefinitions(
  definitions: SmartStackSourceDefinition[],
  query: string,
  filter: PrivacyFilter,
): SmartStackSourceDefinition[] {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  return definitions.filter((definition) => {
    if (filter === "public" && definition.privacy !== "public") return false
    if (filter === "private" && definition.privacy === "public") return false
    const text =
      `${definition.name} ${definition.definition} ${definition.description}`.toLowerCase()
    return words.every((word) => text.includes(word))
  })
}

/** A list of hundreds is cut to its first rows; the search reaches the rest. */
export const DEFINITIONS_SHOWN = 60

/** The foot under a cut list: "The first 60 of 512. Search to reach the rest." */
export function cutWords(shown: number, total: number): string | undefined {
  if (total <= shown) return undefined
  return `The first ${shown} of ${total.toLocaleString("en")}. Search to reach the rest.`
}

/** What a definition's row says under its name: its kind, and that it asks for an account. */
export function definitionLine(
  definition: Pick<SmartStackSourceDefinition, "privacy" | "needsInput" | "language">,
): string {
  return [
    PRIVACY_WORD[definition.privacy],
    definition.needsInput ? "asks for your account" : "nothing to fill in",
    definition.language,
  ]
    .filter(Boolean)
    .join(" · ")
}

export type FieldValue = string | number | boolean

/** The form's first values: each field's default, a checkbox off and a list's first choice unset. */
export function fieldDefaults(fields: SmartStackSourceField[]): Record<string, FieldValue> {
  return Object.fromEntries(
    fields.map((field) => {
      const value = field.value
      if (field.type === "checkbox") return [field.name, value === true]
      if (value === null || value === undefined) return [field.name, ""]
      if (typeof value === "boolean") return [field.name, value ? "true" : ""]
      return [field.name, value]
    }),
  )
}

/**
 * The values `POST …/sources` is sent, typed as Prowlarr's schema has them: a
 * number as a number, a checkbox as a boolean, a list's choice as the
 * option's own value. A number field left empty is left out, so the schema's
 * own default stands.
 */
export function fieldsToSend(
  fields: SmartStackSourceField[],
  values: Record<string, FieldValue>,
): Record<string, FieldValue> {
  const sent: Record<string, FieldValue> = {}
  for (const field of fields) {
    const value = values[field.name]
    if (value === undefined) continue
    if (field.type === "checkbox") {
      sent[field.name] = value === true
      continue
    }
    if (field.type === "number") {
      const text = String(value).trim()
      if (!text) continue
      const number = Number(text)
      if (Number.isFinite(number)) sent[field.name] = number
      continue
    }
    if (field.type === "select") {
      const option = field.options?.find((one) => String(one.value) === String(value))
      if (option) sent[field.name] = option.value
      continue
    }
    sent[field.name] = String(value)
  }
  return sent
}

/** The fields drawn at once, and the ones behind "More settings". */
export function splitFields(fields: SmartStackSourceField[]) {
  return {
    basic: fields.filter((field) => !field.advanced),
    advanced: fields.filter((field) => field.advanced),
  }
}

/** What adding a source came to, in words. */
export function addedWords(added: SmartStackSourceAdded): { title: string; body: string } {
  const name = added.source.name
  return added.synced
    ? {
        title: `${name} is added`,
        body: "Prowlarr tested it and shared it with Radarr and Sonarr, so a request in Seerr is searched for there from now on.",
      }
    : {
        title: `${name} is added to Prowlarr`,
        body: "Radarr and Sonarr do not have it yet. The stack's next pass shares it with them, and its page says when that is done.",
      }
}

/**
 * Why the picker cannot be used, in words, for the refusals every one of its
 * routes may answer. Anything else is the caller's error to show as it is.
 */
export function sourcesRefusal(code: string | undefined): string | undefined {
  switch (code) {
    case "stack_waiting":
      return "The stack is not installed yet. Sources can be added once the deployment's run has brought it up."
    case "stack_no_sources":
      return "This preset has no Prowlarr, so it keeps no media sources. The Complete preset does."
    case "stack_not_preset":
      return "This project is no longer one of the stack's presets, so the dashboard does not manage its sources."
    case "stack_app_unreachable":
      return "Prowlarr did not answer. It may still be starting, or its container has stopped."
    case "stack_not_found":
      return "This project is not a stack."
    default:
      return undefined
  }
}

/** Where the picker opens one definition: the page's own address with it in `?definition=`. */
export function definitionHref(base: string, definition: string, filter?: PrivacyFilter): string {
  const query = new URLSearchParams({ definition })
  if (filter && filter !== "all") query.set("filter", filter)
  return `${base}/stack/sources?${query.toString()}`
}

/** The picker with nothing open, keeping its filter. */
export function sourcesHref(base: string, filter?: PrivacyFilter): string {
  return filter && filter !== "all"
    ? `${base}/stack/sources?filter=${filter}`
    : `${base}/stack/sources`
}
