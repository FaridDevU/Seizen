package main

import (
	"embed"

	"seizen/internal/core"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	core.Run(assets)
}
