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
)

const workflowStatusLimit = 20

type workflowCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newWorkflowCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &workflowCommand{stdout: stdout, config: store, prompts: prompts}
}

func (cmd *workflowCommand) Name() string { return "workflow" }

func (cmd *workflowCommand) Summary() string { return "Manage async workflows" }

func (cmd *workflowCommand) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return command.ErrUsage
	}
	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	switch args[0] {
	case "set":
		return cmd.runSet(ctx, brainClient, args[1:])
	case "run":
		return cmd.runRun(ctx, brainClient, args[1:])
	case "list":
		return cmd.runList(ctx, brainClient, args[1:])
	case "status":
		return cmd.runStatus(ctx, brainClient, args[1:])
	case "logs":
		return cmd.runLogs(ctx, brainClient, args[1:])
	case "rm":
		return cmd.runRemove(ctx, brainClient, args[1:])
	default:
		return fmt.Errorf("unknown workflow subcommand %q", args[0])
	}
}

func (cmd *workflowCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek workflow set <project> <name> --image <capsule-ref> [--schedule '<cron>']\n  ovek workflow run <project> <name>\n  ovek workflow list <project>\n  ovek workflow status <project> [<name>]\n  ovek workflow logs <project> <run-id> [--follow|--no-follow]\n  ovek workflow rm <project> <name>\n")
}

func (cmd *workflowCommand) runSet(ctx context.Context, brainClient *client.Client, args []string) error {
	setArgs, err := parseWorkflowSetArgs(args)
	if err != nil {
		return err
	}

	workflow, err := runReauthMutation(ctx, brainClient, cmd.prompts, func() (brainapi.Workflow, error) {
		return brainClient.UpsertWorkflow(ctx, setArgs.projectName, setArgs.workflowName, brainapi.UpsertWorkflowRequest{
			ImageRef: setArgs.imageRef,
			Schedule: setArgs.schedule,
		})
	})
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Workflow")
	output.WriteKeyValues(cmd.stdout, workflowDefinitionPairs(workflow))
	return nil
}

type workflowSetArgs struct {
	projectName  string
	workflowName string
	imageRef     string
	schedule     string
}

func parseWorkflowSetArgs(args []string) (workflowSetArgs, error) {
	var parsed workflowSetArgs
	var positionals []string
	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		switch {
		case arg == "-h" || arg == "--help":
			return workflowSetArgs{}, command.ErrUsage
		case arg == "--image":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return workflowSetArgs{}, fmt.Errorf("ovek workflow set --image requires a value")
			}
			parsed.imageRef = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "--image="):
			parsed.imageRef = strings.TrimSpace(strings.TrimPrefix(arg, "--image="))
			if parsed.imageRef == "" {
				return workflowSetArgs{}, fmt.Errorf("ovek workflow set --image requires a value")
			}
		case arg == "--schedule":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return workflowSetArgs{}, fmt.Errorf("ovek workflow set --schedule requires a value")
			}
			parsed.schedule = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "--schedule="):
			parsed.schedule = strings.TrimSpace(strings.TrimPrefix(arg, "--schedule="))
			if parsed.schedule == "" {
				return workflowSetArgs{}, fmt.Errorf("ovek workflow set --schedule requires a value")
			}
		case strings.HasPrefix(arg, "-"):
			return workflowSetArgs{}, fmt.Errorf("unknown workflow set flag %q", arg)
		case arg == "":
			return workflowSetArgs{}, fmt.Errorf("ovek workflow set requires <project> and <name>")
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) != 2 {
		return workflowSetArgs{}, fmt.Errorf("ovek workflow set requires <project> and <name>")
	}
	if strings.TrimSpace(parsed.imageRef) == "" {
		return workflowSetArgs{}, fmt.Errorf("ovek workflow set requires --image")
	}
	parsed.projectName = positionals[0]
	parsed.workflowName = positionals[1]
	return parsed, nil
}

func (cmd *workflowCommand) runRun(ctx context.Context, brainClient *client.Client, args []string) error {
	flagSet := flag.NewFlagSet("ovek workflow run", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 2 {
		return fmt.Errorf("ovek workflow run requires <project> and <name>")
	}
	projectName := flagSet.Arg(0)
	run, err := brainClient.CreateWorkflowRun(ctx, projectName, flagSet.Arg(1), brainapi.CreateWorkflowRunRequest{TriggerType: brainapi.WorkflowRunTriggerManual})
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Workflow Run")
	output.WriteKeyValues(cmd.stdout, workflowRunPairs(run))
	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Logs")
	if err := streamWorkflowLogs(ctx, cmd.stdout, brainClient, projectName, run.ID); err != nil {
		return err
	}

	finalRun, err := brainClient.GetWorkflowRun(ctx, projectName, run.ID)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.stdout)
	output.WriteSection(cmd.stdout, "Result")
	output.WriteKeyValues(cmd.stdout, workflowRunPairs(finalRun))
	if finalRun.ErrorMessage != "" {
		_, _ = fmt.Fprintf(cmd.stdout, "Error   %s\n", finalRun.ErrorMessage)
	}
	if finalRun.Status != brainapi.WorkflowRunStatusSucceeded {
		if finalRun.ErrorMessage != "" {
			return fmt.Errorf("workflow run failed: %s", finalRun.ErrorMessage)
		}
		return fmt.Errorf("workflow run finished with status %s", finalRun.Status)
	}
	return nil
}

func (cmd *workflowCommand) runList(ctx context.Context, brainClient *client.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("ovek workflow list requires <project>")
	}
	workflows, err := brainClient.ListWorkflows(ctx, args[0])
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(workflows))
	for _, workflow := range workflows {
		rows = append(rows, []string{workflow.Name, workflow.SourceImageRef, valueOrDash(workflow.Schedule), fmt.Sprintf("%t", workflow.Enabled)})
	}
	output.WriteTable(cmd.stdout, []string{"Name", "Image", "Schedule", "Enabled"}, rows)
	return nil
}

func (cmd *workflowCommand) runStatus(ctx context.Context, brainClient *client.Client, args []string) error {
	if len(args) != 1 && len(args) != 2 {
		return fmt.Errorf("ovek workflow status requires <project> [<name>]")
	}
	projectName := args[0]
	if len(args) == 2 {
		workflows, err := brainClient.ListWorkflows(ctx, projectName)
		if err != nil {
			return err
		}
		for _, workflow := range workflows {
			if workflow.Name == args[1] {
				output.WriteSection(cmd.stdout, "Workflow")
				output.WriteKeyValues(cmd.stdout, workflowDefinitionPairs(workflow))
				break
			}
		}
	}
	runs, err := brainClient.ListWorkflowRuns(ctx, projectName, workflowStatusLimit)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(runs))
	for _, run := range runs {
		if len(args) == 2 && run.WorkflowName != args[1] {
			continue
		}
		rows = append(rows, []string{run.ID, run.WorkflowName, run.TriggerType, run.Status, valueOrDash(run.CreatedAt), valueOrDash(run.FinishedAt)})
	}
	if len(args) == 2 {
		_, _ = fmt.Fprintln(cmd.stdout)
	}
	output.WriteSection(cmd.stdout, "Runs")
	output.WriteTable(cmd.stdout, []string{"Run", "Workflow", "Trigger", "Status", "Created", "Finished"}, rows)
	return nil
}

func (cmd *workflowCommand) runLogs(ctx context.Context, brainClient *client.Client, args []string) error {
	follow, positionals, err := parseWorkflowLogsArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) != 2 {
		return fmt.Errorf("ovek workflow logs requires <project> and <run-id>")
	}
	if !follow {
		logs, err := brainClient.GetWorkflowRunLogs(ctx, positionals[0], positionals[1])
		if err != nil {
			return err
		}
		_, _ = cmd.stdout.Write(logs)
		return nil
	}
	return streamWorkflowLogs(ctx, cmd.stdout, brainClient, positionals[0], positionals[1])
}

func (cmd *workflowCommand) runRemove(ctx context.Context, brainClient *client.Client, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("ovek workflow rm requires <project> and <name>")
	}
	_, err := runReauthMutation(ctx, brainClient, cmd.prompts, func() (struct{}, error) {
		return struct{}{}, brainClient.DeleteWorkflow(ctx, args[0], args[1])
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.stdout, "Workflow %s removed from %s.\n", args[1], args[0])
	return nil
}

func streamWorkflowLogs(ctx context.Context, stdout io.Writer, brainClient *client.Client, projectName string, runID string) error {
	stream, err := brainClient.StreamWorkflowRunLogs(ctx, projectName, runID)
	if err != nil {
		return err
	}
	defer stream.Close()

	if err := client.ReadSSEData(stream, func(line string) error {
		_, writeErr := fmt.Fprintln(stdout, line)
		return writeErr
	}); err != nil {
		return fmt.Errorf("stream workflow logs: %w", err)
	}
	return nil
}

func parseWorkflowLogsArgs(args []string) (bool, []string, error) {
	follow := true
	var positionals []string
	for _, arg := range args {
		switch arg {
		case "--follow":
			follow = true
		case "--no-follow":
			follow = false
		default:
			positionals = append(positionals, arg)
		}
	}
	return follow, positionals, nil
}

func workflowDefinitionPairs(workflow brainapi.Workflow) [][2]string {
	return [][2]string{
		{"Project", workflow.ProjectName},
		{"Name", workflow.Name},
		{"Image", workflow.SourceImageRef},
		{"Digest", valueOrDash(workflow.ResolvedRepoDigest)},
		{"Runtime Image", valueOrDash(workflow.RuntimeImageID)},
		{"Schedule", valueOrDash(workflow.Schedule)},
		{"Enabled", fmt.Sprintf("%t", workflow.Enabled)},
	}
}

func workflowRunPairs(run brainapi.WorkflowRun) [][2]string {
	exitCode := "-"
	if run.ExitCode != nil {
		exitCode = fmt.Sprintf("%d", *run.ExitCode)
	}
	return [][2]string{
		{"Project", run.ProjectName},
		{"Workflow", run.WorkflowName},
		{"Run", run.ID},
		{"Trigger", run.TriggerType},
		{"Status", run.Status},
		{"Exit Code", exitCode},
		{"Created", valueOrDash(run.CreatedAt)},
		{"Finished", valueOrDash(run.FinishedAt)},
	}
}
