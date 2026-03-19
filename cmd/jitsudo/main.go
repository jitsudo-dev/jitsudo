// jitsudo is the CLI client for the jitsudo Just-In-Time privileged access management system.
//
// License: Apache 2.0
package main

import (
	"fmt"
	"os"

	"github.com/jitsudo-dev/jitsudo/internal/cli"
)

func main() {
	root := cli.NewRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
