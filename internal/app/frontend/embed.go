//go:build app

package frontend

import "embed"

//go:embed all:dist
var Assets embed.FS
