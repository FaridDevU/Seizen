import { useEffect, useState } from "react"
import {
  AppWindow,
  Check,
  Code,
  Folder,
  Globe,
  House,
  Moon,
  Palette,
  SlidersHorizontal,
  StickyNote,
  Sun,
  SquareTerminal,
  Waves,
  X,
} from "lucide-react"

import { cn } from "@/lib/utils"

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

const glassOptions = [
  { id: "terminal", label: "Terminals", description: "See the canvas through the shell", Icon: SquareTerminal },
  { id: "editor", label: "Code editors", description: "Translucent editor panels", Icon: Code },
  { id: "browser", label: "Browser", description: "Translucent browser panels", Icon: Globe },
  { id: "notes", label: "Notes & documents", description: "Notes, to-dos, and documents", Icon: StickyNote },
] as const

export type GlassPanel = (typeof glassOptions)[number]["id"]

export function isGlassPanel(value: unknown): value is GlassPanel {
  return glassOptions.some((option) => option.id === value)
}

export type GlassTint = "dark" | "light"

const sections = [
  { id: "general", label: "General", Icon: SlidersHorizontal },
  { id: "appearance", label: "Appearance", Icon: Palette },
  { id: "panels", label: "Panels", Icon: AppWindow },
] as const

type SectionId = (typeof sections)[number]["id"]

function OptionCard({
  selected,
  onClick,
  Icon,
  label,
  description,
}: {
  selected: boolean
  onClick: () => void
  Icon: React.ComponentType<{ className?: string; strokeWidth?: number }>
  label: string
  description: string
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onClick}
      className={cn(
        "flex items-center gap-4 rounded-2xl border p-4 text-left outline-none transition-[border-color,background-color,box-shadow,transform] hover:-translate-y-0.5 focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
        selected
          ? "border-[var(--focus-border)] bg-[var(--primary-container)] shadow-[0_8px_24px_var(--shadow-soft)]"
          : "border-[var(--outline-variant)] bg-[var(--surface-container)] hover:bg-[var(--state-layer)]",
      )}
    >
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-[var(--surface-container-high)] text-[var(--primary)] shadow-[0_2px_8px_var(--shadow-soft)]">
        <Icon className="size-[1.1rem]" strokeWidth={1.75} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-semibold">{label}</span>
        <span className="mt-0.5 block text-xs text-[var(--on-surface-variant)]">
          {description}
        </span>
      </span>
      {selected && <Check className="size-4 shrink-0 text-[var(--primary)]" />}
    </button>
  )
}

function SettingsPanel({
  isDark,
  accent,
  startView,
  glassPanels,
  glassTint,
  glassLevel,
  onModeChange,
  onAccentChange,
  onStartViewChange,
  wobbly,
  onGlassToggle,
  onGlassTintChange,
  onGlassLevelChange,
  onWobblyChange,
  onClose,
}: {
  isDark: boolean
  accent: ThemeAccent
  startView: StartView
  glassPanels: GlassPanel[]
  glassTint: GlassTint
  glassLevel: number
  onModeChange: (dark: boolean) => void
  onAccentChange: (accent: ThemeAccent) => void
  onStartViewChange: (view: StartView) => void
  wobbly: boolean
  onGlassToggle: (panel: GlassPanel) => void
  onGlassTintChange: (tint: GlassTint) => void
  onGlassLevelChange: (level: number) => void
  onWobblyChange: (wobbly: boolean) => void
  onClose: () => void
}) {
  const [section, setSection] = useState<SectionId>("general")

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
            className="w-48 shrink-0 space-y-1 overflow-y-auto border-r border-[var(--outline-variant)] p-3 sm:w-52"
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
                    "flex w-full items-center gap-2.5 rounded-xl px-3 py-2 text-left text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                    active
                      ? "bg-[var(--primary-container)] font-semibold text-[var(--on-primary-container)]"
                      : "text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]",
                  )}
                >
                  <Icon className="size-4 shrink-0" strokeWidth={1.7} />
                  {label}
                </button>
              )
            })}
          </nav>

          <main className="min-w-0 flex-1 overflow-y-auto p-6">
            {section === "general" && (
              <fieldset>
                <legend className="text-xs font-semibold tracking-[0.025em] text-[var(--on-surface-variant)]">
                  Start view
                </legend>
                <div className="mt-3 grid gap-3 lg:grid-cols-2">
                  {[
                    { view: "home" as const, label: "Home", description: "Search, actions, and recents", Icon: House },
                    { view: "folders" as const, label: "Library", description: "Your spaces and projects", Icon: Folder },
                  ].map(({ view, label, description, Icon }) => (
                    <OptionCard
                      key={view}
                      selected={startView === view}
                      onClick={() => onStartViewChange(view)}
                      Icon={Icon}
                      label={label}
                      description={description}
                    />
                  ))}
                </div>
              </fieldset>
            )}

            {section === "appearance" && (
              <div className="space-y-8">
                <fieldset>
                  <legend className="text-xs font-semibold tracking-[0.025em] text-[var(--on-surface-variant)]">
                    Mode
                  </legend>
                  <div className="mt-3 grid gap-3 lg:grid-cols-2">
                    {[
                      { dark: false, label: "Light", description: "Bright and clean", Icon: Sun },
                      { dark: true, label: "Dark", description: "Easy on the eyes", Icon: Moon },
                    ].map(({ dark, label, description, Icon }) => (
                      <OptionCard
                        key={label}
                        selected={isDark === dark}
                        onClick={() => onModeChange(dark)}
                        Icon={Icon}
                        label={label}
                        description={description}
                      />
                    ))}
                  </div>
                </fieldset>

                <fieldset>
                  <legend className="text-xs font-semibold tracking-[0.025em] text-[var(--on-surface-variant)]">
                    Theme color
                  </legend>
                  <div className="mt-3 grid grid-cols-2 gap-3 lg:grid-cols-4">
                    {accentOptions.map((option) => {
                      const selected = accent === option.id
                      return (
                        <button
                          key={option.id}
                          type="button"
                          aria-pressed={selected}
                          onClick={() => onAccentChange(option.id)}
                          className={cn(
                            "flex items-center gap-3 rounded-2xl border px-3 py-3 text-left text-xs font-medium outline-none transition-[border-color,background-color,box-shadow,transform] hover:-translate-y-0.5 focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                            selected
                              ? "border-[var(--focus-border)] bg-[var(--primary-container)] text-[var(--on-primary-container)] shadow-[0_6px_18px_var(--shadow-soft)]"
                              : "border-[var(--outline-variant)] bg-[var(--surface-container)] hover:bg-[var(--state-layer)]",
                          )}
                        >
                          <span
                            aria-hidden="true"
                            className="flex size-8 shrink-0 items-center justify-center rounded-full shadow-[inset_0_0_0_1px_rgba(255,255,255,0.28),0_3px_10px_var(--shadow-soft)]"
                            style={{ backgroundColor: option.swatch }}
                          >
                            {selected && <Check className="size-3.5 text-white" strokeWidth={2.2} />}
                          </span>
                          {option.label}
                        </button>
                      )
                    })}
                  </div>
                </fieldset>
              </div>
            )}

            {section === "panels" && (
              <div className="space-y-8">
              <fieldset>
                <legend className="text-xs font-semibold tracking-[0.025em] text-[var(--on-surface-variant)]">
                  Effects
                </legend>
                <div className="mt-3">
                  <OptionCard
                    selected={wobbly}
                    onClick={() => onWobblyChange(!wobbly)}
                    Icon={Waves}
                    label="Wobbly windows"
                    description="Panels jiggle like jelly while you drag them"
                  />
                </div>
              </fieldset>
              <fieldset>
                <legend className="text-xs font-semibold tracking-[0.025em] text-[var(--on-surface-variant)]">
                  Translucent panels
                </legend>
                <p className="mt-1 text-xs text-[var(--on-surface-variant)]">
                  Chosen workspace panels let the background show through.
                </p>
                <div className="mt-3 grid gap-3 lg:grid-cols-2">
                  {glassOptions.map(({ id, label, description, Icon }) => (
                    <OptionCard
                      key={id}
                      selected={glassPanels.includes(id)}
                      onClick={() => onGlassToggle(id)}
                      Icon={Icon}
                      label={label}
                      description={description}
                    />
                  ))}
                </div>
                <div className="mt-4 grid gap-3 lg:grid-cols-2">
                  {(
                    [
                      { tint: "dark", label: "Black glass", swatch: "rgba(0,0,0,0.55)" },
                      { tint: "light", label: "White glass", swatch: "rgba(255,255,255,0.75)" },
                    ] as const
                  ).map(({ tint, label, swatch }) => {
                    const selected = glassTint === tint
                    return (
                      <button
                        key={tint}
                        type="button"
                        aria-pressed={selected}
                        onClick={() => onGlassTintChange(tint)}
                        className={cn(
                          "flex items-center gap-3 rounded-2xl border px-3 py-3 text-left text-xs font-medium outline-none transition-[border-color,background-color,box-shadow,transform] hover:-translate-y-0.5 focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                          selected
                            ? "border-[var(--focus-border)] bg-[var(--primary-container)] text-[var(--on-primary-container)] shadow-[0_6px_18px_var(--shadow-soft)]"
                            : "border-[var(--outline-variant)] bg-[var(--surface-container)] hover:bg-[var(--state-layer)]",
                        )}
                      >
                        <span
                          aria-hidden="true"
                          className="flex size-8 shrink-0 items-center justify-center rounded-full border border-[var(--outline-variant)] shadow-[0_3px_10px_var(--shadow-soft)]"
                          style={{ backgroundColor: swatch }}
                        >
                          {selected && (
                            <Check
                              className={cn("size-3.5", tint === "dark" ? "text-white" : "text-black")}
                              strokeWidth={2.2}
                            />
                          )}
                        </span>
                        {label}
                      </button>
                    )
                  })}
                </div>
                <label className="mt-4 block">
                  <span className="flex items-center justify-between text-xs font-medium text-[var(--on-surface-variant)]">
                    Transparency level
                    <span className="tabular-nums">{glassLevel}%</span>
                  </span>
                  <input
                    type="range"
                    min={0}
                    max={100}
                    step={5}
                    value={glassLevel}
                    onChange={(event) => onGlassLevelChange(Number(event.target.value))}
                    className="mt-2 w-full accent-[var(--primary)]"
                  />
                </label>
              </fieldset>
              </div>
            )}
          </main>
        </div>
      </div>
    </div>
  )
}

export { SettingsPanel }
