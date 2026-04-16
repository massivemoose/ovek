package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/massivemoose/alces/internal/cli/command"
	"github.com/massivemoose/alces/internal/cli/config"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	return runApp(ctx, args, stdin, stdout, stderr, config.NewStore(""))
}

func runApp(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, store *config.Store) int {
	router := newRootRouter(stdin, stdout, stderr, store)
	err := router.Run(ctx, args)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, command.ErrUsage):
		router.Usage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
}

func newRootRouter(stdin io.Reader, stdout io.Writer, stderr io.Writer, store *config.Store) *command.Router {
	prompts := newStdioPrompter(stdin, stdout)
	return command.NewRouter(
		"alces",
		"Alces CLI for working with the Brain control plane.",
		newAuthCommand(stdout, stderr, store, prompts),
		newDeployCommand(stdout, store, prompts),
		newLogsCommand(stdout, store),
		newStatusCommand(stdout, store),
	)
}

type stubCommand struct {
	name    string
	summary string
	stderr  io.Writer
}

func newStubCommand(name string, summary string, stderr io.Writer) *stubCommand {
	return &stubCommand{
		name:    name,
		summary: summary,
		stderr:  stderr,
	}
}

func (command *stubCommand) Name() string { return command.name }

func (command *stubCommand) Summary() string { return command.summary }

func (command *stubCommand) Run(_ context.Context, _ []string) error {
	return fmt.Errorf("%s is not implemented yet", command.name)
}

func (command *stubCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces %s\n", command.name)
}
