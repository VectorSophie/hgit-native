// Command hgit is the native hgit command line: see internal/cli.
package main

import (
	"os"

	"github.com/VectorSophie/hgit-native/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr)) }
