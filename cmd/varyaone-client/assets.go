//go:build windows

package main

import _ "embed"

//go:embed assets/connect.html
var connectHTML string

//go:embed assets/panel.html
var panelHTML string

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"
