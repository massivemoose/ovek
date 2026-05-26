package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/chomp"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
)

type registryCommand struct {
	stdin   io.Reader
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newRegistryCommand(stdin io.Reader, stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &registryCommand{
		stdin:   stdin,
		stdout:  stdout,
		config:  store,
		prompts: prompts,
	}
}

func (cmd *registryCommand) Name() string { return "registry" }

func (cmd *registryCommand) Summary() string { return "Manage private capsule registry credentials" }

func (cmd *registryCommand) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return command.ErrUsage
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return command.ErrUsage
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	switch args[0] {
	case "login":
		return cmd.runLogin(ctx, brainClient, args[1:])
	case "list":
		return cmd.runList(ctx, brainClient, args[1:])
	case "rm":
		return cmd.runRemove(ctx, brainClient, args[1:])
	default:
		return fmt.Errorf("unknown registry command %q", args[0])
	}
}

func (cmd *registryCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek registry login <host> --username <user> [--password-stdin]\n  ovek registry list\n  ovek registry rm <host>\n")
}

func (cmd *registryCommand) runLogin(ctx context.Context, brainClient *client.Client, args []string) error {
	loginArgs, err := parseRegistryLoginArgs(args)
	if err != nil {
		return err
	}

	password, err := cmd.registryPassword(loginArgs.passwordStdin)
	if err != nil {
		return err
	}

	credential, err := runReauthMutation(ctx, brainClient, cmd.prompts, func() (brainapi.RegistryCredential, error) {
		return brainClient.UpsertRegistryCredential(ctx, loginArgs.host, brainapi.UpsertRegistryCredentialRequest{
			Username: loginArgs.username,
			Password: password,
		})
	})
	if err != nil {
		return err
	}

	output.WriteSuccess(cmd.stdout, fmt.Sprintf("Saved registry credentials for %s", credential.Host))
	output.WriteKeyValues(cmd.stdout, [][2]string{{"Username", credential.Username}})
	return nil
}

func (cmd *registryCommand) runList(ctx context.Context, brainClient *client.Client, args []string) error {
	flagSet := flag.NewFlagSet("ovek registry list", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek registry list does not accept positional arguments")
	}

	credentials, err := brainClient.ListRegistryCredentials(ctx)
	if err != nil {
		return err
	}
	if len(credentials) == 0 {
		output.WriteEmpty(cmd.stdout, "No registry credentials configured.")
		return nil
	}

	rows := make([][]string, 0, len(credentials))
	for _, credential := range credentials {
		rows = append(rows, []string{credential.Host, credential.Username, valueOrDash(credential.UpdatedAt)})
	}
	output.WriteTable(cmd.stdout, []string{"Host", "Username", "Updated"}, rows)
	return nil
}

func (cmd *registryCommand) runRemove(ctx context.Context, brainClient *client.Client, args []string) error {
	host, err := parseRegistryRemoveArgs(args)
	if err != nil {
		return err
	}

	_, err = runReauthMutation(ctx, brainClient, cmd.prompts, func() (struct{}, error) {
		return struct{}{}, brainClient.DeleteRegistryCredential(ctx, host)
	})
	if err != nil {
		return err
	}

	output.WriteSuccess(cmd.stdout, fmt.Sprintf("Removed registry credentials for %s", host))
	return nil
}

func (cmd *registryCommand) registryPassword(passwordStdin bool) (string, error) {
	if passwordStdin {
		passwordBytes, err := io.ReadAll(cmd.stdin)
		if err != nil {
			return "", fmt.Errorf("read registry password from stdin: %w", err)
		}
		password := strings.TrimRight(string(passwordBytes), "\r\n")
		if password == "" {
			return "", fmt.Errorf("registry password is required")
		}
		return password, nil
	}

	password, err := cmd.prompts.PromptPassword("Registry password: ")
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", fmt.Errorf("registry password is required")
	}
	return password, nil
}

type registryLoginArgs struct {
	host          string
	username      string
	passwordStdin bool
}

func parseRegistryLoginArgs(args []string) (registryLoginArgs, error) {
	parsed, err := ovekCommand("registry", "login").
		String("username", chomp.Required()).
		Bool("password-stdin").
		Positionals(1, 1, "host").
		Parse(args)
	if err != nil {
		return registryLoginArgs{}, normalizeChompError(err)
	}
	return registryLoginArgs{
		host:          parsed.Positional(0),
		username:      parsed.String("username"),
		passwordStdin: parsed.Bool("password-stdin"),
	}, nil
}

func parseRegistryRemoveArgs(args []string) (string, error) {
	parsed, err := ovekCommand("registry", "rm").
		Positionals(1, 1, "host").
		Parse(args)
	if err != nil {
		return "", normalizeChompError(err)
	}
	return parsed.Positional(0), nil
}
