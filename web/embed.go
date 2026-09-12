package web

import "embed"

//go:embed templates/*.html templates/partials/*.html templates/mobile/*.html templates/mobile/partials/*.html
var Templates embed.FS

//go:embed static/css/*.css static/js/*.js static/img/*
var Static embed.FS
