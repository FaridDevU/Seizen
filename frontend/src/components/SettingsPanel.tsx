import { Check, Moon, Palette, Sun } from "lucide-react"

import { cn } from "@/lib/utils"

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

function SettingsPanel({
  isDark,
  accent,
  onModeChange,
  onAccentChange,
}: {
  isDark: boolean
  accent: ThemeAccent
  onModeChange: (dark: boolean) => void
  onAccentChange: (accent: ThemeAccent) => void
}) {
  return (
    <section className="view-enter absolute inset-0 overflow-y-auto px-4 pb-28 pt-24 sm:px-7 lg:pl-28 lg:pr-10 lg:pt-24 2xl:pl-36 2xl:pr-14 2xl:pt-28">
      <div className="mx-auto w-full max-w-[62rem]">
        <h1 className="display-font text-[2.15rem] font-light tracking-[-0.035em] sm:text-[2.6rem]">
          Settings
        </h1>
        <p className="mt-2 text-sm text-[var(--on-surface-variant)]">
          Customize Seizen's appearance.
        </p>

        <div className="mt-8 overflow-hidden rounded-[1.75rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_16px_44px_var(--shadow-elevated)] backdrop-blur-2xl">
          <div className="flex items-start gap-4 border-b border-[var(--outline-variant)] p-5 sm:p-7">
            <span className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
              <Palette className="size-5" strokeWidth={1.7} />
            </span>
            <div>
              <h2 className="text-base font-semibold tracking-[-0.02em]">
                Appearance
              </h2>
              <p className="mt-1 text-xs leading-5 text-[var(--on-surface-variant)]">
                Changes apply instantly and save automatically.
              </p>
            </div>
          </div>

          <div className="space-y-8 p-5 sm:p-7">
            <fieldset>
              <legend className="text-xs font-semibold tracking-[0.025em] text-[var(--on-surface-variant)]">
                Mode
              </legend>
              <div className="mt-3 grid gap-3 sm:grid-cols-2">
                {[
                  { dark: false, label: "Light", description: "Bright and clean", Icon: Sun },
                  { dark: true, label: "Dark", description: "Easy on the eyes", Icon: Moon },
                ].map(({ dark, label, description, Icon }) => {
                  const selected = isDark === dark
                  return (
                    <button
                      key={label}
                      type="button"
                      aria-pressed={selected}
                      onClick={() => onModeChange(dark)}
                      className={cn(
                        "flex items-center gap-4 rounded-2xl border p-4 text-left outline-none transition-[border-color,background-color,box-shadow,transform] hover:-translate-y-0.5 focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                        selected
                          ? "border-[var(--focus-border)] bg-[var(--primary-container)] shadow-[0_8px_24px_var(--shadow-soft)]"
                          : "border-[var(--outline-variant)] bg-[var(--surface-container)] hover:bg-[var(--state-layer)]",
                      )}
                    >
                      <span className="flex size-10 items-center justify-center rounded-xl bg-[var(--surface-container-high)] text-[var(--primary)] shadow-[0_2px_8px_var(--shadow-soft)]">
                        <Icon className="size-[1.1rem]" strokeWidth={1.75} />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block text-sm font-semibold">{label}</span>
                        <span className="mt-0.5 block text-xs text-[var(--on-surface-variant)]">
                          {description}
                        </span>
                      </span>
                      {selected && <Check className="size-4 text-[var(--primary)]" />}
                    </button>
                  )
                })}
              </div>
            </fieldset>

            <fieldset>
              <legend className="text-xs font-semibold tracking-[0.025em] text-[var(--on-surface-variant)]">
                Theme color
              </legend>
              <div className="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-4">
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
        </div>
      </div>
    </section>
  )
}

export { SettingsPanel }
