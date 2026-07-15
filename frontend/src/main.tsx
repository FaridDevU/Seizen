import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import App from "./App"
import "./index.css"

document.documentElement.classList.toggle(
  "dark",
  localStorage.getItem("seizen-theme") === "dark",
)

const cachedAccent = localStorage.getItem("seizen-accent")
document.documentElement.dataset.accent =
  cachedAccent === "blue" ||
  cachedAccent === "violet" ||
  cachedAccent === "emerald" ||
  cachedAccent === "amber"
    ? cachedAccent
    : "blue"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
