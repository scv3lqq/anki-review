// Package web holds the review page, embedded into the binary.
package web

import "embed"

// FS has index.html, app.js, style.css and icon.svg at its root.
//
//go:embed index.html app.js style.css icon.svg
var FS embed.FS
