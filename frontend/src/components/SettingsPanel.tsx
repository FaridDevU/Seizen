import { useEffect, useState } from "react"
import {
  AppWindow,
  Bot,
  Check,
  Copy,
  ExternalLink,
  Folder,
  House,
  Library,
  Palette,
  RefreshCw,
  SlidersHorizontal,
  SquareTerminal,
  X,
} from "lucide-react"

import {
  AddAssistantKey,
  CancelAssistantLogin,
  GetAssistantSettings,
  ListAssistantModels,
  RemoveAssistantKey,
  SelectAssistantKey,
  SetAssistantModel,
  SetAssistantProvider,
  StartAssistantLogin,
  SubmitAssistantLoginCode,
} from "../../wailsjs/go/core/App"
import { BrowserOpenURL, EventsOn } from "../../wailsjs/runtime/runtime"
import type { core } from "../../wailsjs/go/models"
import { ResourcesPanel } from "@/components/ResourcesPanel"
import { Select } from "@/components/ui/select"
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
  { id: "agent", label: "Agent APIs", Icon: Bot },
  { id: "resources", label: "Resources", Icon: Library },
] as const

type SectionId = (typeof sections)[number]["id"]

// Mirrors the backend's assistant:login events; the modal renders whatever
// stage the hidden CLI reached.
type AssistantLoginState = {
  provider: string
  stage: "starting" | "browser" | "done" | "error"
  url?: string
  needsCode?: boolean
  message?: string
}

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

  const [assistant, setAssistant] = useState<core.AssistantSettingsView | null>(null)
  const [assistantModels, setAssistantModels] = useState<core.AssistantModel[]>([])
  const [assistantError, setAssistantError] = useState<string | null>(null)
  const [modelsLoading, setModelsLoading] = useState(false)
  const [keyLabel, setKeyLabel] = useState("")
  const [keyDraft, setKeyDraft] = useState("")
  const [login, setLogin] = useState<AssistantLoginState | null>(null)
  const [loginCode, setLoginCode] = useState("")

  useEffect(() => {
    const off = EventsOn("assistant:login", (event: AssistantLoginState) => {
      setLogin((current) => (current ? { ...current, ...event } : current))
      if (event.stage === "done") {
        void GetAssistantSettings()
          .then(setAssistant)
          .catch(() => {})
      }
    })
    return () => {
      off()
      // Leaving Settings mid-login: kill the hidden CLI instead of leaking it.
      void CancelAssistantLogin().catch(() => {})
    }
  }, [])

  const closeLogin = () => {
    if (login && login.stage !== "done") void CancelAssistantLogin().catch(() => {})
    setLogin(null)
  }

  const startLogin = (target: string) => {
    setAssistantError(null)
    setLoginCode("")
    setLogin({ provider: target, stage: "starting" })
    void StartAssistantLogin(target).catch((cause: unknown) =>
      setLogin({ provider: target, stage: "error", message: String(cause) }),
    )
  }

  useEffect(() => {
    let mounted = true
    void GetAssistantSettings()
      .then((view) => {
        if (mounted) setAssistant(view)
      })
      .catch((cause: unknown) => {
        if (mounted) setAssistantError(String(cause))
      })
    return () => {
      mounted = false
    }
  }, [])

  const activeKeyID = assistant?.keys.find((key) => key.active)?.id ?? ""
  const provider = assistant?.provider ?? "api"
  const modelsReady = provider === "api" ? Boolean(activeKeyID) : true

  // The model list follows the active provider: the Models API for keys, the
  // CLI's local cache for subscriptions.
  useEffect(() => {
    if (section !== "agent" || !modelsReady) return
    let mounted = true
    setModelsLoading(true)
    setAssistantError(null)
    void ListAssistantModels()
      .then((models) => {
        if (mounted) setAssistantModels(models)
      })
      .catch((cause: unknown) => {
        if (mounted) {
          setAssistantModels([])
          setAssistantError(String(cause))
        }
      })
      .finally(() => {
        if (mounted) setModelsLoading(false)
      })
    return () => {
      mounted = false
    }
  }, [section, activeKeyID, provider, modelsReady])

  const mutateAssistant = async (run: () => Promise<core.AssistantSettingsView>) => {
    setAssistantError(null)
    try {
      setAssistant(await run())
    } catch (cause) {
      setAssistantError(String(cause))
    }
  }

  const addKey = async () => {
    const key = keyDraft.trim()
    if (!key) return
    await mutateAssistant(() => AddAssistantKey(keyLabel, key))
    setKeyLabel("")
    setKeyDraft("")
  }
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

            {section === "agent" && (
              <section>
                <SectionHeading>Agent APIs</SectionHeading>
                <p className="mt-2 text-xs text-[var(--on-surface-variant)]">
                  What powers the Home chat bar: an API key, or your existing
                  Claude / ChatGPT subscription through its official CLI.
                </p>

                <div className="mt-3 border-b border-[var(--outline-variant)] pb-1">
                  <Row label="Provider" description="Where the assistant's brain lives">
                    <Segmented
                      label="Provider"
                      value={provider}
                      options={[
                        { value: "api", label: "API key" },
                        { value: "claude-cli", label: "Claude Code" },
                        { value: "codex-cli", label: "Codex" },
                      ]}
                      onChange={(next) =>
                        void mutateAssistant(() => SetAssistantProvider(next))
                      }
                    />
                  </Row>
                </div>

                {assistantError && (
                  <p
                    role="alert"
                    className="mt-3 rounded-xl bg-[var(--error-container)] px-3 py-2 text-xs text-[var(--on-error-container)]"
                  >
                    {assistantError}
                  </p>
                )}

                {provider !== "api" && (
                  <div className="mt-4 rounded-xl bg-[var(--surface-container)] px-4 py-3.5 shadow-[inset_0_0_0_1px_var(--outline-variant)]">
                    <div className="flex items-center justify-between gap-4">
                      <div className="min-w-0">
                        <div className="text-sm font-medium text-[var(--on-surface)]">
                          {provider === "claude-cli"
                            ? "Claude Code subscription"
                            : "ChatGPT subscription (Codex)"}
                        </div>
                        <p className="mt-0.5 text-xs text-[var(--on-surface-variant)]">
                          {(provider === "claude-cli"
                            ? assistant?.claudeCli
                            : assistant?.codexCli
                          )?.note === "connected"
                            ? "Connected — the assistant runs on your plan, no API key."
                            : "Sign in once; if you already opened this agent's terminal in a project, you're set."}
                        </p>
                      </div>
                      <button
                        type="button"
                        onClick={() => startLogin(provider)}
                        className="flex h-8 shrink-0 items-center rounded-lg bg-[var(--primary)] px-3 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                      >
                        {(provider === "claude-cli"
                          ? assistant?.claudeCli
                          : assistant?.codexCli
                        )?.note === "connected"
                          ? "Check connection"
                          : "Connect"}
                      </button>
                    </div>
                  </div>
                )}

                {login && (
                  <div
                    role="dialog"
                    aria-modal="true"
                    aria-label="Sign in"
                    className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 backdrop-blur-sm"
                  >
                    <div className="w-[26rem] max-w-[calc(100vw-3rem)] rounded-2xl bg-[var(--surface)] p-6 shadow-2xl ring-1 ring-[var(--outline-variant)]">
                      <div className="flex items-start justify-between gap-4">
                        <div>
                          <div className="text-sm font-semibold text-[var(--on-surface)]">
                            {login.provider === "claude-cli"
                              ? "Connect Claude Code"
                              : "Connect Codex"}
                          </div>
                          <p className="mt-0.5 text-xs text-[var(--on-surface-variant)]">
                            Your subscription, no API key.
                          </p>
                        </div>
                        <button
                          type="button"
                          aria-label="Close sign-in"
                          onClick={closeLogin}
                          className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                        >
                          <X className="size-4" strokeWidth={1.8} />
                        </button>
                      </div>

                      {login.stage === "starting" && (
                        <div className="mt-6 flex flex-col items-center gap-3 pb-2 text-center">
                          <RefreshCw className="size-5 animate-spin text-[var(--primary)]" strokeWidth={1.8} />
                          <p className="text-xs text-[var(--on-surface-variant)]">
                            Preparing your sign-in…
                          </p>
                        </div>
                      )}

                      {login.stage === "browser" && (
                        <div className="mt-5">
                          <p className="text-xs leading-relaxed text-[var(--on-surface-variant)]">
                            Your browser just opened — finish signing in there.
                            Seizen takes care of the rest.
                          </p>
                          <div className="mt-3 flex gap-2">
                            <button
                              type="button"
                              onClick={() => login.url && BrowserOpenURL(login.url)}
                              className="flex h-8 items-center gap-1.5 rounded-lg bg-[var(--primary)] px-3 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                            >
                              <ExternalLink className="size-3.5" strokeWidth={1.8} />
                              Open the page again
                            </button>
                            <button
                              type="button"
                              onClick={() =>
                                login.url && void navigator.clipboard.writeText(login.url)
                              }
                              className="flex h-8 items-center gap-1.5 rounded-lg px-3 text-xs font-medium text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                            >
                              <Copy className="size-3.5" strokeWidth={1.8} />
                              Copy link
                            </button>
                          </div>
                          {login.needsCode && (
                            <div className="mt-5 border-t border-[var(--outline-variant)] pt-4">
                              <label
                                htmlFor="assistant-login-code"
                                className="text-xs font-medium text-[var(--on-surface)]"
                              >
                                Paste the code from the browser
                              </label>
                              <div className="mt-2 flex gap-2">
                                <input
                                  id="assistant-login-code"
                                  type="text"
                                  value={loginCode}
                                  autoComplete="off"
                                  spellCheck={false}
                                  placeholder="Code…"
                                  onChange={(event) => setLoginCode(event.target.value)}
                                  onKeyDown={(event) => {
                                    if (event.key === "Enter" && loginCode.trim()) {
                                      void SubmitAssistantLoginCode(loginCode)
                                      setLogin({ ...login, message: "checking" })
                                    }
                                  }}
                                  className="h-8 min-w-0 flex-1 rounded-lg bg-[var(--surface-container)] px-3 font-mono text-xs text-[var(--on-surface)] shadow-[inset_0_0_0_1px_var(--outline-variant)] outline-none placeholder:text-[var(--on-surface-variant)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                                />
                                <button
                                  type="button"
                                  disabled={!loginCode.trim()}
                                  onClick={() => {
                                    void SubmitAssistantLoginCode(loginCode)
                                    setLogin({ ...login, message: "checking" })
                                  }}
                                  className="flex h-8 shrink-0 items-center rounded-lg bg-[var(--primary)] px-3 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-40"
                                >
                                  Confirm
                                </button>
                              </div>
                              {login.message === "checking" && (
                                <p className="mt-2 flex items-center gap-1.5 text-xs text-[var(--on-surface-variant)]">
                                  <RefreshCw className="size-3 animate-spin" strokeWidth={1.8} />
                                  Checking the code…
                                </p>
                              )}
                              {login.message === "code-rejected" && (
                                <p role="alert" className="mt-2 text-xs text-[var(--error)]">
                                  That code didn't work — copy it again from the
                                  browser and paste it here.
                                </p>
                              )}
                            </div>
                          )}
                          {!login.needsCode && (
                            <p className="mt-4 flex items-center gap-1.5 text-xs text-[var(--on-surface-variant)]">
                              <RefreshCw className="size-3 animate-spin" strokeWidth={1.8} />
                              Waiting for the browser…
                            </p>
                          )}
                        </div>
                      )}

                      {login.stage === "done" && (
                        <div className="mt-6 flex flex-col items-center gap-3 pb-2 text-center">
                          <span className="flex size-10 items-center justify-center rounded-full bg-[var(--primary)]/15">
                            <Check className="size-5 text-[var(--primary)]" strokeWidth={2.2} />
                          </span>
                          <div>
                            <div className="text-sm font-medium text-[var(--on-surface)]">
                              Connected
                            </div>
                            <p className="mt-0.5 text-xs text-[var(--on-surface-variant)]">
                              The assistant now runs on your plan.
                            </p>
                          </div>
                          <button
                            type="button"
                            onClick={closeLogin}
                            className="mt-1 flex h-8 items-center rounded-lg bg-[var(--primary)] px-4 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                          >
                            Done
                          </button>
                        </div>
                      )}

                      {login.stage === "error" && (
                        <div className="mt-5">
                          <p
                            role="alert"
                            className="rounded-xl bg-[var(--error-container)] px-3 py-2 text-xs text-[var(--on-error-container)]"
                          >
                            {login.message ?? "The sign-in did not finish."}
                          </p>
                          <div className="mt-3 flex justify-end gap-2">
                            <button
                              type="button"
                              onClick={closeLogin}
                              className="flex h-8 items-center rounded-lg px-3 text-xs font-medium text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                            >
                              Close
                            </button>
                            <button
                              type="button"
                              onClick={() => startLogin(login.provider)}
                              className="flex h-8 items-center rounded-lg bg-[var(--primary)] px-3 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                            >
                              Try again
                            </button>
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                )}

                {provider === "api" && (
                  <>
                <div className="mt-4 flex items-center gap-2">
                  <input
                    type="text"
                    value={keyLabel}
                    placeholder="Label (Work, Personal…)"
                    aria-label="Key label"
                    onChange={(event) => setKeyLabel(event.target.value)}
                    className="h-8 w-40 rounded-lg bg-[var(--surface-container)] px-3 text-xs text-[var(--on-surface)] shadow-[inset_0_0_0_1px_var(--outline-variant)] outline-none placeholder:text-[var(--on-surface-variant)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                  />
                  <input
                    type="password"
                    value={keyDraft}
                    placeholder="sk-ant-..."
                    autoComplete="off"
                    aria-label="Anthropic API key"
                    onChange={(event) => setKeyDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") void addKey()
                    }}
                    className="h-8 min-w-0 flex-1 rounded-lg bg-[var(--surface-container)] px-3 font-mono text-xs text-[var(--on-surface)] shadow-[inset_0_0_0_1px_var(--outline-variant)] outline-none placeholder:text-[var(--on-surface-variant)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                  />
                  <button
                    type="button"
                    disabled={!keyDraft.trim()}
                    onClick={() => void addKey()}
                    className="flex h-8 shrink-0 items-center rounded-lg bg-[var(--primary)] px-3 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-40"
                  >
                    Add
                  </button>
                </div>

                <div className="mt-4 divide-y divide-[var(--outline-variant)] border-t border-[var(--outline-variant)]">
                  {(assistant?.keys ?? []).map((key) => (
                    <div key={key.id} className="flex items-center gap-3 py-3">
                      <input
                        type="radio"
                        name="active-assistant-key"
                        checked={key.active}
                        aria-label={`Use ${key.label}`}
                        onChange={() =>
                          void mutateAssistant(() => SelectAssistantKey(key.id))
                        }
                        className="size-4 accent-[var(--primary)]"
                      />
                      <div className="min-w-0 flex-1">
                        <div className="text-sm font-medium text-[var(--on-surface)]">
                          {key.label}
                        </div>
                        <div className="font-mono text-[0.68rem] text-[var(--on-surface-variant)]">
                          {key.masked}
                        </div>
                      </div>
                      <button
                        type="button"
                        aria-label={`Remove ${key.label}`}
                        onClick={() =>
                          void mutateAssistant(() => RemoveAssistantKey(key.id))
                        }
                        className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--error)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                      >
                        <X className="size-3.5" strokeWidth={1.8} />
                      </button>
                    </div>
                  ))}
                  {(assistant?.keys ?? []).length === 0 && (
                    <p className="py-4 text-xs text-[var(--on-surface-variant)]">
                      No keys yet. Add your Anthropic API key to bring the chat
                      bar to life.
                    </p>
                  )}
                </div>
                  </>
                )}

                <div className="mt-4 border-t border-[var(--outline-variant)]">
                  <Row
                    label="Model"
                    description={
                      modelsLoading
                        ? "Loading the models this provider offers…"
                        : assistantModels.length > 0
                          ? provider === "api"
                            ? "Models the active key can use"
                            : "Models your subscription offers"
                          : provider === "api"
                            ? "Add and activate a key to load its models"
                            : "Connect the account to load its models"
                    }
                  >
                    <div className="flex items-center gap-2">
                      <Select
                        aria-label="Assistant model"
                        value={assistant?.model ?? ""}
                        disabled={assistantModels.length === 0}
                        onChange={(event) =>
                          void mutateAssistant(() =>
                            SetAssistantModel(event.target.value),
                          )
                        }
                        className="text-xs"
                      >
                        {assistantModels.length === 0 && (
                          <option value={assistant?.model ?? ""}>
                            {assistant?.model ?? ""}
                          </option>
                        )}
                        {assistantModels.map((model) => (
                          <option key={model.id} value={model.id}>
                            {model.name}
                          </option>
                        ))}
                      </Select>
                      <button
                        type="button"
                        aria-label="Reload models"
                        disabled={modelsLoading || !modelsReady}
                        onClick={() => {
                          setModelsLoading(true)
                          setAssistantError(null)
                          void ListAssistantModels()
                            .then(setAssistantModels)
                            .catch((cause: unknown) => setAssistantError(String(cause)))
                            .finally(() => setModelsLoading(false))
                        }}
                        className="flex size-8 items-center justify-center rounded-lg text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-40"
                      >
                        <RefreshCw
                          className={cn("size-3.5", modelsLoading && "animate-spin")}
                          strokeWidth={1.8}
                        />
                      </button>
                    </div>
                  </Row>
                </div>
              </section>
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
