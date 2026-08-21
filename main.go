package main

import (
	"os"

	"moss/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Stdout, os.Stderr))
}
