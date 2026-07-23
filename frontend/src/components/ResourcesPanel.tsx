import { useEffect, useState, type ReactNode } from "react"
import {
  Bot,
  CircleAlert,
  Code2,
  LoaderCircle,
  Plug,
  TerminalSquare,
  type LucideIcon,
} from "lucide-react"

import {
  GetEditorIntegrations,
  GetWSLDistributions,
  InstallEditorIntegration,
  InstallWSLDistribution,
  SetDefaultWSLDistribution,
  SetEditorIntegrationEnabled,
} from "../../wailsjs/go/core/App"
import type { core } from "../../wailsjs/go/models"
import {
  projectService,
  type AgentResourceSettings,
} from "@/features/projects/project-service"
import { BrandChip, brandGlyphs } from "@/components/ui/brand-icon"
import { Checkbox } from "@/components/ui/checkbox"
import { Select } from "@/components/ui/select"
import { cn } from "@/lib/utils"

const editorDescriptions: Record<string, string> = {
  vscode: "Editor managed by Seizen",
  cursor: "Editor with AI features",
  antigravity: "AI development environment",
  zed: "Collaborative code editor",
}

const wslDescriptions: Record<string, string> = {
  debian: "Default environment for Claude Code and Codex",
  ubuntu: "Compatible with development and AI tools",
  fedora: "Up-to-date RPM-based environment",
  arch: "Up-to-date and configurable",
}

const agentEnvironmentLabels: Record<string, string> = {
  debian: "Debian 13 · WSL 2",
  ubuntu: "Ubuntu · WSL 2",
  fedora: "Fedora 44 · WSL 2",
  arch: "Arch · WSL 2",
  cmd: "Windows CMD",
}

const agentRows = [
  { id: "codex", name: "Codex", environment: "codexEnvironment", unrestricted: "codexUnrestricted" },
  { id: "claude", name: "Claude Code", environment: "claudeEnvironment", unrestricted: "claudeUnrestricted" },
  { id: "opencode", name: "OpenCode", environment: "opencodeEnvironment", unrestricted: "opencodeUnrestricted" },
] as const

const agentUnrestrictedLabels: Record<string, string> = {
  codex: "YOLO mode without approvals",
  claude: "Skip permissions (dangerously skip)",
  opencode: "Allow everything without approvals",
}

function ResourcesPanel() {
  const [integrations, setIntegrations] = useState<core.EditorIntegration[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState<string | null>(null)
  const [installing, setInstalling] = useState(false)
  const [installError, setInstallError] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [reload, setReload] = useState(0)
  const [distributions, setDistributions] = useState<
    core.WSLDistributionResource[]
  >([])
  const [wslLoading, setWslLoading] = useState(true)
  const [wslSaving, setWslSaving] = useState<string | null>(null)
  const [wslInstalling, setWslInstalling] = useState<string | null>(null)
  const [wslError, setWslError] = useState<string | null>(null)
  const [agentSettings, setAgentSettings] = useState<AgentResourceSettings | null>(null)
  const [agentSaving, setAgentSaving] = useState(false)
  const [agentError, setAgentError] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true
    let poll: number | undefined
    setLoading(true)
    setError(null)

    const load = () => {
      void GetEditorIntegrations()
        .then((items) => {
          if (!mounted) return
          setIntegrations(items)
          if (items.some((item) => item.status === "installing")) {
            poll = window.setTimeout(load, 1_000)
          }
        })
        .catch((cause: unknown) => {
          if (mounted) setError(`Could not load editors: ${String(cause)}`)
        })
        .finally(() => {
          if (mounted) setLoading(false)
        })
    }
    load()

    return () => {
      mounted = false
      if (poll !== undefined) window.clearTimeout(poll)
    }
  }, [reload])

  useEffect(() => {
    let mounted = true
    setAgentError(null)
    void projectService.getAgentResourceSettings()
      .then((settings) => {
        if (mounted) setAgentSettings(settings)
      })
      .catch((cause: unknown) => {
        if (mounted) setAgentError(`Could not load agent settings: ${String(cause)}`)
      })
    return () => {
      mounted = false
    }
  }, [reload])

  useEffect(() => {
    let mounted = true
    setWslLoading(true)
    setWslError(null)
    void GetWSLDistributions()
      .then((items) => {
        if (mounted) setDistributions(items)
      })
      .catch((cause: unknown) => {
        if (mounted) {
          setWslError(`Could not load WSL environments: ${String(cause)}`)
        }
      })
      .finally(() => {
        if (mounted) setWslLoading(false)
      })
    return () => {
      mounted = false
    }
  }, [reload])

  const setEnabled = async (integration: core.EditorIntegration) => {
    setSaving(integration.id)
    setError(null)
    try {
      setIntegrations(
        await SetEditorIntegrationEnabled(integration.id, !integration.enabled),
      )
    } catch (cause: unknown) {
      setError(`Could not update ${integration.name}: ${String(cause)}`)
    } finally {
      setSaving(null)
    }
  }

  const installVSCode = async () => {
    setInstalling(true)
    setInstallError(null)
    try {
      setIntegrations(await InstallEditorIntegration("vscode"))
    } catch (cause: unknown) {
      setInstallError(`Could not install VS Code: ${String(cause)}`)
      setReload((current) => current + 1)
    } finally {
      setInstalling(false)
    }
  }

  const selectWSL = async (distribution: core.WSLDistributionResource) => {
    if (distribution.selected) return
    setWslSaving(distribution.id)
    setWslError(null)
    try {
      setDistributions(await SetDefaultWSLDistribution(distribution.id))
    } catch (cause: unknown) {
      setWslError(`Could not select ${distribution.name}: ${String(cause)}`)
    } finally {
      setWslSaving(null)
    }
  }

  const installWSL = async (distribution: core.WSLDistributionResource) => {
    setWslInstalling(distribution.id)
    setWslError(null)
    try {
      setDistributions(await InstallWSLDistribution(distribution.id))
    } catch (cause: unknown) {
      setWslError(String(cause))
    } finally {
      setWslInstalling(null)
    }
  }

  const saveAgentSettings = async (next: AgentResourceSettings) => {
    setAgentSaving(true)
    setAgentError(null)
    try {
      setAgentSettings(await projectService.setAgentResourceSettings(next))
    } catch (cause: unknown) {
      setAgentError(`Could not save agent settings: ${String(cause)}`)
    } finally {
      setAgentSaving(false)
    }
  }

  // Rendered inside the Settings modal, so no page chrome of its own.
  return (
    <div>
      <p className="text-xs text-[var(--on-surface-variant)]">
        Editors, AI agents, and environments available within Seizen
      </p>

      <div className="mt-4">
          <SectionCard
            icon={Bot}
            title="AI agents"
            description="Where each agent runs and whether it can skip approvals"
          >
            {agentError && <ErrorBanner message={agentError} />}
            {!agentSettings ? (
              <SectionLoading label="Loading agents…" />
            ) : (
              <>
                <div className="grid sm:grid-cols-2 sm:divide-x sm:divide-[var(--outline-variant)]">
                  {agentRows.map((agent) => (
                    <div key={agent.id} className="space-y-2.5 px-4 py-3.5">
                      <div className="flex items-center gap-2">
                        <BrandChip
                          brand={agent.id}
                          className="size-6 rounded-[0.45rem]"
                          iconClassName="size-3.5"
                        />
                        <span className="text-[0.8rem] font-semibold">{agent.name}</span>
                      </div>
                      <Select
                        aria-label={`${agent.name} environment`}
                        value={agentSettings[agent.environment]}
                        disabled={agentSaving || (agent.id === "claude" && agentSettings.claudeUnrestricted)}
                        onChange={(event) => void saveAgentSettings({
                          ...agentSettings,
                          [agent.environment]: event.target.value,
                        })}
                        className="text-xs"
                      >
                        {Object.entries(agentEnvironmentLabels)
                          .filter(([id]) => !(agent.id === "opencode" && id === "cmd"))
                          .map(([id, label]) => {
                            const resource = distributions.find((item) => item.id === id)
                            return <option key={id} value={id}>{label}{resource && !resource.installed ? " · not installed" : ""}</option>
                          })}
                      </Select>
                      <label className="flex w-fit items-center gap-2 text-xs text-[var(--on-surface-variant)]">
                        <Checkbox
                          checked={agentSettings[agent.unrestricted]}
                          disabled={agentSaving}
                          onChange={(event) => void saveAgentSettings({
                            ...agentSettings,
                            [agent.unrestricted]: event.target.checked,
                          })}
                        />
                        {agentUnrestrictedLabels[agent.id]}
                      </label>
                      {agent.id === "claude" && agentSettings.claudeUnrestricted && (
                        <p role="alert" className="flex items-start gap-1.5 text-[0.66rem] text-[var(--warning)]">
                          <CircleAlert className="mt-px size-3 shrink-0" strokeWidth={1.8} />
                          With permissions skipped, Claude runs in Windows CMD, not WSL:
                          --dangerously-skip-permissions cannot run as root and Seizen's
                          WSL sessions log in as root.
                        </p>
                      )}
                    </div>
                  ))}
                </div>
                <label className="flex items-center gap-2.5 border-t border-[var(--outline-variant)] px-4 py-3 text-xs text-[var(--on-surface-variant)]">
                  <Checkbox
                    checked={agentSettings.sharedExtensions}
                    disabled={agentSaving}
                    onChange={(event) => void saveAgentSettings({
                      ...agentSettings,
                      sharedExtensions: event.target.checked,
                    })}
                  />
                  <span>
                    <span className="font-semibold text-[var(--on-surface)]">Share skills and plugins across projects</span>
                    <span className="ml-1.5">· with this on, agents reuse the global profile</span>
                  </span>
                </label>
              </>
            )}
          </SectionCard>
        </div>

        <div className="mt-4 grid items-start gap-4">
          <SectionCard
            icon={Plug}
            title="Code editors"
            description="VS Code comes active and managed by Seizen"
          >
            {error && (
              <ErrorBanner message={error}>
                {!loading && integrations.length === 0 && (
                  <button
                    type="button"
                    className="shrink-0 font-semibold underline underline-offset-2"
                    onClick={() => setReload((current) => current + 1)}
                  >
                    Retry
                  </button>
                )}
              </ErrorBanner>
            )}
            {loading ? (
              <SectionLoading label="Loading editors…" />
            ) : (
              <div className="divide-y divide-[var(--outline-variant)]">
                {integrations.map((integration) => {
                  const isInstalling = integration.managed
                    && (installing || integration.status === "installing")
                  const hasInstallError = integration.managed
                    && (installError !== null || integration.status === "error")
                  const isAvailable = integration.available || integration.status === "installed"
                  const canInstall = integration.managed
                    && !isInstalling
                    && (integration.status === "not_installed" || integration.status === "error")

                  return (
                    <div key={integration.id} className="flex items-center gap-3 px-4 py-3">
                      {brandGlyphs[integration.id] ? (
                        <BrandChip
                          brand={integration.id}
                          className="size-8 rounded-lg"
                          iconClassName="size-4"
                        />
                      ) : (
                        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-[var(--surface-container)] text-[var(--primary)]">
                          <Code2 className="size-4" strokeWidth={1.75} />
                        </span>
                      )}
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-[0.8rem] font-semibold">{integration.name}</span>
                        <span
                          className={cn(
                            "block truncate text-[0.66rem]",
                            hasInstallError
                              ? "text-[var(--error)]"
                              : "text-[var(--on-surface-variant)]",
                          )}
                          role={hasInstallError ? "alert" : undefined}
                          title={editorDescriptions[integration.id]}
                        >
                          {isInstalling
                            ? "Installing…"
                            : hasInstallError
                              ? installError || integration.errorMessage || "Could not install VS Code"
                              : isAvailable
                                ? editorDescriptions[integration.id] ?? "Available"
                                : integration.status === "not_installed"
                                  ? "Pending install"
                                  : integration.status === "unsupported"
                                    ? "Not available on this system"
                                    : "Not detected on this machine"}
                        </span>
                      </span>
                      <StatusDot
                        tone={isInstalling ? "warning" : hasInstallError ? "error" : isAvailable ? "success" : "muted"}
                      />
                      {canInstall && (
                        <button
                          type="button"
                          onClick={() => void installVSCode()}
                          className="flex h-7 shrink-0 items-center gap-1.5 rounded-full bg-[var(--primary)] px-3 text-[0.68rem] font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                        >
                          {integration.status === "error" ? "Retry" : "Install"}
                        </button>
                      )}
                      <button
                        type="button"
                        role="switch"
                        aria-checked={integration.enabled}
                        aria-label={`${integration.enabled ? "Disable" : "Enable"} ${integration.name}`}
                        disabled={saving !== null || isInstalling}
                        onClick={() => void setEnabled(integration)}
                        className={cn(
                          "relative h-6 w-10 shrink-0 rounded-full border outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:cursor-wait disabled:opacity-60",
                          integration.enabled
                            ? "border-[var(--focus-border)] bg-[var(--primary)]"
                            : "border-[var(--outline-variant)] bg-[var(--surface-container)]",
                        )}
                      >
                        <span
                          aria-hidden="true"
                          className={cn(
                            "absolute left-1 top-1 size-3.5 rounded-full bg-white shadow-sm transition-transform",
                            integration.enabled && "translate-x-4",
                          )}
                        />
                      </button>
                    </div>
                  )
                })}
              </div>
            )}
          </SectionCard>

          <SectionCard
            icon={TerminalSquare}
            title="WSL environments"
            description="Debian 13 is selected by default · each environment installs into its managed folder"
          >
            {wslError && <ErrorBanner message={wslError} />}
            <div
              role="radiogroup"
              aria-label="Default WSL distribution in Seizen"
              className="divide-y divide-[var(--outline-variant)]"
            >
              {wslLoading ? (
                <SectionLoading label="Checking environments…" />
              ) : (
                distributions.map((distribution) => {
                  const installingWSL = wslInstalling === distribution.id
                  const unavailable = distribution.status === "unavailable"
                  const restartRequired = distribution.status === "restart_required"
                  return (
                    <div key={distribution.id} className="flex items-center gap-3 px-4 py-3">
                      <button
                        type="button"
                        role="radio"
                        aria-checked={distribution.selected}
                        aria-label={`Use ${distribution.name} in WSL terminals`}
                        disabled={wslSaving !== null || wslInstalling !== null}
                        onClick={() => void selectWSL(distribution)}
                        className={cn(
                          "flex size-5 shrink-0 items-center justify-center rounded-full border outline-none transition-colors hover:border-[var(--primary)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-60",
                          distribution.selected
                            ? "border-[var(--primary)]"
                            : "border-[var(--outline-variant)]",
                        )}
                      >
                        {distribution.selected && (
                          <span className="size-2.5 rounded-full bg-[var(--primary)]" />
                        )}
                      </button>
                      {brandGlyphs[distribution.id] && (
                        <BrandChip
                          brand={distribution.id}
                          className="size-8 rounded-lg"
                          iconClassName="size-4"
                        />
                      )}
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-[0.8rem] font-semibold">
                          {distribution.name}
                          {distribution.selected && (
                            <span className="ml-2 rounded-full bg-[var(--primary-container)] px-1.5 py-px text-[0.58rem] font-medium text-[var(--on-primary-container)]">
                              Selected
                            </span>
                          )}
                        </span>
                        <span
                          className={cn(
                            "block truncate text-[0.66rem]",
                            unavailable || restartRequired
                              ? "text-[var(--error)]"
                              : "text-[var(--on-surface-variant)]",
                          )}
                          title={wslDescriptions[distribution.id]}
                        >
                          {installingWSL
                            ? "Installing…"
                            : restartRequired
                              ? distribution.errorMessage || "Restart Windows to finish enabling WSL"
                              : unavailable
                                ? distribution.errorMessage || "WSL not available"
                                : distribution.installed
                                  ? wslDescriptions[distribution.id] ?? "Available"
                                  : "Pending install"}
                        </span>
                      </span>
                      <StatusDot
                        tone={installingWSL ? "warning" : unavailable || restartRequired ? "error" : distribution.installed ? "success" : "muted"}
                      />
                      {!distribution.installed && !unavailable && !restartRequired && (
                        <button
                          type="button"
                          disabled={wslInstalling !== null || wslSaving !== null}
                          onClick={() => void installWSL(distribution)}
                          className="flex h-7 shrink-0 items-center gap-1.5 rounded-full bg-[var(--primary)] px-3 text-[0.68rem] font-semibold text-[var(--primary-foreground)] outline-none transition-opacity hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:cursor-wait disabled:opacity-60"
                        >
                          {installingWSL && <LoaderCircle className="size-3 animate-spin" />}
                          {installingWSL ? "Installing…" : "Install"}
                        </button>
                      )}
                    </div>
                  )
                })
              )}
            </div>
          </SectionCard>
        </div>
    </div>
  )
}

function SectionCard({
  icon: Icon,
  title,
  description,
  children,
}: {
  icon: LucideIcon
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <div className="overflow-hidden rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_1px_3px_var(--shadow-soft),0_10px_28px_var(--shadow-elevated)] backdrop-blur-2xl">
      <div className="flex items-center gap-3 border-b border-[var(--outline-variant)] px-4 py-3">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-[var(--primary-container)] text-[var(--on-primary-container)]">
          <Icon className="size-4" strokeWidth={1.7} />
        </span>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold tracking-[-0.01em]">{title}</h2>
          <p className="truncate text-[0.66rem] text-[var(--on-surface-variant)]">
            {description}
          </p>
        </div>
      </div>
      {children}
    </div>
  )
}

function SectionLoading({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-8 text-xs text-[var(--on-surface-variant)]">
      <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
      {label}
    </div>
  )
}

function ErrorBanner({ message, children }: { message: string; children?: ReactNode }) {
  return (
    <div
      role="alert"
      className="view-enter m-3 flex items-start gap-2.5 rounded-xl bg-[var(--error-container)] px-3 py-2.5 text-xs text-[var(--on-error-container)]"
    >
      <CircleAlert className="mt-0.5 size-4 shrink-0" strokeWidth={1.8} />
      <span className="min-w-0 flex-1 break-words">{message}</span>
      {children}
    </div>
  )
}

function StatusDot({ tone }: { tone: "success" | "warning" | "error" | "muted" }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        tone === "success" && "bg-[var(--success)]",
        tone === "warning" && "bg-[var(--warning)] animate-pulse",
        tone === "error" && "bg-[var(--error)]",
        tone === "muted" && "bg-[var(--outline-variant)]",
      )}
    />
  )
}

export { ResourcesPanel }
