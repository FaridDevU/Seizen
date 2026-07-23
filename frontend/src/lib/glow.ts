// Palettes for the glow around the Home chat bar. Single source of truth:
// the CSS only reads the --ai-c* variables the app sets from here.

export const glowPalettes: readonly (readonly string[])[] = [
  ["#7c3aed", "#4f46e5", "#38bdf8", "#a855f7", "#6366f1"],
  ["#ff9a62", "#ff4d8d", "#ffd166", "#ff7eb3", "#fb923c"],
  ["#38bdf8", "#5eead4", "#4ade80", "#60a5fa", "#2dd4bf"],
  ["#fbbf24", "#fb7185", "#f97316", "#f472b6", "#facc15"],
  ["#ff4d8d", "#b45cf6", "#ff9a62", "#f43f5e", "#c084fc"],
  ["#b45cf6", "#38bdf8", "#5eead4", "#818cf8", "#a78bfa"],
]

// hex <-> HSV for the inline color picker. h in [0,360), s and v in [0,1].
export function hexToHsv(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16)
  if (Number.isNaN(n)) return [0, 0, 1]
  const r = ((n >> 16) & 255) / 255
  const g = ((n >> 8) & 255) / 255
  const b = (n & 255) / 255
  const max = Math.max(r, g, b)
  const d = max - Math.min(r, g, b)
  let h = 0
  if (d) {
    if (max === r) h = ((g - b) / d) % 6
    else if (max === g) h = (b - r) / d + 2
    else h = (r - g) / d + 4
    h = (h * 60 + 360) % 360
  }
  return [h, max ? d / max : 0, max]
}

export function hsvToHex(h: number, s: number, v: number): string {
  const channel = (n: number) => {
    const k = (n + h / 60) % 6
    const c = v - v * s * Math.max(0, Math.min(k, 4 - k, 1))
    return Math.round(c * 255)
      .toString(16)
      .padStart(2, "0")
  }
  return `#${channel(5)}${channel(3)}${channel(1)}`
}

export type GlowMode = "time" | "random" | "fixed" | "custom"

export function isGlowMode(value: unknown): value is GlowMode {
  return (
    value === "time" ||
    value === "random" ||
    value === "fixed" ||
    value === "custom"
  )
}
