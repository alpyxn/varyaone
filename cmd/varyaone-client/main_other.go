//go:build !windows

package main

import (
	"fmt"
	"os"
)

// The Varya One desktop client embeds a WebView2 window and is Windows-only.
// This stub keeps `go build ./...` working on other platforms.
func main() {
	fmt.Fprintln(os.Stderr, "varyaone-client: yalnızca Windows üzerinde çalışır")
	os.Exit(1)
}
