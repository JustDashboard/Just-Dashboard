"use client"

import { memo, useId } from "react"
import { motion, useReducedMotion } from "motion/react"
import type { BoardPreview, DrawingElement } from "@/lib/boards"
import {
  arrowhead,
  extentOf,
  faceOf,
  frameOf,
  isLinear,
  linePath,
  shapePath,
} from "@/lib/drawing-preview"

/**
 * The editor runs Excalidraw in its dark theme, which draws the scene in its
 * light palette and inverts the canvas. The thumbnail does the same, so a
 * drawing on the list is the drawing the operator left, in the colours they
 * saw — not a white page lit up in a dark product.
 */
const DARK = "invert(93%) hue-rotate(180deg)"

const FACE = {
  hand: "var(--font-drawing)",
  sans: "var(--font-sans)",
  mono: "var(--font-mono)",
} as const

/** Above this many shapes a drawing simply appears: sketching in four hundred is a stutter. */
const SKETCH_LIMIT = 120

/**
 * A drawing drawn small, from the shapes the server keeps for it. Shapes are
 * clean geometry in the scene's own colours, and words are in Excalidraw's
 * hand. On arrival each stroke sketches itself in once, as a pen would —
 * the *arrived* of §11, the way the Git graph's lanes draw down — and the
 * fill follows it; reduced motion, or a drawing too large for it, is simply
 * drawn.
 */
export const DrawingPreview = memo(function DrawingPreview({
  preview,
  className,
  sketch = true,
}: {
  preview?: BoardPreview
  className?: string
  sketch?: boolean
}) {
  const uid = useId().replace(/:/g, "")
  const reduced = useReducedMotion()
  const elements = preview?.elements ?? []
  const frame = frameOf(elements)
  const background = preview?.background || "#ffffff"

  if (!frame) {
    return (
      <div aria-hidden className={className}>
        <div
          style={{ background, filter: DARK, fontFamily: FACE.hand, color: "#868e96" }}
          className="flex size-full items-center justify-center text-title"
        >
          {preview && "Empty canvas"}
        </div>
      </div>
    )
  }

  // One screen pixel in scene units, near enough: the card is about four
  // hundred pixels across and the drawing is fitted to it.
  const px = Math.max(frame.w, frame.h) / 420
  const animate = sketch && !reduced && elements.length <= SKETCH_LIMIT
  const hatches = [
    ...new Set(elements.filter((el) => el.fill && el.fillStyle).map((el) => hatchKey(el))),
  ]

  return (
    // The inversion is on the drawing alone: on the element that carries the
    // caller's border it turned a hairline into a bright rule.
    <div aria-hidden className={className}>
      <svg
        viewBox={`${frame.x} ${frame.y} ${frame.w} ${frame.h}`}
        preserveAspectRatio="xMidYMid meet"
        style={{ background, filter: DARK }}
        className="block size-full"
      >
        <defs>
          {hatches.map((key) => {
            const [style, colour] = key.split("|")
            const gap = px * 5
            return (
              <pattern
                key={key}
                id={`${uid}-${hatches.indexOf(key)}`}
                patternUnits="userSpaceOnUse"
                width={gap}
                height={gap}
                patternTransform="rotate(-41)"
              >
                <line x1={0} y1={0} x2={0} y2={gap} stroke={colour} strokeWidth={px * 1.2} />
                {style === "cross-hatch" && (
                  <line x1={0} y1={0} x2={gap} y2={0} stroke={colour} strokeWidth={px * 1.2} />
                )}
              </pattern>
            )
          })}
        </defs>
        {elements.map((el, i) => (
          <Element
            key={i}
            el={el}
            px={px}
            delay={animate ? Math.min(i * 0.02, 0.6) : null}
            fill={
              el.fill
                ? el.fillStyle
                  ? `url(#${uid}-${hatches.indexOf(hatchKey(el))})`
                  : el.fill
                : "none"
            }
          />
        ))}
      </svg>
    </div>
  )
})

function hatchKey(el: DrawingElement) {
  return `${el.fillStyle}|${el.fill}`
}

function Element({
  el,
  px,
  delay,
  fill,
}: {
  el: DrawingElement
  px: number
  /** When it sketches in, or null when it is simply drawn. */
  delay: number | null
  fill: string
}) {
  const box = extentOf(el)
  const transform = rotation(el) || undefined
  const opacity = el.opacity === undefined ? undefined : el.opacity / 100
  const stroke = el.stroke ?? "#1e1e1e"
  const width = px * (0.8 + (el.strokeWidth ?? 2) * 0.55)
  const dash =
    el.strokeStyle === "dashed"
      ? `${width * 4} ${width * 3}`
      : el.strokeStyle === "dotted"
        ? `${width * 0.2} ${width * 2.4}`
        : undefined

  if (el.type === "text") {
    return <Words el={el} transform={transform} opacity={opacity} colour={stroke} delay={delay} />
  }

  if (el.type === "image") {
    const s = Math.min(box.w, box.h) * 0.36
    const cx = box.x + box.w / 2
    const cy = box.y + box.h / 2
    return (
      <g transform={transform} opacity={opacity}>
        <rect x={box.x} y={box.y} width={box.w} height={box.h} fill="#e9ecef" />
        <circle cx={cx + s * 0.28} cy={cy - s * 0.26} r={s * 0.13} fill="#ced4da" />
        <path
          d={`M${cx - s / 2} ${cy + s / 3}L${cx - s / 6} ${cy - s / 8}L${cx + s / 12} ${cy + s / 8}L${cx + s / 4} ${cy}L${cx + s / 2} ${cy + s / 3}Z`}
          fill="#ced4da"
        />
      </g>
    )
  }

  if (el.type === "frame" || el.type === "magicframe") {
    return (
      <g transform={transform}>
        <rect
          x={box.x}
          y={box.y}
          width={box.w}
          height={box.h}
          rx={px * 4}
          fill="none"
          stroke="#bbb"
          strokeWidth={px}
        />
        {el.text && (
          <text
            x={box.x}
            y={box.y - px * 6}
            fontSize={px * 11}
            fill="#999"
            style={{ fontFamily: FACE.sans }}
          >
            {el.text}
          </text>
        )}
      </g>
    )
  }

  const linear = isLinear(el)
  const ink = {
    stroke,
    strokeWidth: el.type === "freedraw" ? width * 1.3 : width,
    strokeDasharray: dash,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
  }
  const d = linear ? linePath(el.points!, el.type !== "freedraw" && !!el.round) : shapePath(el)
  const heads = linear && el.type === "arrow" ? arrowheads(el) : ""

  return (
    <g
      transform={[linear && `translate(${el.x} ${el.y})`, rotation(el, linear)]
        .filter(Boolean)
        .join(" ")}
      opacity={opacity}
    >
      {fill !== "none" && !linear && <Ink d={d} delay={delay} fillOnly fill={fill} />}
      <Ink d={d} delay={dash ? null : delay} fill="none" {...ink} />
      {heads && <Ink d={heads} delay={delay === null ? null : delay + 0.35} fill="none" {...ink} />}
    </g>
  )
}

/**
 * Excalidraw turns an element about its own centre. A linear element is drawn
 * translated to its first point, so its centre is measured from there.
 */
function rotation(el: DrawingElement, linear = false) {
  if (!el.angle) return ""
  const box = extentOf(el)
  const ox = linear ? el.x : 0
  const oy = linear ? el.y : 0
  return `rotate(${(el.angle * 180) / Math.PI} ${box.x + box.w / 2 - ox} ${box.y + box.h / 2 - oy})`
}

function arrowheads(el: DrawingElement) {
  const points = el.points!
  const n = points.length
  return [
    el.endArrow ? arrowhead(points[n - 1], points[n - 2], el.strokeWidth) : "",
    el.startArrow ? arrowhead(points[0], points[1], el.strokeWidth) : "",
  ].join("")
}

function Ink({
  d,
  delay,
  fillOnly,
  ...props
}: React.SVGProps<SVGPathElement> & { d: string; delay: number | null; fillOnly?: boolean }) {
  if (delay === null) return <path d={d} {...(props as React.SVGProps<SVGPathElement>)} />
  if (fillOnly) {
    return (
      <motion.path
        d={d}
        fill={props.fill}
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ delay: delay + 0.45, duration: 0.4 }}
      />
    )
  }
  return (
    <motion.path
      d={d}
      fill={props.fill}
      stroke={props.stroke}
      strokeWidth={props.strokeWidth}
      strokeLinecap={props.strokeLinecap}
      strokeLinejoin={props.strokeLinejoin}
      initial={{ pathLength: 0, opacity: 0 }}
      animate={{ pathLength: 1, opacity: 1 }}
      transition={{
        delay,
        pathLength: { delay, duration: 0.7, ease: [0.4, 0, 0.2, 1] },
        opacity: { delay, duration: 0.05 },
      }}
    />
  )
}

function Words({
  el,
  transform,
  opacity,
  colour,
  delay,
}: {
  el: DrawingElement
  transform?: string
  opacity?: number
  colour: string
  delay: number | null
}) {
  const size = el.fontSize ?? 20
  const lineHeight = size * 1.25
  const anchor = el.textAlign === "center" ? "middle" : el.textAlign === "right" ? "end" : "start"
  const x =
    el.textAlign === "center" ? el.x + el.w / 2 : el.textAlign === "right" ? el.x + el.w : el.x
  const lines = (el.text ?? "").split("\n")
  const words = (
    <text
      fontSize={size}
      fill={colour}
      textAnchor={anchor}
      dominantBaseline="central"
      style={{ fontFamily: FACE[faceOf(el.fontFamily)] }}
    >
      {lines.map((line, i) => (
        <tspan key={i} x={x} y={el.y + lineHeight * (i + 0.5)}>
          {line}
        </tspan>
      ))}
    </text>
  )
  if (delay === null) {
    return (
      <g transform={transform} opacity={opacity}>
        {words}
      </g>
    )
  }
  return (
    <motion.g
      transform={transform}
      initial={{ opacity: 0 }}
      animate={{ opacity: opacity ?? 1 }}
      transition={{ delay: delay + 0.3, duration: 0.45 }}
    >
      {words}
    </motion.g>
  )
}
