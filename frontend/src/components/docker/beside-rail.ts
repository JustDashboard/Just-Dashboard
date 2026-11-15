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

/**
 * `lg`, for a block inside such a page that goes side by side there: true
 * from 1024px as before, not between 1280px and 1400px, where the rail has
 * just taken its width and the column is narrower than it was at 1024px.
 * Written `lg:max-xl:` with `min-[1400px]:` where a class decides.
 */
export const LG_BESIDE_RAIL = "(min-width: 1024px) and (max-width: 1279.98px), (min-width: 1400px)"
