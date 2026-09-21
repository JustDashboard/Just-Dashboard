import type { ConfigureStepKey } from "@/components/deploy/new-project/draft"

/**
 * Which screen decides one part of the plan, and where on it.
 *
 * Three things need this answer and used to each have their own half of it:
 * the plan drawing beside the form, whose four nodes are pressable; a
 * preflight finding, whose "Open it" names a plan field rather than a
 * control; and `?mode=advanced`. While Configure was one screen the answer
 * was a scroll target and a fold to force open, and the map existed only
 * because half the fields were hidden behind two disclosures — a finding
 * about a health check pointed at a control nobody could see.
 *
 * Now the answer is a *step* first and a scroll target second, so one map
 * serves all three and a finding lands on the screen that owns its field.
 */
export type PlanSection =
  "source" | "build" | "runtime" | "limits" | "storage" | "address" | "checks" | "variables"

export const SECTION_IDS = {
  source: "plan-source",
  build: "plan-build",
  runtime: "plan-runtime",
  limits: "plan-limits",
  storage: "plan-storage",
  address: "plan-address",
  checks: "plan-checks",
  variables: "plan-variables",
} as const satisfies Record<PlanSection, string>

export const SECTION_STEPS = {
  source: "project",
  build: "project",
  runtime: "runtime",
  limits: "runtime",
  storage: "runtime",
  address: "runtime",
  checks: "runtime",
  variables: "variables",
} as const satisfies Record<PlanSection, ConfigureStepKey>

/**
 * The fold a section is behind on its own step, which has to be opened before
 * there is anything to scroll to.
 *
 * `runtime` is the one entry whose fold is not its own scroll target: the plan
 * drawing's Runtime node reads back both the port — the step's first field, in
 * the open — and the limits, which are a fold. Pressing it lands on the port
 * and opens the limits, so either answer is one press away.
 */
export const SECTION_FOLDS: Partial<Record<PlanSection, string>> = {
  build: SECTION_IDS.build,
  runtime: SECTION_IDS.limits,
  limits: SECTION_IDS.limits,
  storage: SECTION_IDS.storage,
  checks: SECTION_IDS.checks,
  variables: SECTION_IDS.variables,
}

/**
 * The plan field names `/detect` and preflight use, longest prefix first so
 * `configuration.build` is not read as `configuration` and `runtime.mounts`
 * is not read as `runtime`.
 *
 * `build.startCommand` is here as well as `configuration.build`: the schema
 * tooling emits the short spelling, and only the long one was mapped — so the
 * one finding that asks for a start command to be changed offered a link that
 * did nothing.
 */
const FIELD_SECTIONS: { prefix: string; section: PlanSection }[] = [
  { prefix: "configuration.build", section: "build" },
  { prefix: "build", section: "build" },
  { prefix: "detection", section: "source" },
  { prefix: "source", section: "source" },
  { prefix: "runtime.mounts", section: "storage" },
  { prefix: "runtime.internalPort", section: "runtime" },
  // bindAddress, hostPort, strategy, memoryMb, cpus — every one of them a
  // field of the resources fold.
  { prefix: "runtime", section: "limits" },
  { prefix: "domains", section: "address" },
  { prefix: "checks", section: "checks" },
  { prefix: "variables", section: "variables" },
  { prefix: "dependencies", section: "variables" },
]

export function sectionForField(fieldId: string | undefined): PlanSection | undefined {
  if (!fieldId) return undefined
  return FIELD_SECTIONS.find((entry) => fieldId.startsWith(entry.prefix))?.section
}
