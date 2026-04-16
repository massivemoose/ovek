package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/massivemoose/alces/internal/cli/client"
	"github.com/massivemoose/alces/internal/cli/command"
	"github.com/massivemoose/alces/internal/cli/config"
)

func newAuthCommand(stdout io.Writer, stderr io.Writer, store *config.Store) command.Command {
	return command.NewRouter(
		"auth",
		"Manage local Brain authentication.",
		&authLoginCommand{stdout: stdout, stderr: stderr, config: store},
		&authStatusCommand{stdout: stdout, config: store},
		&authLogoutCommand{stdout: stdout, config: store},
	)
}

type authLoginCommand struct {
	stdout io.Writer
	stderr io.Writer
	config *config.Store
}

func (cmd *authLoginCommand) Name() string { return "login" }

func (cmd *authLoginCommand) Summary() string { return "Verify and save local Brain auth" }

func (cmd *authLoginCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth login", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	host := flagSet.String("host", config.DefaultHost, "Brain base URL")
	apiKey := flagSet.String("api-key", "", "Brain API key")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth login does not accept positional arguments")
	}
	if strings.TrimSpace(*apiKey) == "" {
		return fmt.Errorf("alces auth login requires --api-key")
	}

	brainClient, err := client.New(*host, *apiKey)
	if err != nil {
		return err
	}
	if err := brainClient.Ping(ctx); err != nil {
		return fmt.Errorf("verify credentials: %w", err)
	}

	if err := cmd.config.Save(config.Config{
		Host:   *host,
		APIKey: *apiKey,
	}); err != nil {
		return err
	}

	configPath, err := cmd.config.Path()
	if err != nil {
		return err
	}
	savedConfig, err := cmd.config.Load()
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Saved credentials for %s\n", savedConfig.Host)
	_, _ = fmt.Fprintf(cmd.stdout, "Config: %s\n", configPath)
	return nil
}

func (cmd *authLoginCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth login [--host <url>] --api-key <key>\n")
}

type authStatusCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authStatusCommand) Name() string { return "status" }

func (cmd *authStatusCommand) Summary() string { return "Show local auth configuration status" }

func (cmd *authStatusCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth status", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth status does not accept positional arguments")
	}

	configPath, err := cmd.config.Path()
	if err != nil {
		return err
	}

	cfg, err := cmd.config.Load()
	if errors.Is(err, config.ErrNotFound) {
		_, _ = fmt.Fprintf(cmd.stdout, "Authenticated: no\n")
		_, _ = fmt.Fprintf(cmd.stdout, "Host: %s\n", config.DefaultHost)
		_, _ = fmt.Fprintf(cmd.stdout, "Config: %s\n", configPath)
		return nil
	}
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Authenticated: yes\n")
	_, _ = fmt.Fprintf(cmd.stdout, "Host: %s\n", cfg.Host)
	_, _ = fmt.Fprintf(cmd.stdout, "Config: %s\n", configPath)
	return nil
}

func (cmd *authStatusCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth status\n")
}

type authLogoutCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authLogoutCommand) Name() string { return "logout" }

func (cmd *authLogoutCommand) Summary() string { return "Clear local Brain auth" }

func (cmd *authLogoutCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth logout", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth logout does not accept positional arguments")
	}

	if err := cmd.config.Clear(); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Cleared local auth.\n")
	return nil
}

func (cmd *authLogoutCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth logout\n")
}
