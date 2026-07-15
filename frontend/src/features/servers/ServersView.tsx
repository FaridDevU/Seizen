import { useCallback, useEffect, useState } from "react"
import { EventsOn } from "../../../wailsjs/runtime/runtime"
import {
  CircleAlert,
  LoaderCircle,
  Play,
  Server,
  Square,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  projectService,
  type GlobalServer,
  type ServerStats,
  type ServerStatus,
} from "@/features/projects/project-service"

const statusDots: Record<ServerStatus, string> = {
  draft: "bg-[var(--outline-variant)]",
  provisioning: "bg-[var(--warning)] animate-pulse",
  stopped: "bg-[var(--outline-variant)]",
  starting: "bg-[var(--warning)] animate-pulse",
  running: "bg-[var(--success)]",
  degraded: "bg-[var(--warning)]",
  stopping: "bg-[var(--warning)] animate-pulse",
  failed: "bg-[var(--error)]",
  deleting: "bg-[var(--warning)] animate-pulse",
}

const statusLabels: Record<ServerStatus, string> = {
  draft: "Draft",
  provisioning: "Provisioning",
  stopped: "Stopped",
  starting: "Starting",
  running: "Running",
  degraded: "Degraded",
  stopping: "Stopping",
  failed: "Failed",
  deleting: "Deleting",
}

type ServersViewProps = {
  onOpen: (server: GlobalServer) => void
}

export function ServersView({ onOpen }: ServersViewProps) {
  const [servers, setServers] = useState<GlobalServer[]>([])
  const [stats, setStats] = useState<Record<string, ServerStats>>({})
  const [loading, setLoading] = useState(true)
  const [busyId, setBusyId] = useState("")
  const [error, setError] = useState("")

  const refreshStats = useCallback(async (items: GlobalServer[]) => {
    const active = items.filter((server) =>
      ["running", "degraded"].includes(server.status),
    )
    if (active.length === 0) {
      setStats({})
      return
    }
    const entries = await Promise.all(
      active.map(async (server) => {
        try {
          return [server.id, await projectService.getServerStats(server.id)] as const
        } catch {
          return null
        }
      }),
    )
    setStats(
      Object.fromEntries(entries.filter((entry) => entry !== null)),
    )
  }, [])

  const load = useCallback(async () => {
    try {
      await projectService.initialize()
      const next = (await projectService.listAllServers()) ?? []
      setServers(next)
      setError("")
      void refreshStats(next)
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setLoading(false)
    }
  }, [refreshStats])

  useEffect(() => {
    if (!servers.some((server) => ["running", "degraded"].includes(server.status))) return
    const timer = window.setInterval(() => void refreshStats(servers), 5_000)
    return () => window.clearInterval(timer)
  }, [refreshStats, servers])

  useEffect(() => {
    void load()
    const subscriptions = [
      "server.provisioning",
      "server.starting",
      "server.running",
      "server.degraded",
      "server.stopping",
      "server.stopped",
      "server.failed",
      "server.deleted",
    ].map((event) => EventsOn(event, () => void load()))
    return () => subscriptions.forEach((off) => off())
  }, [load])

  const act = async (
    server: GlobalServer,
    action: (id: string) => Promise<unknown>,
  ) => {
    setBusyId(server.id)
    setError("")
    try {
      await action(server.id)
      await load()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusyId("")
    }
  }

  return (
    <section className="view-enter absolute inset-0 overflow-y-auto px-4 pb-28 pt-24 sm:px-7 lg:pl-28 lg:pr-10 2xl:pl-36 2xl:pr-14 2xl:pt-28">
      <div className="mx-auto w-full max-w-[76rem]">
        <div>
          <h1 className="display-font text-[2.15rem] font-light tracking-[-0.035em] sm:text-[2.6rem]">
            Servers
          </h1>
          <p className="mt-2 text-[0.8rem] font-medium text-[var(--on-surface-variant)]">
            Local servers linked to your Apps
          </p>
        </div>

        {error && (
          <p
            role="alert"
            className="view-enter mt-6 flex items-center gap-2 rounded-xl bg-[var(--error-container)] px-3 py-2 text-xs text-[var(--on-error-container)]"
          >
            <CircleAlert className="size-4 shrink-0" />
            {error}
          </p>
        )}

        <div className="mt-7">
          {loading ? (
            <div className="flex min-h-56 items-center justify-center rounded-2xl bg-[var(--surface-container-high)] text-sm text-[var(--on-surface-variant)]">
              <LoaderCircle className="mr-2 size-4 animate-spin" />
              Loading servers
            </div>
          ) : servers.length === 0 ? (
            <div className="flex min-h-64 flex-col items-center justify-center rounded-2xl bg-[var(--surface-container-high)] px-6 text-center">
              <span className="flex size-10 items-center justify-center rounded-2xl bg-[var(--surface-container)] text-[var(--on-surface-variant)]">
                <Server className="size-[1.15rem]" strokeWidth={1.6} />
              </span>
              <p className="mt-4 text-sm font-semibold">
                No servers configured
              </p>
              <p className="mt-1 max-w-sm text-xs leading-5 text-[var(--on-surface-variant)]">
                Create them from Server Lab inside a project with an App.
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              {servers.map((server) => {
                const busy = busyId === server.id
                const canStart = ["stopped", "failed"].includes(
                  server.status,
                )
                const canStop = [
                  "provisioning",
                  "starting",
                  "running",
                  "degraded",
                ].includes(server.status)

                return (
                  <article
                    key={server.id}
                    className="flex min-w-0 flex-col gap-3 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-4 py-3 shadow-[0_1px_3px_var(--shadow-soft)] transition-shadow hover:shadow-[0_1px_3px_var(--shadow-soft),0_6px_18px_var(--shadow-elevated)] sm:flex-row sm:items-center"
                  >
                    <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
                      <Server className="size-4" strokeWidth={1.7} />
                    </span>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-semibold">
                        {server.name}
                      </p>
                      <p className="mt-0.5 truncate text-[0.68rem] text-[var(--on-surface-variant)]">
                        {server.projectName ?? server.projectId} ·{" "}
                        {server.appName ?? server.appId}
                      </p>
                    </div>
                    <div className="flex flex-wrap items-center gap-2 text-[0.68rem] text-[var(--on-surface-variant)]">
                      <span className="flex items-center gap-1.5">
                        <span
                          aria-hidden="true"
                          className={`size-1.5 rounded-full ${statusDots[server.status]}`}
                        />
                        {statusLabels[server.status]}
                      </span>
                      <span aria-hidden="true">·</span>
                      <span>
                        {server.cpuLimit} CPU · {server.memoryMb} MB RAM
                      </span>
                      {stats[server.id] && (
                        <>
                          <span aria-hidden="true">·</span>
                          <span className="tabular-nums font-medium text-[var(--on-surface)]">
                            {Math.round(stats[server.id].cpuPercent)}% CPU ·{" "}
                            {stats[server.id].memoryUsedMb}
                            {stats[server.id].memoryLimitMb > 0
                              ? `/${stats[server.id].memoryLimitMb}`
                              : ""}{" "}
                            MB in use
                          </span>
                        </>
                      )}
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      {canStart && (
                        <Button
                          type="button"
                          variant="ghost"
                          disabled={busyId !== ""}
                          onClick={() => void act(server, projectService.startServer)}
                          className="h-8 rounded-full px-3 text-xs"
                        >
                          {busy ? (
                            <LoaderCircle className="size-3.5 animate-spin" />
                          ) : (
                            <Play className="size-3.5" />
                          )}
                          Start
                        </Button>
                      )}
                      {canStop && (
                        <Button
                          type="button"
                          variant="ghost"
                          disabled={busyId !== ""}
                          onClick={() => void act(server, projectService.stopServer)}
                          className="h-8 rounded-full px-3 text-xs"
                        >
                          {busy ? (
                            <LoaderCircle className="size-3.5 animate-spin" />
                          ) : (
                            <Square className="size-3.5" />
                          )}
                          Stop
                        </Button>
                      )}
                      <Button
                        type="button"
                        variant="outline"
                        disabled={busyId !== ""}
                        onClick={() => onOpen(server)}
                        className="h-8 rounded-full border-[var(--outline-variant)] px-3 text-xs shadow-none"
                      >
                        Open
                      </Button>
                    </div>
                  </article>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </section>
  )
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}
