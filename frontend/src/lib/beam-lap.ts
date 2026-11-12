/**
 * One lap of a frame's edge, as the keyframes of a transform animation.
 *
 * `BorderBeam` carried its light round a frame on `offset-path`, which no
 * browser animates off the main thread: every frame of the lap was a style
 * pass and a paint, from JavaScript before that. A lap is straight runs and
 * quarter turns, though, and a transform can say both, so the same path is
 * written here as places and headings the compositor moves between on its
 * own. The path is the one `offset-path: rect(0 auto auto 0 round <size>)`
 * drew: the frame's border box with its corners cut at `size`, or at half the
 * shorter side where that is less, walked clockwise from the end of the
 * top-left corner at one speed.
 *
 * A place is where the light's centre is, so the transform moves a square of
 * `size` whose own corner sits on the frame's: its centre to the place, then
 * turned to face along the path. A corner is `turns` short chords with the
 * heading stepping round with them; four keep the centre within a pixel of
 * the arc at the sizes a frame has. Offsets are distance over the whole lap,
 * which is what makes the speed one speed on a row forty pixels tall and
 * seven hundred wide.
 */
export type LapFrame = { offset: number; transform: string }

export function beamLap(width: number, height: number, size: number, turns = 4): LapFrame[] {
  if (!(width > 0) || !(height > 0) || !(size > 0)) return []
  const radius = Math.min(size, width / 2, height / 2)
  const runs = [width - 2 * radius, height - 2 * radius]
  const arc = (Math.PI / 2) * radius
  const lap = 2 * runs[0] + 2 * runs[1] + 4 * arc
  // The centres the four corners turn about, in the order they are met.
  const corners = [
    [width - radius, radius],
    [width - radius, height - radius],
    [radius, height - radius],
    [radius, radius],
  ]
  const frames: LapFrame[] = []
  let gone = 0
  const at = (x: number, y: number, heading: number) =>
    frames.push({
      offset: Math.min(1, Number((gone / lap).toFixed(5))),
      transform: `translate(${(x - size / 2).toFixed(2)}px, ${(y - size / 2).toFixed(2)}px) rotate(${Number(heading.toFixed(2))}deg)`,
    })

  let x = radius
  let y = 0
  at(x, y, 0)
  for (let side = 0; side < 4; side++) {
    const heading = side * 90
    const run = runs[side % 2]
    if (run > 0) {
      x += Math.round(Math.cos((heading * Math.PI) / 180)) * run
      y += Math.round(Math.sin((heading * Math.PI) / 180)) * run
      gone += run
      at(x, y, heading)
    }
    const [cx, cy] = corners[side]
    for (let step = 1; step <= turns; step++) {
      const turned = (step / turns) * 90
      const angle = ((heading - 90 + turned) * Math.PI) / 180
      gone += arc / turns
      x = cx + radius * Math.cos(angle)
      y = cy + radius * Math.sin(angle)
      at(x, y, heading + turned)
    }
  }
  // The last frame is the first again, a whole turn on; say so exactly, so a
  // rounding of the distance cannot leave the lap a hair short of closed.
  frames[frames.length - 1].offset = 1
  return frames
}
