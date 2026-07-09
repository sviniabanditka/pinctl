// Package web embeds the built Vue Flow editor (web/dist, produced by `npm
// run build` in this directory) into the pinctl binary, per
// docs/03-architecture.md's ADR-4 (embedded UI, not a separately served
// frontend) and docs/10-api.md's "Static UI serving" section.
//
// web/dist/index.html committed here is a placeholder ("pinctl UI
// placeholder — build web app") so `go:embed` — which requires the embedded
// path to exist at compile time — always has something to embed even
// before the frontend is built. Running the real frontend build overwrites
// everything under web/dist (including this placeholder) with the actual
// app; internal/api serves whatever is present in FS verbatim, so it
// automatically picks up the real build once one has been run.
package web

import "embed"

//go:embed all:dist
var FS embed.FS
