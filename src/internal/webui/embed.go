package webui

import "embed"

//go:embed templates/*.html static/*.css static/*.js
var content embed.FS
