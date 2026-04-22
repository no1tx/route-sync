package main

import (
	"context"
	"fmt"
	"os"

	"route-sync/internal/app"
)

var version = "dev"

func main() {
	if err := app.Run(context.Background(), os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
