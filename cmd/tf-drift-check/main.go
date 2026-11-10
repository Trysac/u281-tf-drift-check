package main

import (
	"os"

	tfdriftcheck "github.com/Trysac/u281-tf-drift-check"
)

func main() {
	os.Exit(tfdriftcheck.Run(os.Args[1:], os.Stdout, os.Stderr))
}
