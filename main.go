package main

import (
	"os"

	"github.com/chenquan/moss/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Stdout, os.Stderr))
}
