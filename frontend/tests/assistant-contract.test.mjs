import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = (name) => readFile(new URL(`../src/${name}`, import.meta.url), "utf8")

test("chat bar drives the assistant and lights the app up", async () => {
  const app = await source("App.tsx")
  // Enter on the Home bar sends the prompt to Claude via the Go binding.
  assert.match(app, /AskAssistant/)
  assert.match(app, /runAssistant\(query\.trim\(\)\)/)
  // Actions execute in order and the whole app glows while they run.
  assert.match(app, /executeAssistantAction/)
  assert.match(app, /dataset\.aiActive = "on"/)
  assert.match(app, /open_project/)
  assert.match(app, /add_panel/)

  const css = await source("index.css")
  assert.match(css, /\[data-ai-active="on"\] button/)

  // Keys live in their own "Agent APIs" section: many keys, one active,
  // and the model list comes from what the API reports for that key.
  const settings = await source("components/SettingsPanel.tsx")
  assert.match(settings, /"agent", label: "Agent APIs"/)
  assert.match(settings, /AddAssistantKey/)
  assert.match(settings, /SelectAssistantKey/)
  assert.match(settings, /RemoveAssistantKey/)
  assert.match(settings, /ListAssistantModels/)
  assert.match(settings, /SetAssistantModel/)

  // Terminal panels can pick a shell (e.g. two Claude Code terminals).
  const actions = await source("features/projects/workspace-actions.ts")
  assert.match(actions, /shell\?: string/)
})
