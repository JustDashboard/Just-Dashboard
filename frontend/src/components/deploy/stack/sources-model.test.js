import { expect, test } from "bun:test"
import {
  PRIVACY_WORD,
  addedWords,
  cutWords,
  definitionHref,
  definitionLine,
  fieldDefaults,
  fieldsToSend,
  filterDefinitions,
  privacyFilter,
  sourcesHref,
  sourcesRefusal,
  splitFields,
} from "./sources-model"

const definitions = [
  {
    definition: "openfilms",
    name: "Open Films",
    description: "Public-domain films",
    privacy: "public",
    language: "en-US",
    needsInput: false,
  },
  {
    definition: "torznab",
    name: "Generic Torznab",
    description: "Any Torznab feed you run yourself",
    privacy: "private",
    language: "en-US",
    needsInput: true,
  },
  {
    definition: "community",
    name: "Community Archive",
    description: "Needs an account",
    privacy: "semiPrivate",
    language: "fr-FR",
    needsInput: true,
  },
]

test("the search reads the name, the id and the description, and the filter the privacy", () => {
  const names = (list) => list.map((one) => one.definition)
  expect(names(filterDefinitions(definitions, "", "all"))).toEqual([
    "openfilms",
    "torznab",
    "community",
  ])
  expect(names(filterDefinitions(definitions, "FILMS", "all"))).toEqual(["openfilms"])
  expect(names(filterDefinitions(definitions, "torznab", "all"))).toEqual(["torznab"])
  expect(names(filterDefinitions(definitions, "feed yourself", "all"))).toEqual(["torznab"])
  expect(names(filterDefinitions(definitions, "", "public"))).toEqual(["openfilms"])
  // A semi-private source asks for an account too, so it is under Private.
  expect(names(filterDefinitions(definitions, "", "private"))).toEqual(["torznab", "community"])
  expect(names(filterDefinitions(definitions, "films", "private"))).toEqual([])
  expect(privacyFilter("private")).toBe("private")
  expect(privacyFilter("semiPrivate")).toBe("all")
  expect(privacyFilter(null)).toBe("all")
})

test("a long catalogue is cut with a line that says so, and a row says what it asks for", () => {
  expect(cutWords(60, 60)).toBeUndefined()
  expect(cutWords(60, 1512)).toBe("The first 60 of 1,512. Search to reach the rest.")
  expect(definitionLine(definitions[0])).toBe("Public · nothing to fill in · en-US")
  expect(definitionLine(definitions[2])).toBe("Semi-private · asks for your account · fr-FR")
  expect(PRIVACY_WORD.semiPrivate).toBe("Semi-private")
})

const fields = [
  {
    name: "baseUrl",
    label: "URL",
    type: "text",
    value: "https://feed.example.test",
    advanced: false,
  },
  { name: "apiKey", label: "API key", type: "password", value: null, advanced: false },
  { name: "minimumSeeders", label: "Minimum seeders", type: "number", value: 1, advanced: true },
  { name: "freeleech", label: "Freeleech only", type: "checkbox", value: false, advanced: false },
  {
    name: "sort",
    label: "Sort",
    type: "select",
    value: 0,
    options: [
      { value: 0, label: "Created" },
      { value: 1, label: "Seeders" },
    ],
    advanced: true,
  },
]

test("a definition's form starts from its defaults and sends each value as its schema types it", () => {
  const values = fieldDefaults(fields)
  expect(values).toEqual({
    baseUrl: "https://feed.example.test",
    apiKey: "",
    minimumSeeders: 1,
    freeleech: false,
    sort: 0,
  })
  expect(
    fieldsToSend(fields, { ...values, apiKey: "typed-key", minimumSeeders: "5", sort: "1" }),
  ).toEqual({
    baseUrl: "https://feed.example.test",
    apiKey: "typed-key",
    minimumSeeders: 5,
    freeleech: false,
    sort: 1,
  })
  // An emptied number keeps the schema's default; a choice it does not list is not sent.
  expect(fieldsToSend(fields, { ...values, minimumSeeders: "", sort: "9" })).not.toHaveProperty(
    "minimumSeeders",
  )
  expect(fieldsToSend(fields, { ...values, sort: "9" })).not.toHaveProperty("sort")
  expect(fieldsToSend(fields, { freeleech: true })).toEqual({ freeleech: true })
  const { basic, advanced } = splitFields(fields)
  expect(basic.map((field) => field.name)).toEqual(["baseUrl", "apiKey", "freeleech"])
  expect(advanced.map((field) => field.name)).toEqual(["minimumSeeders", "sort"])
})

test("adding a source is said in words, and whether Radarr and Sonarr have it", () => {
  const source = {
    id: 3,
    name: "Open Films",
    definition: "openfilms",
    privacy: "public",
    protocol: "torrent",
    enabled: true,
  }
  expect(addedWords({ source, synced: true }).title).toBe("Open Films is added")
  expect(addedWords({ source, synced: true }).body).toMatch(/shared it with Radarr and Sonarr/)
  expect(addedWords({ source, synced: false }).body).toMatch(/do not have it yet/)
})

test("the picker's refusals are words, and its addresses keep the filter", () => {
  expect(sourcesRefusal("stack_waiting")).toMatch(/not installed yet/)
  expect(sourcesRefusal("stack_no_sources")).toMatch(/no Prowlarr/)
  expect(sourcesRefusal("stack_app_unreachable")).toMatch(/Prowlarr did not answer/)
  expect(sourcesRefusal("stack_not_preset")).toMatch(/no longer one of the stack's presets/)
  expect(sourcesRefusal("internal")).toBeUndefined()
  expect(definitionHref("/deploy/7", "torznab")).toBe("/deploy/7/stack/sources?definition=torznab")
  expect(definitionHref("/deploy/7", "a b", "private")).toBe(
    "/deploy/7/stack/sources?definition=a+b&filter=private",
  )
  expect(sourcesHref("/deploy/7")).toBe("/deploy/7/stack/sources")
  expect(sourcesHref("/deploy/7", "public")).toBe("/deploy/7/stack/sources?filter=public")
})
