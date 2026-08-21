package main

import (
	"os"

	"cairn/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Stdout, os.Stderr))
}
