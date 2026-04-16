package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/massivemoose/alces/internal/brainapi"
	"github.com/massivemoose/alces/internal/cli/client"
	"github.com/massivemoose/alces/internal/cli/command"
	"github.com/massivemoose/alces/internal/cli/config"
	"github.com/massivemoose/alces/internal/cli/output"
)

type deployCommand struct {
	stdout io.Writer
	config *config.Store
}

func newDeployCommand(stdout io.Writer, store *config.Store) command.Command {
	return &deployCommand{
		stdout: stdout,
		config: store,
	}
}

func (cmd *deployCommand) Name() string { return "deploy" }

func (cmd *deployCommand) Summary() string { return "Create and follow deployments" }

func (cmd *deployCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces deploy", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 2 {
		return fmt.Errorf("alces deploy requires <project> and <repoURL>")
	}

	projectName := flagSet.Arg(0)
	repoURL := flagSet.Arg(1)

	brainClient, err := loadConfiguredClient(cmd.config)
	if err != nil {
		return err
	}

	job, err := brainClient.CreateDeployment(ctx, projectName, brainapi.CreateDeploymentRequest{RepoURL: repoURL})
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Deployment")
	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Project", projectName},
		{"Job", job.ID},
		{"Repo", repoURL},
		{"Status", job.Status},
	})

	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Build Logs")
	if err := cmd.followJobLogs(ctx, brainClient, job.ID); err != nil {
		return err
	}

	finalJob, err := brainClient.GetJob(ctx, job.ID)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Result")
	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Job", finalJob.ID},
		{"Status", finalJob.Status},
		{"Image", valueOrDash(finalJob.ImageRef)},
		{"Finished", valueOrDash(finalJob.FinishedAt)},
	})
	if finalJob.ErrorMessage != "" {
		_, _ = fmt.Fprintf(cmd.stdout, "Error   %s\n", finalJob.ErrorMessage)
	}

	if finalJob.Status != "succeeded" {
		if finalJob.ErrorMessage != "" {
			return fmt.Errorf("deployment failed: %s", finalJob.ErrorMessage)
		}
		return fmt.Errorf("deployment finished with status %s", finalJob.Status)
	}

	project, err := brainClient.GetProject(ctx, projectName)
	if err != nil {
		return err
	}
	runtimeView, hasRuntime, err := fetchRuntimeView(ctx, brainClient, projectName)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Project")
	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Name", project.Name},
		{"Status", project.Status},
		{"Current Deployment", stringOrDash(project.CurrentDeploymentID)},
	})
	if hasRuntime {
		_, _ = fmt.Fprintf(cmd.stdout, "Runtime  %s\n", runtimeAppSummary(runtimeView.App))
	}

	return nil
}

func (cmd *deployCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces deploy <project> <repoURL>\n")
}

func (cmd *deployCommand) followJobLogs(ctx context.Context, brainClient *client.Client, jobID string) error {
	stream, err := brainClient.StreamJobLogs(ctx, jobID)
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := client.ReadSSEData(stream, func(line string) error {
		_, writeErr := fmt.Fprintln(cmd.stdout, line)
		return writeErr
	}); err != nil {
		return fmt.Errorf("stream deploy logs: %w", err)
	}

	return nil
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
