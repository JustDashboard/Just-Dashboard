import { plural } from "@/lib/format"
import type { BlueprintSummary, UserTemplate } from "@/lib/types"

/**
 * The words and reads of templates added on this dashboard — the picker's
 * "Your templates", the templates page and Save as template — kept apart from
 * the components so they are unit-tested (templates.test.js) and so the three
 * surfaces say the same thing.
 */

/**
 * The namespace of a template added on this dashboard: the backend's
 * `blueprint.UserPrefix`. A built-in can never have an id in it, so a
 * template somebody saved never stands in for a reviewed one.
 */
export const USER_TEMPLATE_PREFIX = "user-"

export function isUserTemplate(id: string | undefined): boolean {
  return Boolean(id?.startsWith(USER_TEMPLATE_PREFIX))
}

/**
 * The catalogue as the picker draws it: the reviewed built-ins on their
 * shelves, and this dashboard's own in a section of their own, each in the
 * server's order. An entry without an origin is read from its id.
 */
export function splitCatalogue(entries: BlueprintSummary[]) {
  const builtIn: BlueprintSummary[] = []
  const yours: BlueprintSummary[] = []
  for (const entry of entries) {
    const user = entry.origin ? entry.origin === "user" : isUserTemplate(entry.id)
    ;(user ? yours : builtIn).push(entry)
  }
  return { builtIn, yours }
}

/**
 * The id a name is saved under — the backend's `blueprint.UserID` — so a
 * dialog can say it before the press: the name as a slug, in the user
 * namespace, "user-" typed by the reader not doubled, 64 characters at most.
 */
export function templateId(name: string): string | undefined {
  let slug = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
  if (slug.startsWith(USER_TEMPLATE_PREFIX)) slug = slug.slice(USER_TEMPLATE_PREFIX.length)
  const limit = 64 - USER_TEMPLATE_PREFIX.length
  if (slug.length > limit) slug = slug.slice(0, limit).replace(/-+$/, "")
  return slug ? USER_TEMPLATE_PREFIX + slug : undefined
}

/** How a template added here was made, in the words its row uses. */
export function templateOrigin(template: Pick<UserTemplate, "source">): string {
  switch (template.source) {
    case "compose":
      return "From a Compose file"
    case "deployment":
      return "Saved from a deployment"
    default:
      return "Imported"
  }
}

/** What a template runs, in a phrase: its services, or its one image. */
export function templateRuns(summary: Pick<BlueprintSummary, "image" | "services">): string {
  const services = summary.services ?? []
  if (services.length > 0) {
    return `${plural(services.length, "service")}: ${services.map((service) => service.name).join(", ")}`
  }
  return summary.image
}

/**
 * Who deploys it. Each deployment made from a template runs the copy its
 * source froze, so the count is said beside Remove, which stops none of them.
 */
export function templateUse(deployments: number): string {
  if (deployments === 0) return "No deployment uses it"
  return deployments === 1 ? "1 deployment uses it" : `${deployments} deployments use it`
}

/** Where a chosen template comes from, beside its settings in the picker. */
export function templateProvenance(definition: {
  id: string
  provenance: { reviewedAt: string; maintainer: string }
}): string {
  const { reviewedAt, maintainer } = definition.provenance
  return isUserTemplate(definition.id)
    ? `Added ${reviewedAt} by ${maintainer} on this dashboard.`
    : `Reviewed ${reviewedAt} by ${maintainer}.`
}

/** The picker, opened with this template chosen. */
export function templateDeployHref(id: string): string {
  return `/deploy/new?source=template&template=${encodeURIComponent(id)}`
}

/** The file an export is saved as. */
export function templateFilename(id: string): string {
  return `${id}.json`
}

/**
 * The two ways to make one, said wherever there are none yet: Save as
 * template on a running deployment's settings, and a Compose file — or a
 * definition exported from another dashboard — on the Templates page.
 */
export const YOUR_TEMPLATES_EMPTY =
  "Templates you add appear here. Save a running deployment as one from its Settings → General, or add one from a Compose file on the Templates page."
