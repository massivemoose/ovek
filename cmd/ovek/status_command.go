package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
	"github.com/massivemoose/ovek/internal/cli/projectctx"
)

const statusActivityLimit = 5

type statusCommand struct {
	stdout io.Writer
	config *config.Store
}

func newStatusCommand(stdout io.Writer, store *config.Store) command.Command {
	return &statusCommand{
		stdout: stdout,
		config: store,
	}
}

func (cmd *statusCommand) Name() string { return "status" }

func (cmd *statusCommand) Summary() string { return "Inspect projects and runtime state" }

func (cmd *statusCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek status", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	if flagSet.NArg() == 0 {
		return cmd.runList(ctx, brainClient)
	}

	projectName, err := projectctx.ExplicitResolver{CommandPath: "ovek status"}.Resolve(flagSet.Args())
	if err != nil {
		return err
	}

	return cmd.runProject(ctx, brainClient, projectName)
}

func (cmd *statusCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek status\n  ovek status <project>\n")
}

func (cmd *statusCommand) runList(ctx context.Context, brainClient *client.Client) error {
	projects, err := brainClient.GetProjects(ctx, 20)
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Projects")
	if len(projects) == 0 {
		_, _ = fmt.Fprintf(cmd.stdout, "No projects found.\n")
		return nil
	}

	rows := make([][]string, 0, len(projects))
	for _, project := range projects {
		currentDeploymentID := "-"
		if project.CurrentDeploymentID != nil && strings.TrimSpace(*project.CurrentDeploymentID) != "" {
			currentDeploymentID = *project.CurrentDeploymentID
		}
		rows = append(rows, []string{
			project.Name,
			project.Status,
			currentDeploymentID,
			project.CreatedAt,
		})
	}

	output.WriteTable(cmd.stdout, []string{"Name", "Status", "Current Deployment", "Created"}, rows)
	return nil
}

func (cmd *statusCommand) runProject(ctx context.Context, brainClient *client.Client, projectName string) error {
	project, err := brainClient.GetProject(ctx, projectName)
	if err != nil {
		return err
	}

	runtimeView, hasRuntime, err := fetchRuntimeView(ctx, brainClient, projectName)
	if err != nil {
		return err
	}

	jobs, err := brainClient.GetProjectJobs(ctx, projectName, statusActivityLimit)
	if err != nil {
		return err
	}
	deployments, err := brainClient.GetProjectDeployments(ctx, projectName, statusActivityLimit)
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Project")
	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Name", project.Name},
		{"Status", project.Status},
		{"Current Deployment", stringOrDash(project.CurrentDeploymentID)},
		{"Created", project.CreatedAt},
	})

	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Runtime")
	if !hasRuntime {
		_, _ = fmt.Fprintf(cmd.stdout, "No runtime is currently available.\n")
	} else {
		runtimePairs := [][2]string{
			{"Deployment", stringOrDash(runtimeView.CurrentDeploymentID)},
			{"App", runtimeAppSummary(runtimeView.App)},
			{"PocketBase", runtimeContainerSummary(runtimeView.PocketBase)},
			{"Network", runtimeNetworkSummary(runtimeView.Network)},
		}
		output.WriteKeyValues(cmd.stdout, runtimePairs)
	}

	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Recent Jobs")
	if len(jobs) == 0 {
		_, _ = fmt.Fprintf(cmd.stdout, "No jobs found.\n")
	} else {
		rows := make([][]string, 0, len(jobs))
		for _, job := range jobs {
			rows = append(rows, []string{job.ID, job.Status, recentJobPhase(job), job.CreatedAt, job.RepoURL, recentJobError(job)})
		}
		output.WriteTable(cmd.stdout, []string{"Job", "Status", "Phase", "Created", "Repo", "Error"}, rows)
	}

	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Recent Deployments")
	if len(deployments) == 0 {
		_, _ = fmt.Fprintf(cmd.stdout, "No deployments found.\n")
		return nil
	}

	rows := make([][]string, 0, len(deployments))
	for _, deployment := range deployments {
		rows = append(rows, []string{
			deployment.ID,
			deployment.Status,
			deployment.CreatedAt,
			deployment.ImageRef,
		})
	}
	output.WriteTable(cmd.stdout, []string{"Deployment", "Status", "Created", "Image"}, rows)
	return nil
}

func fetchRuntimeView(ctx context.Context, brainClient *client.Client, projectName string) (brainapi.ProjectRuntime, bool, error) {
	runtimeView, err := brainClient.GetProjectRuntime(ctx, projectName)
	if err == nil {
		return runtimeView, true, nil
	}

	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "project_runtime_not_found" {
		return brainapi.ProjectRuntime{}, false, nil
	}

	return brainapi.ProjectRuntime{}, false, err
}

func stringOrDash(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	return *value
}

func recentJobError(job brainapi.Job) string {
	if job.Status != "failed" || strings.TrimSpace(job.ErrorMessage) == "" {
		return "-"
	}
	return job.ErrorMessage
}

func recentJobPhase(job brainapi.Job) string {
	if strings.TrimSpace(job.Phase) == "" {
		return "-"
	}
	return job.Phase
}

func runtimeAppSummary(app *brainapi.ProjectRuntimeApp) string {
	if app == nil {
		return "-"
	}
	return fmt.Sprintf("%s (%s, running=%t)", app.ContainerName, app.ImageRef, app.Running)
}

func runtimeContainerSummary(container *brainapi.ProjectRuntimeContainer) string {
	if container == nil {
		return "-"
	}
	return fmt.Sprintf("%s (running=%t)", container.ContainerName, container.Running)
}

func runtimeNetworkSummary(network *brainapi.ProjectRuntimeNetwork) string {
	if network == nil {
		return "-"
	}
	return network.Name
}
