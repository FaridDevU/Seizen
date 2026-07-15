import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = (name) => readFile(new URL(`../src/${name}`, import.meta.url), "utf8")

test("Resources configures editors and distinguishes integration from install", async () => {
  const [app, resources] = await Promise.all([
    source("App.tsx"),
    source("components/ResourcesPanel.tsx"),
  ])

  assert.match(app, /activeItem === "resources" && <ResourcesPanel/)
  for (const editor of ["vscode", "cursor", "antigravity", "zed"]) {
    assert.match(resources, new RegExp(editor))
  }
  assert.match(resources, /GetEditorIntegrations/)
  assert.match(resources, /InstallEditorIntegration\("vscode"\)/)
  assert.match(resources, /SetEditorIntegrationEnabled/)
  assert.match(resources, /role="switch"/)
  assert.match(resources, /integration\.available/)
  assert.match(resources, /Not detected on this machine/)
  assert.match(resources, /item\.status === "installing"/)
  assert.match(resources, /integration\.status === "error"/)
  assert.match(resources, /integration\.errorMessage/)
  assert.match(resources, /integration\.status === "not_installed"/)
  assert.match(resources, /"Installing…"/)
  assert.match(resources, /"Pending install"/)
  assert.match(resources, /integration\.status === "error" \? "Retry" : "Install"/)
  assert.doesNotMatch(resources, /se instalan por separado/)
  assert.match(resources, /role="alert"/)
})

test("Resources configures agents and managed WSL", async () => {
  const resources = await source("components/ResourcesPanel.tsx")
  for (const contract of [
    "WSL environments",
    "ubuntu",
    "debian",
    "fedora",
    "arch",
    "GetWSLDistributions",
    "SetDefaultWSLDistribution",
    "InstallWSLDistribution",
  ]) assert.match(resources, new RegExp(contract))
  assert.match(resources, /role="radiogroup"/)
  assert.match(resources, /role="radio"/)
  assert.match(resources, /Debian 13 is selected by default/)
  assert.match(resources, /managed folder/)
  assert.match(resources, /distribution\.status === "restart_required"/)
  assert.match(resources, /!restartRequired/)
  assert.match(resources, /Restart Windows to finish enabling WSL/)
  for (const contract of [
    "AI agents",
    "Debian 13",
    "Windows CMD",
    "YOLO mode",
    "dangerously skip",
    "Share skills and plugins across projects",
    "getAgentResourceSettings",
    "setAgentResourceSettings",
  ]) assert.match(resources, new RegExp(contract))
})
