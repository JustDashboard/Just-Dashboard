/**
 * The widths at which a Docker detail page's main column has the room an
 * `xl` and a `2xl` window gave it before the list stood beside it as a rail.
 *
 * A container's and a stack's pages were laid out for the full width of the
 * page. From `xl` a rail of eighteen rem and its gap take 312px of that, so
 * a block that went side by side at 1280px does so at 1600px now, and one
 * that waited for 1536px waits for 1856px. Between, the column is as wide as
 * a tablet's and draws what a tablet draws. The same two figures are written
 * as `min-[1600px]:` and `min-[1856px]:` where a class decides.
 */
export const WIDE_BESIDE_RAIL = "(min-width: 1600px)"
export const ROOMY_BESIDE_RAIL = "(min-width: 1856px)"
