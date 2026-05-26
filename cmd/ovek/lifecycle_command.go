package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/chomp"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
)

type runtimeActionCommand struct {
	name    string
	summary string
	stdout  io.Writer
	config  *config.Store
	prompts prompter
	mutate  func(context.Context, *client.Client, string) (brainapi.ProjectRuntime, error)
}

func newStopCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &runtimeActionCommand{
		name:    "stop",
		summary: "Stop a project's app runtime",
		stdout:  stdout,
		config:  store,
		prompts: prompts,
		mutate: func(ctx context.Context, brainClient *client.Client, projectName string) (brainapi.ProjectRuntime, error) {
			return brainClient.StopProjectRuntime(ctx, projectName)
		},
	}
}

func newStartCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &runtimeActionCommand{
		name:    "start",
		summary: "Start a stopped project app runtime",
		stdout:  stdout,
		config:  store,
		prompts: prompts,
		mutate: func(ctx context.Context, brainClient *client.Client, projectName string) (brainapi.ProjectRuntime, error) {
			return brainClient.StartProjectRuntime(ctx, projectName)
		},
	}
}

func newRestartCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &runtimeActionCommand{
		name:    "restart",
		summary: "Restart a project's app runtime",
		stdout:  stdout,
		config:  store,
		prompts: prompts,
		mutate: func(ctx context.Context, brainClient *client.Client, projectName string) (brainapi.ProjectRuntime, error) {
			return brainClient.RestartProjectRuntime(ctx, projectName)
		},
	}
}

func (cmd *runtimeActionCommand) Name() string { return cmd.name }

func (cmd *runtimeActionCommand) Summary() string { return cmd.summary }

func (cmd *runtimeActionCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek %s <project>\n", cmd.name)
}

func (cmd *runtimeActionCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek "+cmd.name, flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 1 {
		return fmt.Errorf("ovek %s requires <project>", cmd.name)
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	projectName := flagSet.Arg(0)
	runtimeView, err := runRuntimeMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectRuntime, error) {
		return cmd.mutate(ctx, brainClient, projectName)
	})
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, lifecycleTitle(cmd.name))
	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Project", projectName},
		{"Deployment", stringOrDash(runtimeView.CurrentDeploymentID)},
		{"App", runtimeAppSummary(runtimeView.App)},
		{"Database", runtimeContainerSummary(runtimeView.PocketBase)},
		{"Network", runtimeNetworkSummary(runtimeView.Network)},
	})
	return nil
}

type removeCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newRemoveCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &removeCommand{
		stdout:  stdout,
		config:  store,
		prompts: prompts,
	}
}

func (cmd *removeCommand) Name() string { return "rm" }

func (cmd *removeCommand) Summary() string { return "Remove a project's app runtime" }

func (cmd *removeCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  ovek rm <project> [--remove-database] [--delete-database-data]")
}

func (cmd *removeCommand) Run(ctx context.Context, args []string) error {
	projectName, removeDatabase, deleteDatabaseData, err := parseRemoveArgs(args)
	if err != nil {
		return err
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	runtimeView, err := brainClient.GetProjectRuntime(ctx, projectName)
	if err != nil {
		return err
	}
	if !hasRuntimeRemovalTargets(runtimeView, removeDatabase) {
		output.WriteSection(cmd.stdout, "Nothing To Remove")
		output.WriteKeyValues(cmd.stdout, [][2]string{
			{"Project", projectName},
			{"App Runtime", "none"},
			{"Database", noRemovalDatabaseText(runtimeView, removeDatabase)},
		})
		return nil
	}
	if removeDatabase {
		if err := confirmDatabaseRemoval(cmd.prompts, projectName, deleteDatabaseData); err != nil {
			return err
		}
	}

	if _, err := runRuntimeMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectRuntime, error) {
		return brainapi.ProjectRuntime{}, brainClient.RemoveProjectRuntime(ctx, projectName, removeDatabase, deleteDatabaseData)
	}); err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Removed")
	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Project", projectName},
		{"App Runtime", appRemovalText(runtimeView.App)},
		{"Database", databaseRemovalText(removeDatabase, deleteDatabaseData)},
	})
	return nil
}

func parseRemoveArgs(args []string) (string, bool, bool, error) {
	parsed, err := chomp.New("ovek rm").
		Bool("remove-database").
		Bool("delete-database-data").
		Positionals(1, 1, "project").
		Parse(args)
	if err != nil {
		return "", false, false, normalizeChompError(err)
	}
	projectName := parsed.Positional(0)
	removeDatabase := parsed.Bool("remove-database")
	deleteDatabaseData := parsed.Bool("delete-database-data")
	if deleteDatabaseData && !removeDatabase {
		return "", false, false, fmt.Errorf("ovek rm --delete-database-data requires --remove-database")
	}
	return projectName, removeDatabase, deleteDatabaseData, nil
}

func hasRuntimeRemovalTargets(runtimeView brainapi.ProjectRuntime, removeDatabase bool) bool {
	if runtimeView.App != nil {
		return true
	}
	if removeDatabase && (runtimeView.PocketBase != nil || runtimeView.Network != nil) {
		return true
	}
	return false
}

func confirmDatabaseRemoval(prompts prompter, projectName string, deleteDatabaseData bool) error {
	action := "remove the managed database"
	if deleteDatabaseData {
		action = "remove the managed database and delete its data"
	}
	value, err := prompts.Prompt(fmt.Sprintf("Type %q to %s: ", projectName, action))
	if err != nil {
		return err
	}
	if value != projectName {
		return fmt.Errorf("database removal not confirmed")
	}
	return nil
}

func runRuntimeMutation(ctx context.Context, brainClient *client.Client, prompts prompter, mutate func() (brainapi.ProjectRuntime, error)) (brainapi.ProjectRuntime, error) {
	runtimeView, err := mutate()
	if err == nil {
		return runtimeView, nil
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "reauth_required" {
		return brainapi.ProjectRuntime{}, err
	}

	password, promptErr := prompts.PromptPassword("Password: ")
	if promptErr != nil {
		return brainapi.ProjectRuntime{}, promptErr
	}
	if password == "" {
		return brainapi.ProjectRuntime{}, fmt.Errorf("password is required")
	}

	reauthResponse, reauthErr := brainClient.Reauth(ctx, password)
	if reauthErr != nil {
		return brainapi.ProjectRuntime{}, fmt.Errorf("reauthenticate: %w", reauthErr)
	}
	brainClient.SetReauthToken(reauthResponse.ReauthToken)

	return mutate()
}

func lifecycleTitle(action string) string {
	switch action {
	case "start":
		return "Started"
	case "stop":
		return "Stopped"
	case "restart":
		return "Restarted"
	default:
		return "Runtime"
	}
}

func databaseRemovalText(removeDatabase bool, deleteDatabaseData bool) string {
	if !removeDatabase {
		return "preserved"
	}
	if deleteDatabaseData {
		return "removed, data deleted"
	}
	return "removed, data preserved"
}

func appRemovalText(app *brainapi.ProjectRuntimeApp) string {
	if app == nil {
		return "none"
	}
	return "removed"
}

func noRemovalDatabaseText(runtimeView brainapi.ProjectRuntime, removeDatabase bool) string {
	if !removeDatabase {
		if runtimeView.PocketBase != nil {
			return "preserved"
		}
		return "none"
	}
	return "none"
}
