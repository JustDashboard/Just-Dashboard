/**
 * vh: a height is measured against the small viewport (MOB-23).
 *
 * `vh` changes as a phone's toolbars come and go, so a well or a dialog sized in it jumps while
 * it is read. An arbitrary value's `vh` becomes `svh`; `h-[min(75vh,40rem)]`, the workbench well
 * written seven times, becomes `h-workbench`. `h-screen` and its kin are reported: a full-height
 * pane may want `dvh` rather than `svh`, and that is a choice per pane.
 */
import { classTransform, type Rule, type Transform, withUtility } from "./core"

const WORKBENCH = /^h-\[min\(75vh,_?40rem\)\]$/
const VH = /(\d+(?:\.\d+)?)vh\b/g
const SCREEN = /^(h|min-h|max-h)-screen$/

const rule = (): Rule => (token) => {
  const u = token.utility
  if (WORKBENCH.test(u)) return { to: withUtility(token, "h-workbench") }
  if (/^[a-z-]+-\[.*\d+vh/.test(u)) return { to: withUtility(token, u.replace(VH, "$1svh")) }
  const screen = SCREEN.exec(u)
  if (screen)
    return {
      review: "100vh: svh for a well, dvh for a pane that fills the screen",
      suggest: withUtility(token, `${screen[1]}-svh`),
    }
  return undefined
}

export const vh: Transform = {
  name: "vh",
  summary: "h-[..vh] to svh, h-[min(75vh,40rem)] to h-workbench",
  closes: ["MOB-23"],
  requires: [
    {
      file: "src/app/globals.css",
      pattern: /--height-workbench\s*:/,
      why: "h-workbench exists (WP-01)",
    },
  ],
  apply: (text, file, options) => classTransform("vh", rule)(text, file, options),
}
