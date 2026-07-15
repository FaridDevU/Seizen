import { useState, type FormEvent } from "react"
import { FlaskConical, LoaderCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
import type { ProjectApp, ProjectServer } from "./project-service"

export type ExperimentDraft = {
  name: string
  objective: string
  appId: string
  baseServerId: string
  agent: "codex" | "claude"
}

export function ExperimentCreateDialog({
  kind,
  apps,
  servers = [],
  busy,
  onCancel,
  onCreate,
}: {
  kind: "app" | "server"
  apps: ProjectApp[]
  servers?: ProjectServer[]
  busy: boolean
  onCancel: () => void
  onCreate: (draft: ExperimentDraft) => Promise<void>
}) {
  const [name, setName] = useState("")
  const [objective, setObjective] = useState("")
  const [appId, setAppId] = useState(apps[0]?.id ?? "")
  const availableServers = servers.filter((server) => server.appId === appId)
  const [baseServerId, setBaseServerId] = useState(servers[0]?.id ?? "")
  const [agent, setAgent] = useState<"codex" | "claude">("codex")

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const selectedBase = availableServers.some((server) => server.id === baseServerId)
      ? baseServerId
      : (availableServers[0]?.id ?? "")
    void onCreate({ name: name.trim(), objective: objective.trim(), appId, baseServerId: selectedBase, agent })
  }

  return (
    <form
      onSubmit={submit}
      className="panel-in mb-4 rounded-[1.35rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-5 shadow-[0_1px_3px_var(--shadow-soft),0_12px_32px_var(--shadow-elevated)]"
    >
      <div className="mb-4 flex items-center gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
          <FlaskConical className="size-4" strokeWidth={1.7} />
        </span>
        <div>
          <h3 className="text-sm font-semibold">New experiment</h3>
          <p className="text-xs text-[var(--on-surface-variant)]">
            Test changes without affecting Main
          </p>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="grid gap-1.5 text-xs font-medium">
          Name
          <Input required value={name} onChange={(event) => setName(event.target.value)} placeholder={kind === "app" ? "Cart redesign" : "Test Redis for sessions"} />
        </label>
        <label className="grid gap-1.5 text-xs font-medium">
          App
          <Select required value={appId} onChange={(event) => { setAppId(event.target.value); setBaseServerId("") }}>
            {apps.map((app) => <option key={app.id} value={app.id}>{app.name}</option>)}
          </Select>
        </label>
        <label className="grid gap-1.5 text-xs font-medium sm:col-span-2">
          Objective
          <Input required value={objective} onChange={(event) => setObjective(event.target.value)} placeholder="What you want to test without affecting Main" />
        </label>
        {kind === "server" && (
          <label className="grid gap-1.5 text-xs font-medium">
            Base server
            <Select required value={availableServers.some((server) => server.id === baseServerId) ? baseServerId : (availableServers[0]?.id ?? "")} onChange={(event) => setBaseServerId(event.target.value)}>
              {availableServers.map((server) => <option key={server.id} value={server.id}>{server.name}</option>)}
            </Select>
          </label>
        )}
        <label className="grid gap-1.5 text-xs font-medium">
          Agent
          <Select value={agent} onChange={(event) => setAgent(event.target.value as "codex" | "claude")}>
            <option value="codex">Codex</option>
            <option value="claude">Claude Code</option>
          </Select>
        </label>
        <div className="flex justify-end gap-2 sm:col-span-2">
          <Button type="button" variant="ghost" disabled={busy} onClick={onCancel} className="rounded-full">
            Cancel
          </Button>
          <Button
            type="submit"
            disabled={busy || !appId || (kind === "server" && availableServers.length === 0)}
            className="rounded-full"
          >
            {busy && <LoaderCircle className="size-3.5 animate-spin" />}
            {busy ? "Creating…" : "Create experiment"}
          </Button>
        </div>
      </div>
    </form>
  )
}
