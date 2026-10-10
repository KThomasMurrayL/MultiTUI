package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"multitui/internal/cli"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := cli.App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Version: version}
	if err := app.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mt:", err)
		os.Exit(1)
	}
}
