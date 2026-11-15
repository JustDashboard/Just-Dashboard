/**
 * Socket history's views, and which one an address opens. Under the recorder
 * and its picture the page was five blocks down one column; they are three
 * views named in the address (`?tab=`) now, one for each thing the picture
 * says the recorder keeps or reads:
 *
 * - `hours`: the day's socket hours with the chosen one read out, and the
 *   hours of that day that were recorded. One view, because both read the
 *   day the table's toolbar chooses;
 * - `observer`: the opt-in kernel packet observer;
 * - `sample`: the newest sample the recorder took, and what a reading here
 *   can and cannot say.
 */

export const FLOW_VIEWS = ["hours", "observer", "sample"] as const
export type FlowViewKey = (typeof FLOW_VIEWS)[number]

export const FLOW_VIEW_NAME: Record<FlowViewKey, string> = {
  hours: "Socket hours",
  observer: "Kernel observer",
  sample: "Latest sample",
}

/** The blocks a node of the picture leads to, and the view each is in. */
export const FLOW_ANCHOR_VIEW = {
  "flow-rows": "hours",
  "flow-hours": "hours",
  "flow-observer": "observer",
  "flow-sample": "sample",
} as const satisfies Record<string, FlowViewKey>
export type FlowAnchor = keyof typeof FLOW_ANCHOR_VIEW

/** The view an address opens: its `?tab=`, else the socket hours. */
export function flowViewOf(tab: string | null | undefined): FlowViewKey {
  return FLOW_VIEWS.find((view) => view === tab) ?? "hours"
}

/** The address of a view: the default one has no `tab`. */
export function flowHref(view: FlowViewKey) {
  return view === "hours" ? "/network/flows" : `/network/flows?tab=${view}`
}
