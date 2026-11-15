/** The rail key of the connection path investigator. */
export const PATH_TOOL = "path"

/**
 * Where an old `/network/investigate` address lands: the Tools workbench with
 * Connection path chosen and every parameter the investigator read
 * (`target`, `port`, `protocol`, `family`, `measure`, `container`) carried
 * across untouched, so the tool fills the same fields the page did. A `tool`
 * in the old address is not the investigator's and is dropped rather than
 * allowed to choose another tool.
 */
export function pathToolHref(
  query: Record<string, string | string[] | undefined> | URLSearchParams,
): string {
  const params = new URLSearchParams()
  params.set("tool", PATH_TOOL)
  const entries: [string, string][] =
    query instanceof URLSearchParams
      ? [...query.entries()]
      : Object.entries(query).flatMap(([key, value]) =>
          (Array.isArray(value) ? value : value === undefined ? [] : [value]).map(
            (one): [string, string] => [key, one],
          ),
        )
  for (const [key, value] of entries) if (key !== "tool") params.append(key, value)
  return `/network/tools?${params}`
}

/**
 * The example a tool's empty pane offers as a button: the first of the
 * placeholder's alternatives ("example.com or 203.0.113.9" offers
 * "example.com"). It fills the target; running it is still a press of Run,
 * because a probe is traffic this server sends.
 */
export function exampleTarget(placeholder: string | undefined): string | undefined {
  const first = placeholder?.split(/\s+or\s+/)[0]?.trim()
  return first || undefined
}
