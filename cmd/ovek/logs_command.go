package main

import (
	"context"
	"fmt"
	"io"

	"github.com/massivemoose/ovek/internal/cli/chomp"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/projectctx"
)

type logsCommand struct {
	stdout io.Writer
	config *config.Store
}

func newLogsCommand(stdout io.Writer, store *config.Store) command.Command {
	return &logsCommand{
		stdout: stdout,
		config: store,
	}
}

func (cmd *logsCommand) Name() string { return "logs" }

func (cmd *logsCommand) Summary() string { return "Read build and runtime logs" }

func (cmd *logsCommand) Run(ctx context.Context, args []string) error {
	jobID, noFollow, positionals, err := parseLogsArgs(args)
	if err != nil {
		return err
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	if jobID != "" {
		if len(positionals) != 0 {
			return fmt.Errorf("ovek logs --job does not accept a <project> argument")
		}
		return cmd.runJob(ctx, brainClient, jobID, noFollow)
	}

	projectName, err := projectctx.ExplicitResolver{CommandPath: "ovek logs"}.Resolve(positionals)
	if err != nil {
		return err
	}

	return cmd.runRuntime(ctx, brainClient, projectName, noFollow)
}

func (cmd *logsCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek logs <project> [--no-follow]\n  ovek logs --job <jobID> [--no-follow]\n")
}

func (cmd *logsCommand) runRuntime(ctx context.Context, brainClient *client.Client, projectName string, noFollow bool) error {
	if noFollow {
		logs, err := brainClient.GetRuntimeLogs(ctx, projectName)
		if err != nil {
			return err
		}
		_, _ = cmd.stdout.Write(logs)
		return nil
	}

	stream, err := brainClient.StreamRuntimeLogs(ctx, projectName)
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := client.ReadSSEData(stream, func(line string) error {
		_, writeErr := fmt.Fprintln(cmd.stdout, line)
		return writeErr
	}); err != nil {
		return fmt.Errorf("stream runtime logs: %w", err)
	}

	return nil
}

func (cmd *logsCommand) runJob(ctx context.Context, brainClient *client.Client, jobID string, noFollow bool) error {
	if noFollow {
		logs, err := brainClient.GetJobLogs(ctx, jobID)
		if err != nil {
			return err
		}
		_, _ = cmd.stdout.Write(logs)
		return nil
	}

	stream, err := brainClient.StreamJobLogs(ctx, jobID)
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := client.ReadSSEData(stream, func(line string) error {
		_, writeErr := fmt.Fprintln(cmd.stdout, line)
		return writeErr
	}); err != nil {
		return fmt.Errorf("stream job logs: %w", err)
	}

	return nil
}

func parseLogsArgs(args []string) (string, bool, []string, error) {
	parsed, err := chomp.New("ovek logs").
		String("job").
		Bool("no-follow").
		Positionals(0, 1, "project").
		Parse(args)
	if err != nil {
		return "", false, nil, normalizeChompError(err)
	}
	return parsed.String("job"), parsed.Bool("no-follow"), parsed.Positionals(), nil
}
