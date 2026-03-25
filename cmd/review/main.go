package main

import (
	"os"

	"github.com/alansikora/codecanary/cmd/review/cli"
)

var version = "dev"

func main() {
	cli.Version = version
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
