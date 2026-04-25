package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
)

type deployCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newDeployCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &deployCommand{
		stdout:  stdout,
		config:  store,
		prompts: prompts,
	}
}

func (cmd *deployCommand) Name() string { return "deploy" }

func (cmd *deployCommand) Summary() string { return "Create and follow deployments" }

func (cmd *deployCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek deploy", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 2 {
		return fmt.Errorf("ovek deploy requires <project> and <repoURL>")
	}

	projectName := flagSet.Arg(0)
	repoURL := flagSet.Arg(1)

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	job, err := cmd.createDeployment(ctx, brainClient, projectName, repoURL)
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
		_, _ = fmt.Fprintln(cmd.stdout)
		output.WriteSection(cmd.stdout, "Next Step")
		output.WriteKeyValues(cmd.stdout, [][2]string{
			{"Inspect Logs", fmt.Sprintf("ovek logs --job %s --no-follow", finalJob.ID)},
		})

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
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek deploy <project> <repoURL>\n")
}

func (cmd *deployCommand) createDeployment(ctx context.Context, brainClient *client.Client, projectName string, repoURL string) (brainapi.Job, error) {
	job, err := brainClient.CreateDeployment(ctx, projectName, brainapi.CreateDeploymentRequest{RepoURL: repoURL})
	if err == nil {
		return job, nil
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "reauth_required" {
		return brainapi.Job{}, err
	}

	password, promptErr := cmd.prompts.PromptPassword("Password: ")
	if promptErr != nil {
		return brainapi.Job{}, promptErr
	}
	if password == "" {
		return brainapi.Job{}, fmt.Errorf("password is required")
	}

	reauthResponse, reauthErr := brainClient.Reauth(ctx, password)
	if reauthErr != nil {
		return brainapi.Job{}, fmt.Errorf("reauthenticate: %w", reauthErr)
	}
	brainClient.SetReauthToken(reauthResponse.ReauthToken)

	return brainClient.CreateDeployment(ctx, projectName, brainapi.CreateDeploymentRequest{RepoURL: repoURL})
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
