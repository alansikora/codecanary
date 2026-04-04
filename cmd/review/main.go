package main

import (
	"os"

	"github.com/alansikora/codecanary/cmd/review/cli"
	"github.com/alansikora/codecanary/internal/telemetry"
)

var version = "dev"

func main() {
	cli.Version = version
	err := cli.Execute()
	telemetry.Wait()
	if err != nil {
		os.Exit(1)
	}
}
