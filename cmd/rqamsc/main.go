package main

import (
	"os"

	"rqams-cli/internal/app"
)

// main runs the rqamsc demo CLI.
func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
