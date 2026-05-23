package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
)

func newAuthCommand(stdout io.Writer, stderr io.Writer, store *config.Store, prompts prompter) command.Command {
	return command.NewRouter(
		"auth",
		"Manage local Brain authentication.",
		&authBootstrapCommand{stdout: stdout, stderr: stderr, config: store, prompts: prompts},
		&authLoginCommand{stdout: stdout, stderr: stderr, config: store},
		&authStatusCommand{stdout: stdout, config: store},
		&authLogoutCommand{stdout: stdout, config: store},
		&authProfilesCommand{stdout: stdout, config: store},
		&authUseCommand{stdout: stdout, config: store},
		&authKeysCommand{stdout: stdout, config: store},
		newAuthKeyCommand(stdout, store, prompts),
		&authPasswordCommand{stdout: stdout, config: store, prompts: prompts},
	)
}

type authBootstrapCommand struct {
	stdout  io.Writer
	stderr  io.Writer
	config  *config.Store
	prompts prompter
}

func (cmd *authBootstrapCommand) Name() string { return "bootstrap" }

func (cmd *authBootstrapCommand) Summary() string {
	return "Create the first Brain admin and save its API key"
}

func (cmd *authBootstrapCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth bootstrap", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	host := flagSet.String("host", config.DefaultHost, "Brain base URL")
	profileName := flagSet.String("profile", config.DefaultProfile, "Local profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth bootstrap does not accept positional arguments")
	}

	username, err := cmd.prompts.Prompt("Username: ")
	if err != nil {
		return err
	}
	password, err := cmd.prompts.PromptPassword("Password: ")
	if err != nil {
		return err
	}
	passwordConfirm, err := cmd.prompts.PromptPassword("Confirm password: ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return fmt.Errorf("username and password are required")
	}
	if password != passwordConfirm {
		return fmt.Errorf("passwords do not match")
	}

	brainClient, err := client.New(*host, "")
	if err != nil {
		return err
	}

	response, err := brainClient.Bootstrap(ctx, brainapi.BootstrapAuthRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return err
	}

	if err := cmd.config.SaveProfile(*profileName, config.Profile{
		Host:   *host,
		APIKey: response.APIKey,
	}, true); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Bootstrapped admin %s\n", response.Username)
	_, _ = fmt.Fprintf(cmd.stdout, "Profile: %s\n", strings.TrimSpace(*profileName))
	_, _ = fmt.Fprintf(cmd.stdout, "Host: %s\n", strings.TrimRight(strings.TrimSpace(*host), "/"))
	return nil
}

func (cmd *authBootstrapCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth bootstrap --profile <name> [--host <url>]\n")
}

type authLoginCommand struct {
	stdout io.Writer
	stderr io.Writer
	config *config.Store
}

func (cmd *authLoginCommand) Name() string { return "login" }

func (cmd *authLoginCommand) Summary() string { return "Verify and save local Brain auth" }

func (cmd *authLoginCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth login", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	host := flagSet.String("host", config.DefaultHost, "Brain base URL")
	apiKey := flagSet.String("api-key", "", "Brain API key")
	profileName := flagSet.String("profile", config.DefaultProfile, "Local profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth login does not accept positional arguments")
	}
	if strings.TrimSpace(*apiKey) == "" {
		return fmt.Errorf("ovek auth login requires --api-key")
	}

	brainClient, err := client.New(*host, *apiKey)
	if err != nil {
		return err
	}
	if err := brainClient.Ping(ctx); err != nil {
		return fmt.Errorf("verify credentials: %w", err)
	}

	if err := cmd.config.SaveProfile(*profileName, config.Profile{
		Host:   *host,
		APIKey: *apiKey,
	}, true); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Saved credentials for %s\n", strings.TrimSpace(*profileName))
	_, _ = fmt.Fprintf(cmd.stdout, "Host: %s\n", strings.TrimRight(strings.TrimSpace(*host), "/"))
	return nil
}

func (cmd *authLoginCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth login --profile <name> [--host <url>] --api-key <key>\n")
}

type authStatusCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authStatusCommand) Name() string { return "status" }

func (cmd *authStatusCommand) Summary() string { return "Show local auth configuration status" }

func (cmd *authStatusCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth status", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	profileName := flagSet.String("profile", "", "Profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth status does not accept positional arguments")
	}

	configPath, err := cmd.config.Path()
	if err != nil {
		return err
	}

	cfg, err := cmd.config.Load()
	if errors.Is(err, config.ErrNotFound) {
		_, _ = fmt.Fprintf(cmd.stdout, "Authenticated: no\n")
		_, _ = fmt.Fprintf(cmd.stdout, "Config: %s\n", configPath)
		return nil
	}
	if err != nil {
		return err
	}

	resolvedProfile := strings.TrimSpace(*profileName)
	if resolvedProfile == "" {
		resolvedProfile = cfg.ActiveProfile
	}
	profile, ok := cfg.Profiles[resolvedProfile]
	if !ok {
		return config.ErrProfileNotFound
	}

	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Authenticated", "yes"},
		{"Profile", resolvedProfile},
		{"Active Profile", cfg.ActiveProfile},
		{"Host", profile.Host},
		{"Config", configPath},
	})
	return nil
}

func (cmd *authStatusCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth status [--profile <name>]\n")
}

type authLogoutCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authLogoutCommand) Name() string { return "logout" }

func (cmd *authLogoutCommand) Summary() string { return "Clear local Brain auth" }

func (cmd *authLogoutCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth logout", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	profileName := flagSet.String("profile", "", "Profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth logout does not accept positional arguments")
	}

	if err := cmd.config.RemoveProfile(*profileName); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Cleared local auth.\n")
	return nil
}

func (cmd *authLogoutCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth logout [--profile <name>]\n")
}

type authProfilesCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authProfilesCommand) Name() string { return "profiles" }

func (cmd *authProfilesCommand) Summary() string { return "List configured local profiles" }

func (cmd *authProfilesCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth profiles", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth profiles does not accept positional arguments")
	}

	cfg, err := cmd.config.Load()
	if errors.Is(err, config.ErrNotFound) {
		_, _ = fmt.Fprintln(cmd.stdout, "No profiles configured.")
		return nil
	}
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Profiles")
	rows := make([][]string, 0, len(cfg.Profiles))
	for _, name := range orderedProfileNames(cfg) {
		profile := cfg.Profiles[name]
		active := ""
		if name == cfg.ActiveProfile {
			active = "*"
		}
		rows = append(rows, []string{active, name, profile.Host})
	}
	output.WriteTable(cmd.stdout, []string{"", "Profile", "Host"}, rows)
	return nil
}

func (cmd *authProfilesCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth profiles\n")
}

type authUseCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authUseCommand) Name() string { return "use" }

func (cmd *authUseCommand) Summary() string { return "Set the active local profile" }

func (cmd *authUseCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth use", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 1 {
		return fmt.Errorf("ovek auth use requires exactly one <profile> argument")
	}

	profileName := strings.TrimSpace(flagSet.Arg(0))
	if err := cmd.config.SetActiveProfile(profileName); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Active profile: %s\n", profileName)
	return nil
}

func (cmd *authUseCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth use <profile>\n")
}

type authKeysCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authKeysCommand) Name() string { return "keys" }

func (cmd *authKeysCommand) Summary() string { return "List Brain API keys" }

func (cmd *authKeysCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth keys", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth keys does not accept positional arguments")
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}
	keys, err := brainClient.ListAPIKeys(ctx)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		_, _ = fmt.Fprintln(cmd.stdout, "No API keys found.")
		return nil
	}

	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{
			key.ID,
			key.Label,
			valueOrDash(key.CreatedAt),
			valueOrDash(key.LastUsedAt),
			valueOrDash(key.RevokedAt),
		})
	}
	output.WriteTable(cmd.stdout, []string{"ID", "Label", "Created", "Last Used", "Revoked"}, rows)
	return nil
}

func (cmd *authKeysCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth keys\n")
}

func newAuthKeyCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return command.NewRouter(
		"key",
		"Manage Brain API keys.",
		&authKeyCreateCommand{stdout: stdout, config: store, prompts: prompts},
		&authKeyRemoveCommand{stdout: stdout, config: store, prompts: prompts},
	)
}

type authKeyCreateCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func (cmd *authKeyCreateCommand) Name() string { return "create" }

func (cmd *authKeyCreateCommand) Summary() string { return "Create a Brain API key" }

func (cmd *authKeyCreateCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth key create", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	label := flagSet.String("label", "", "API key label")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth key create does not accept positional arguments")
	}
	if strings.TrimSpace(*label) == "" {
		return fmt.Errorf("ovek auth key create requires --label <label>")
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}
	response, err := runReauthMutation(ctx, brainClient, cmd.prompts, func() (brainapi.CreateAPIKeyResponse, error) {
		return brainClient.CreateAPIKey(ctx, brainapi.CreateAPIKeyRequest{Label: strings.TrimSpace(*label)})
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Created API key %s\n", response.Label)
	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"ID", response.ID},
		{"API Key", response.APIKey},
	})
	_, _ = fmt.Fprintln(cmd.stdout, "This key is shown once.")
	return nil
}

func (cmd *authKeyCreateCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth key create --label <label>\n")
}

type authKeyRemoveCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func (cmd *authKeyRemoveCommand) Name() string { return "rm" }

func (cmd *authKeyRemoveCommand) Summary() string { return "Revoke a Brain API key" }

func (cmd *authKeyRemoveCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth key rm", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 1 || strings.TrimSpace(flagSet.Arg(0)) == "" {
		return fmt.Errorf("ovek auth key rm requires <key-id>")
	}
	keyID := strings.TrimSpace(flagSet.Arg(0))

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}
	_, err = runReauthMutation(ctx, brainClient, cmd.prompts, func() (struct{}, error) {
		return struct{}{}, brainClient.RevokeAPIKey(ctx, keyID)
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Revoked API key %s\n", keyID)
	return nil
}

func (cmd *authKeyRemoveCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth key rm <key-id>\n")
}

type authPasswordCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func (cmd *authPasswordCommand) Name() string { return "password" }

func (cmd *authPasswordCommand) Summary() string { return "Change the Brain owner password" }

func (cmd *authPasswordCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("ovek auth password", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("ovek auth password does not accept positional arguments")
	}

	currentPassword, err := cmd.prompts.PromptPassword("Current password: ")
	if err != nil {
		return err
	}
	newPassword, err := cmd.prompts.PromptPassword("New password: ")
	if err != nil {
		return err
	}
	confirmPassword, err := cmd.prompts.PromptPassword("Confirm new password: ")
	if err != nil {
		return err
	}
	if currentPassword == "" || newPassword == "" {
		return fmt.Errorf("current password and new password are required")
	}
	if newPassword != confirmPassword {
		return fmt.Errorf("passwords do not match")
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}
	request := brainapi.ChangePasswordRequest{
		CurrentPassword: currentPassword,
		NewPassword:     newPassword,
	}
	if err := runPasswordChangeMutation(ctx, brainClient, currentPassword, func() error {
		return brainClient.ChangePassword(ctx, request)
	}); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(cmd.stdout, "Password changed.")
	return nil
}

func (cmd *authPasswordCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek auth password\n")
}

func runPasswordChangeMutation(ctx context.Context, brainClient *client.Client, currentPassword string, mutate func() error) error {
	if err := mutate(); err != nil {
		var apiErr *client.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "reauth_required" {
			return err
		}

		reauthResponse, reauthErr := brainClient.Reauth(ctx, currentPassword)
		if reauthErr != nil {
			return fmt.Errorf("reauthenticate: %w", reauthErr)
		}
		brainClient.SetReauthToken(reauthResponse.ReauthToken)
		return mutate()
	}

	return nil
}

func orderedProfileNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
