package main

import (
	"os"

	"github.com/cwbudde/glockenspiel/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
