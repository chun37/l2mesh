package main

import (
	"fmt"
	"os"

	"github.com/chun37/l2mesh/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
