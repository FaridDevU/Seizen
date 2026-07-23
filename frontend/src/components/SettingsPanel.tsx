import { useEffect, useState } from "react"
import {
  AppWindow,
  Check,
  Folder,
  House,
  Library,
  Palette,
  SlidersHorizontal,
  SquareTerminal,
  X,
} from "lucide-react"

import { ResourcesPanel } from "@/components/ResourcesPanel"
import { cn } from "@/lib/utils"
import type { GreetingLang } from "@/lib/greetings"
import { glowPalettes, hexToHsv, hsvToHex, type GlowMode } from "@/lib/glow"

export type StartView = "home" | "folders"

const accentOptions = [
  { id: "blue", label: "Blue", swatch: "#6379a8" },
  { id: "violet", label: "Violet", swatch: "#8067b7" },
  { id: "emerald", label: "Emerald", swatch: "#43816b" },
  { id: "amber", label: "Amber", swatch: "#a67636" },
] as const

export type ThemeAccent = (typeof accentOptions)[number]["id"]

export function isThemeAccent(value: unknown): value is ThemeAccent {
  return accentOptions.some((option) => option.id === value)
}

export type GlassTint = "dark" | "light"

const sections = [
  { id: "general", label: "General", Icon: SlidersHorizontal },
  { id: "appearance", label: "Appearance", Icon: Palette },
  { id: "panels", label: "Panels", Icon: AppWindow },
  { id: "resources", label: "Resources", Icon: Library },
] as const

type SectionId = (typeof sections)[number]["id"]

const clamp01 = (value: number) => Math.min(Math.max(value, 0), 1)

// Pinterest-style inline picker: hue bar + saturation/value square you drag over.
function HsvPicker({
  value,
  onChange,
}: {
  value: string
  onChange: (hex: string) => void
}) {
  const [[hue, sat, val], setHsv] = useState(() => hexToHsv(value))
  const hex = hsvToHex(hue, sat, val)

  const trackPointer = (
    event: React.PointerEvent<HTMLDivElement>,
    apply: (x: number, y: number) => void,
  ) => {
    if (event.type === "pointerdown") {
      event.currentTarget.setPointerCapture(event.pointerId)
    } else if (!(event.buttons & 1)) {
      return
    }
    const rect = event.currentTarget.getBoundingClientRect()
    apply(
      clamp01((event.clientX - rect.left) / rect.width),
      clamp01((event.clientY - rect.top) / rect.height),
    )
  }

  const pickSquare = (event: React.PointerEvent<HTMLDivElement>) =>
    trackPointer(event, (x, y) => {
      setHsv([hue, x, 1 - y])
      onChange(hsvToHex(hue, x, 1 - y))
    })

  const pickHue = (event: React.PointerEvent<HTMLDivElement>) =>
    trackPointer(event, (x) => {
      const next = Math.min(x * 360, 359.9)
      setHsv([next, sat, val])
      onChange(hsvToHex(next, sat, val))
    })

  return (
    <div className="w-full space-y-3">
      <div
        role="slider"
        aria-label="Saturation and brightness"
        aria-valuetext={hex}
        onPointerDown={pickSquare}
        onPointerMove={pickSquare}
        className="relative h-36 w-full cursor-crosshair touch-none rounded-xl shadow-[inset_0_0_0_1px_var(--outline-variant)]"
        style={{
          background: `linear-gradient(to top, #000, transparent), linear-gradient(to right, #fff, hsl(${hue}, 100%, 50%))`,
        }}
      >
        <span
          className="pointer-events-none absolute size-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white shadow-[0_0_0_1px_rgba(0,0,0,0.35)]"
          style={{
            left: `${sat * 100}%`,
            top: `${(1 - val) * 100}%`,
            background: hex,
          }}
        />
      </div>
      <div className="flex items-center gap-3">
        <div
          role="slider"
          aria-label="Hue"
          aria-valuenow={Math.round(hue)}
          aria-valuemin={0}
          aria-valuemax={360}
          onPointerDown={pickHue}
          onPointerMove={pickHue}
          className="relative h-3.5 flex-1 cursor-pointer touch-none rounded-full"
          style={{
            background:
              "linear-gradient(to right, #f00, #ff0, #0f0, #0ff, #00f, #f0f, #f00)",
          }}
        >
          <span
            className="pointer-events-none absolute top-1/2 size-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white shadow-[0_0_0_1px_rgba(0,0,0,0.35)]"
            style={{ left: `${(hue / 360) * 100}%`, background: `hsl(${hue}, 100%, 50%)` }}
          />
        </div>
        <span className="w-16 text-right font-mono text-xs uppercase text-[var(--on-surface-variant)]">
          {hex}
        </span>
      </div>
    </div>
  )
}

function SectionHeading({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="font-mono text-[0.68rem] font-semibold uppercase tracking-[0.2em] text-[var(--on-surface-variant)]">
      {children}
    </h2>
  )
}

function Row({
  label,
  description,
  Icon,
  children,
}: {
  label: string
  description?: string
  Icon?: React.ComponentType<{ className?: string; strokeWidth?: number }>
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-6 py-3.5">
      <div className="flex min-w-0 items-center gap-3">
        {Icon && (
          <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-[var(--surface-container)] text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)]">
            <Icon className="size-4" strokeWidth={1.7} />
          </span>
        )}
        <div className="min-w-0">
          <div className="text-sm font-medium text-[var(--on-surface)]">{label}</div>
          {description && (
            <p className="mt-0.5 text-xs leading-relaxed text-[var(--on-surface-variant)]">
              {description}
            </p>
          )}
        </div>
      </div>
      <div className="flex shrink-0 items-center">{children}</div>
    </div>
  )
}

function Switch({
  checked,
  onChange,
  label,
}: {
  checked: boolean
  onChange: () => void
  label: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={onChange}
      className={cn(
        "relative h-6 w-10 rounded-full outline-none transition-colors duration-200 focus-visible:ring-2 focus-visible:ring-[var(--ring)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--surface-container-high)]",
        checked
          ? "bg-[var(--primary)]"
          : "bg-[var(--state-layer)] shadow-[inset_0_0_0_1px_var(--outline-variant)]",
      )}
    >
      <span
        className={cn(
          "absolute left-0.5 top-0.5 size-5 rounded-full bg-white shadow-[0_1px_3px_rgba(0,0,0,0.25)] transition-transform duration-200",
          checked && "translate-x-4",
        )}
      />
    </button>
  )
}

function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T
  options: readonly { value: T; label: string }[]
  onChange: (value: T) => void
  label: string
}) {
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="flex rounded-full bg-[var(--surface-container)] p-0.5 shadow-[inset_0_0_0_1px_var(--outline-variant)]"
    >
      {options.map((option) => {
        const active = value === option.value
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(option.value)}
            className={cn(
              "rounded-full px-3.5 py-1.5 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
              active
                ? "bg-[var(--surface-container-high)] text-[var(--on-surface)] shadow-[0_2px_8px_var(--shadow-soft),inset_0_0_0_1px_var(--outline-variant)]"
                : "text-[var(--on-surface-variant)] hover:text-[var(--on-surface)]",
            )}
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
}

function ThemeCard({
  selected,
  onClick,
  label,
  dark,
  accentSwatch,
}: {
  selected: boolean
  onClick: () => void
  label: string
  dark: boolean
  accentSwatch: string
}) {
  const line = dark ? "rgba(255,255,255,0.3)" : "rgba(30,33,36,0.22)"
  const lineSoft = dark ? "rgba(255,255,255,0.14)" : "rgba(30,33,36,0.1)"
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onClick}
      className={cn(
        "flex-1 rounded-2xl border p-2 text-left outline-none transition-[border-color,box-shadow,transform] hover:-translate-y-0.5 focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
        selected
          ? "border-[var(--focus-border)] shadow-[0_8px_24px_var(--shadow-soft)]"
          : "border-[var(--outline-variant)] hover:border-[var(--focus-border)]",
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "block rounded-xl p-3",
          dark
            ? "bg-[#1b1e1d]"
            : "bg-[#f2f1ec] shadow-[inset_0_0_0_1px_rgba(30,33,36,0.08)]",
        )}
      >
        <span className="block h-1.5 w-3/4 rounded-full" style={{ backgroundColor: line }} />
        <span className="mt-1.5 block h-1.5 w-1/2 rounded-full" style={{ backgroundColor: lineSoft }} />
        <span className="mt-2.5 flex items-center gap-1.5">
          <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: accentSwatch }} />
          <span className="h-1.5 flex-1 rounded-full" style={{ backgroundColor: lineSoft }} />
        </span>
      </span>
      <span className="mt-2 flex items-center justify-between px-1 pb-0.5">
        <span className="text-xs font-medium text-[var(--on-surface)]">{label}</span>
        {selected && <Check className="size-3.5 text-[var(--primary)]" strokeWidth={2.2} />}
      </span>
    </button>
  )
}

function SettingsPanel({
  isDark,
  accent,
  startView,
  greetingLang,
  glowMode,
  glowPalette,
  glowColors,
  glassTerminal,
  glassTint,
  glassLevel,
  onModeChange,
  onAccentChange,
  onStartViewChange,
  onGreetingLangChange,
  onGlowModeChange,
  onGlowPaletteChange,
  onGlowColorsChange,
  wobbly,
  onGlassTerminalChange,
  onGlassTintChange,
  onGlassLevelChange,
  onWobblyChange,
  onClose,
}: {
  isDark: boolean
  accent: ThemeAccent
  startView: StartView
  greetingLang: GreetingLang
  glowMode: GlowMode
  glowPalette: number
  glowColors: string[]
  glassTerminal: boolean
  glassTint: GlassTint
  glassLevel: number
  onModeChange: (dark: boolean) => void
  onAccentChange: (accent: ThemeAccent) => void
  onStartViewChange: (view: StartView) => void
  onGreetingLangChange: (lang: GreetingLang) => void
  onGlowModeChange: (mode: GlowMode) => void
  onGlowPaletteChange: (index: number) => void
  onGlowColorsChange: (colors: string[]) => void
  wobbly: boolean
  onGlassTerminalChange: (on: boolean) => void
  onGlassTintChange: (tint: GlassTint) => void
  onGlassLevelChange: (level: number) => void
  onWobblyChange: (wobbly: boolean) => void
  onClose: () => void
}) {
  const [section, setSection] = useState<SectionId>("general")
  const [editingColor, setEditingColor] = useState(0)
  // Removing swatches can leave the index past the end; clamp instead of tracking.
  const activeColor = Math.min(editingColor, glowColors.length - 1)
  const accentSwatch =
    accentOptions.find((option) => option.id === accent)?.swatch ?? accentOptions[0].swatch

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose])

  return (
    <div
      role="presentation"
      onClick={onClose}
      className="overlay-in fixed inset-0 z-[200] flex items-center justify-center bg-black/45 p-4 backdrop-blur-sm"
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        onClick={(event) => event.stopPropagation()}
        className="flex h-[min(42rem,88vh)] w-full max-w-4xl flex-col overflow-hidden rounded-[1.5rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_24px_64px_var(--shadow-elevated)]"
      >
        <header className="flex shrink-0 items-center justify-between border-b border-[var(--outline-variant)] px-6 py-4">
          <h1 className="text-base font-semibold tracking-[-0.02em]">Settings</h1>
          <button
            type="button"
            aria-label="Close settings"
            onClick={onClose}
            className="flex size-8 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
          >
            <X className="size-4" strokeWidth={1.8} />
          </button>
        </header>

        <div className="flex min-h-0 flex-1">
          <nav
            aria-label="Settings sections"
            className="w-44 shrink-0 space-y-0.5 overflow-y-auto border-r border-[var(--outline-variant)] p-3 sm:w-48"
          >
            {sections.map(({ id, label, Icon }) => {
              const active = section === id
              return (
                <button
                  key={id}
                  type="button"
                  aria-current={active ? "page" : undefined}
                  onClick={() => setSection(id)}
                  className={cn(
                    "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                    active
                      ? "bg-[var(--primary-container)] font-medium text-[var(--on-primary-container)]"
                      : "text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]",
                  )}
                >
                  <Icon className="size-4 shrink-0" strokeWidth={1.7} />
                  {label}
                </button>
              )
            })}
          </nav>

          <main className="min-w-0 flex-1 overflow-y-auto px-7 py-6">
            {section === "general" && (
              <section>
                <SectionHeading>General</SectionHeading>
                <div className="mt-2 divide-y divide-[var(--outline-variant)]">
                  <Row
                    label="Start view"
                    description="Screen shown when Seizen opens"
                    Icon={startView === "home" ? House : Folder}
                  >
                    <Segmented
                      label="Start view"
                      value={startView}
                      options={[
                        { value: "home", label: "Home" },
                        { value: "folders", label: "Library" },
                      ]}
                      onChange={onStartViewChange}
                    />
                  </Row>
                  <Row
                    label="Greeting language"
                    description="Language of the rotating phrases on Home"
                  >
                    <Segmented
                      label="Greeting language"
                      value={greetingLang}
                      options={[
                        { value: "es", label: "Español" },
                        { value: "en", label: "English" },
                      ]}
                      onChange={onGreetingLangChange}
                    />
                  </Row>
                </div>
              </section>
            )}

            {section === "appearance" && (
              <section>
                <SectionHeading>Appearance</SectionHeading>

                <div className="mt-4">
                  <div className="text-sm font-medium">Theme</div>
                  <p className="mt-0.5 text-xs text-[var(--on-surface-variant)]">
                    How the whole app is lit
                  </p>
                  <div className="mt-3 flex max-w-sm gap-3">
                    <ThemeCard
                      selected={!isDark}
                      onClick={() => onModeChange(false)}
                      label="Light"
                      dark={false}
                      accentSwatch={accentSwatch}
                    />
                    <ThemeCard
                      selected={isDark}
                      onClick={() => onModeChange(true)}
                      label="Dark"
                      dark
                      accentSwatch={accentSwatch}
                    />
                  </div>
                </div>

                <div className="mt-5 divide-y divide-[var(--outline-variant)] border-t border-[var(--outline-variant)]">
                  <Row
                    label="Chat glow"
                    description="How the halo around the chat picks its colors"
                  >
                    <Segmented
                      label="Chat glow"
                      value={glowMode}
                      options={[
                        { value: "time", label: "By hour" },
                        { value: "random", label: "Random" },
                        { value: "fixed", label: "Single" },
                        { value: "custom", label: "Custom" },
                      ]}
                      onChange={onGlowModeChange}
                    />
                  </Row>
                  {glowMode === "fixed" && (
                    <Row label="Palette" description="The one glow to rule them all">
                      <div className="flex items-center gap-2.5">
                        {glowPalettes.map((palette, index) => (
                          <button
                            key={index}
                            type="button"
                            aria-label={`Palette ${index + 1}`}
                            aria-pressed={glowPalette === index}
                            onClick={() => onGlowPaletteChange(index)}
                            className={cn(
                              "size-7 rounded-full outline-none transition-transform hover:scale-110 focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                              glowPalette === index &&
                                "ring-2 ring-[var(--focus-border)] ring-offset-2 ring-offset-[var(--surface-container-high)]",
                            )}
                            style={{ background: `conic-gradient(${palette.join(",")})` }}
                          />
                        ))}
                      </div>
                    </Row>
                  )}
                  {glowMode === "custom" && (
                    <div>
                      <Row label="Custom colors" description="Mix up to six of your own">
                        <div className="flex items-center gap-2">
                          {glowColors.map((color, index) => (
                            <span key={index} className="group relative">
                              <button
                                type="button"
                                aria-label={`Edit color ${index + 1}`}
                                aria-pressed={activeColor === index}
                                onClick={() => setEditingColor(index)}
                                className={cn(
                                  "size-7 rounded-full outline-none transition-transform hover:scale-110 focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                                  activeColor === index &&
                                    "ring-2 ring-[var(--focus-border)] ring-offset-2 ring-offset-[var(--surface-container-high)]",
                                )}
                                style={{ background: color }}
                              />
                              {glowColors.length > 1 && (
                                <button
                                  type="button"
                                  aria-label={`Remove color ${index + 1}`}
                                  onClick={() =>
                                    onGlowColorsChange(
                                      glowColors.filter((_, i) => i !== index),
                                    )
                                  }
                                  className="absolute -right-1 -top-1 hidden size-3.5 items-center justify-center rounded-full bg-[var(--surface-container-high)] text-[9px] leading-none text-[var(--on-surface-variant)] shadow-[0_1px_3px_var(--shadow-soft),inset_0_0_0_1px_var(--outline-variant)] group-hover:flex"
                                >
                                  ×
                                </button>
                              )}
                            </span>
                          ))}
                          {glowColors.length < 6 && (
                            <button
                              type="button"
                              aria-label="Add color"
                              onClick={() => {
                                onGlowColorsChange([...glowColors, "#818cf8"])
                                setEditingColor(glowColors.length)
                              }}
                              className="flex size-7 items-center justify-center rounded-full text-sm text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                            >
                              +
                            </button>
                          )}
                        </div>
                      </Row>
                      <div className="pb-4">
                        <HsvPicker
                          key={`${activeColor}-${glowColors.length}`}
                          value={glowColors[activeColor]}
                          onChange={(hex) =>
                            onGlowColorsChange(
                              glowColors.map((current, i) =>
                                i === activeColor ? hex : current,
                              ),
                            )
                          }
                        />
                      </div>
                    </div>
                  )}
                  <Row
                    label="Accent color"
                    description="Tints highlights, selection, and focus"
                  >
                    <div className="flex items-center gap-2.5">
                      {accentOptions.map((option) => {
                        const selected = accent === option.id
                        return (
                          <button
                            key={option.id}
                            type="button"
                            aria-label={option.label}
                            aria-pressed={selected}
                            onClick={() => onAccentChange(option.id)}
                            className={cn(
                              "flex size-7 items-center justify-center rounded-full outline-none transition-transform hover:scale-110 focus-visible:ring-2 focus-visible:ring-[var(--ring)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--surface-container-high)]",
                              selected &&
                                "ring-2 ring-[var(--focus-border)] ring-offset-2 ring-offset-[var(--surface-container-high)]",
                            )}
                            style={{ backgroundColor: option.swatch }}
                          >
                            {selected && (
                              <Check className="size-3.5 text-white" strokeWidth={2.4} />
                            )}
                          </button>
                        )
                      })}
                    </div>
                  </Row>
                </div>
              </section>
            )}

            {section === "panels" && (
              <div className="space-y-8">
                <section>
                  <SectionHeading>Effects</SectionHeading>
                  <div className="mt-2 divide-y divide-[var(--outline-variant)]">
                    <Row
                      label="Wobbly windows"
                      description="Panels jiggle like jelly while you drag them"
                    >
                      <Switch
                        checked={wobbly}
                        onChange={() => onWobblyChange(!wobbly)}
                        label="Wobbly windows"
                      />
                    </Row>
                  </div>
                </section>

                <section>
                  <SectionHeading>Terminal glass</SectionHeading>
                  <div className="mt-2 divide-y divide-[var(--outline-variant)]">
                    <Row
                      label="Translucent terminals"
                      description="See the canvas through the shell"
                      Icon={SquareTerminal}
                    >
                      <Switch
                        checked={glassTerminal}
                        onChange={() => onGlassTerminalChange(!glassTerminal)}
                        label="Translucent terminals"
                      />
                    </Row>
                    <Row label="Glass tint" description="Color of the glass itself">
                      <Segmented
                        label="Glass tint"
                        value={glassTint}
                        options={[
                          { value: "dark", label: "Black" },
                          { value: "light", label: "White" },
                        ]}
                        onChange={onGlassTintChange}
                      />
                    </Row>
                    <Row
                      label="Transparency"
                      description="How much background shows through"
                    >
                      <div className="flex items-center gap-3">
                        <input
                          type="range"
                          min={0}
                          max={100}
                          step={5}
                          value={glassLevel}
                          aria-label="Transparency level"
                          onChange={(event) => onGlassLevelChange(Number(event.target.value))}
                          className="w-36 accent-[var(--primary)]"
                        />
                        <span className="w-9 text-right font-mono text-xs tabular-nums text-[var(--on-surface-variant)]">
                          {glassLevel}%
                        </span>
                      </div>
                    </Row>
                  </div>
                </section>
              </div>
            )}

            {section === "resources" && (
              <section>
                <SectionHeading>Resources</SectionHeading>
                <div className="mt-3">
                  <ResourcesPanel />
                </div>
              </section>
            )}
          </main>
        </div>
      </div>
    </div>
  )
}

export { SettingsPanel }
