package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"somewear.com/grid-uxs-wizard/internal/wizard"
)

func main() {
	if err := wizard.Run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "grid-uxs-wizard:", err)
		os.Exit(1)
	}
}
