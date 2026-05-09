package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
)

type envCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newEnvCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &envCommand{
		stdout:  stdout,
		config:  store,
		prompts: prompts,
	}
}

func (cmd *envCommand) Name() string { return "env" }

func (cmd *envCommand) Summary() string { return "Manage project environment variables" }

func (cmd *envCommand) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return command.ErrUsage
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		if len(args) != 2 {
			return fmt.Errorf("ovek env list requires <project>")
		}
		return cmd.runList(ctx, brainClient, args[1])
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("ovek env set requires <project> and KEY=value")
		}
		name, value, ok := strings.Cut(args[2], "=")
		if !ok {
			return fmt.Errorf("ovek env set requires KEY=value")
		}
		return cmd.runSet(ctx, brainClient, args[1], name, value, false)
	case "unset":
		if len(args) != 3 {
			return fmt.Errorf("ovek env unset requires <project> and KEY")
		}
		return cmd.runUnset(ctx, brainClient, args[1], args[2])
	default:
		return fmt.Errorf("unknown env command %q", args[0])
	}
}

func (cmd *envCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek env list <project>\n  ovek env set <project> KEY=value\n  ovek env unset <project> KEY\n")
}

func (cmd *envCommand) runList(ctx context.Context, brainClient *client.Client, projectName string) error {
	entries, err := brainClient.GetProjectEnvironment(ctx, projectName)
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Environment")
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(cmd.stdout, "No environment variables configured.")
		return nil
	}

	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, []string{
			entry.Name,
			projectEnvType(entry),
			projectEnvDisplayValue(entry),
			entry.UpdatedAt,
		})
	}
	output.WriteTable(cmd.stdout, []string{"Name", "Type", "Value", "Updated"}, rows)
	return nil
}

func (cmd *envCommand) runSet(ctx context.Context, brainClient *client.Client, projectName string, name string, value string, secret bool) error {
	mutation, err := runProjectEnvironmentMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectEnvironmentMutation, error) {
		return brainClient.SetProjectEnvironment(ctx, projectName, name, brainapi.SetProjectEnvironmentRequest{
			Value:  value,
			Secret: secret,
		})
	})
	if err != nil {
		return err
	}

	writeEnvironmentUpdated(cmd.stdout, mutation.RevisionID)
	return nil
}

func (cmd *envCommand) runUnset(ctx context.Context, brainClient *client.Client, projectName string, name string) error {
	mutation, err := runProjectEnvironmentMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectEnvironmentMutation, error) {
		return brainClient.DeleteProjectEnvironment(ctx, projectName, name)
	})
	if err != nil {
		return err
	}

	writeEnvironmentUpdated(cmd.stdout, mutation.RevisionID)
	return nil
}

type secretCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newSecretCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &secretCommand{
		stdout:  stdout,
		config:  store,
		prompts: prompts,
	}
}

func (cmd *secretCommand) Name() string { return "secret" }

func (cmd *secretCommand) Summary() string { return "Manage project secrets" }

func (cmd *secretCommand) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return command.ErrUsage
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	switch args[0] {
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("ovek secret set requires <project> and KEY")
		}
		value, err := cmd.prompts.PromptPassword("Secret value: ")
		if err != nil {
			return err
		}
		mutation, err := runProjectEnvironmentMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectEnvironmentMutation, error) {
			return brainClient.SetProjectEnvironment(ctx, args[1], args[2], brainapi.SetProjectEnvironmentRequest{
				Value:  value,
				Secret: true,
			})
		})
		if err != nil {
			return err
		}
		writeEnvironmentUpdated(cmd.stdout, mutation.RevisionID)
		return nil
	case "unset":
		if len(args) != 3 {
			return fmt.Errorf("ovek secret unset requires <project> and KEY")
		}
		mutation, err := runProjectEnvironmentMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectEnvironmentMutation, error) {
			return brainClient.DeleteProjectEnvironment(ctx, args[1], args[2])
		})
		if err != nil {
			return err
		}
		writeEnvironmentUpdated(cmd.stdout, mutation.RevisionID)
		return nil
	default:
		return fmt.Errorf("unknown secret command %q", args[0])
	}
}

func (cmd *secretCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek secret set <project> KEY\n  ovek secret unset <project> KEY\n")
}

func runProjectEnvironmentMutation(ctx context.Context, brainClient *client.Client, prompts prompter, mutate func() (brainapi.ProjectEnvironmentMutation, error)) (brainapi.ProjectEnvironmentMutation, error) {
	mutation, err := mutate()
	if err == nil {
		return mutation, nil
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "reauth_required" {
		return brainapi.ProjectEnvironmentMutation{}, err
	}

	password, promptErr := prompts.PromptPassword("Password: ")
	if promptErr != nil {
		return brainapi.ProjectEnvironmentMutation{}, promptErr
	}
	if password == "" {
		return brainapi.ProjectEnvironmentMutation{}, fmt.Errorf("password is required")
	}

	reauthResponse, reauthErr := brainClient.Reauth(ctx, password)
	if reauthErr != nil {
		return brainapi.ProjectEnvironmentMutation{}, fmt.Errorf("reauthenticate: %w", reauthErr)
	}
	brainClient.SetReauthToken(reauthResponse.ReauthToken)

	return mutate()
}

func writeEnvironmentUpdated(w io.Writer, revisionID string) {
	_, _ = fmt.Fprintln(w, "Environment updated. Run 'ovek run <project> <capsule-ref>' to apply changes.")
	output.WriteKeyValues(w, [][2]string{{"Revision", valueOrDash(revisionID)}})
}

func projectEnvType(entry brainapi.ProjectEnvironmentEntry) string {
	if entry.Secret {
		return "secret"
	}
	return "env"
}

func projectEnvDisplayValue(entry brainapi.ProjectEnvironmentEntry) string {
	if entry.Secret {
		return "****"
	}
	if entry.Value == nil {
		return "-"
	}
	return *entry.Value
}
