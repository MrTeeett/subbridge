//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() { fmt.Fprintln(os.Stderr, "Use scripts/with-limits.sh on Linux."); os.Exit(1) }
