package main

import (
	"fmt"
	"os"

	"github.com/l3aro/perk-workbench-plugin-sdk-go/server"
	"github.com/l3aro/postgres-driver-for-perk-workbench/postgres"
)

func main() {
	if err := server.Run(os.Stdin, os.Stdout, postgres.Factory{}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[perk-postgres] fatal: %v\n", err)
		os.Exit(1)
	}
}
