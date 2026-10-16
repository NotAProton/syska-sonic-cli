package main

import (
	"os"

	"github.com/notaproton/syska-sonic-cli/internal/cli"
)

// Version is stamped by the Makefile: -ldflags "-X main.version=$(VERSION)".
var version = "dev"

func main() {
	os.Exit(cli.Execute(os.Args[1:], version))
}
