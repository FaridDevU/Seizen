import assert from "node:assert/strict"
import test from "node:test"

const store = new Map()
globalThis.localStorage = {
  getItem: (key) => (store.has(key) ? store.get(key) : null),
  setItem: (key, value) => store.set(key, String(value)),
}

const { nextGreeting } = await import("../src/lib/greetings.ts")

test("never repeats a phrase until its pool is exhausted", () => {
  store.clear()
  // Tuesday 15:00 -> plain "tarde" pool (45 phrases), no day extras.
  const now = new Date(2026, 6, 21, 15, 0, 0)
  const seen = new Set()
  for (let i = 0; i < 45; i++) {
    const phrase = nextGreeting("es", now)
    assert.ok(!seen.has(phrase), `repeated too early: ${phrase}`)
    seen.add(phrase)
  }
  // Pool exhausted: the next pick must recycle from the same pool.
  assert.ok(seen.has(nextGreeting("es", now)))
  // English pool is independent: nothing from it was consumed.
  assert.ok(!seen.has(nextGreeting("en", now)))
})
