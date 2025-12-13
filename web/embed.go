package web

import "embed"

//go:embed templates/*.html templates/partials/*.html
var Templates embed.FS

//go:embed static/css/*.css static/js/*.js
var Static embed.FS
