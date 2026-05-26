package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/chomp"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
)

const defaultDatabaseTunnelListen = "127.0.0.1:8090"

type dbCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newDBCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &dbCommand{
		stdout:  stdout,
		config:  store,
		prompts: prompts,
	}
}

func (cmd *dbCommand) Name() string { return "db" }

func (cmd *dbCommand) Summary() string { return "Manage a project's database sidecar" }

func (cmd *dbCommand) Run(ctx context.Context, args []string) error {
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
	case "init":
		return cmd.runInit(ctx, brainClient, args[1:])
	case "status":
		return cmd.runStatus(ctx, brainClient, args[1:])
	case "tunnel":
		return cmd.runTunnel(ctx, brainClient, args[1:])
	default:
		return fmt.Errorf("unknown db command %q", args[0])
	}
}

func (cmd *dbCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek db init <project> [--email <email>] [--app-secrets]\n  ovek db status <project>\n  ovek db tunnel <project> [--listen 127.0.0.1:8090]\n")
}

func (cmd *dbCommand) runInit(ctx context.Context, brainClient *client.Client, args []string) error {
	projectName, email, appSecrets, err := parseDatabaseInitArgs(args)
	if err != nil {
		return err
	}

	status, err := runDatabaseInitMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectPocketBaseStatus, error) {
		return brainClient.InitProjectPocketBase(ctx, projectName, brainapi.InitProjectPocketBaseRequest{
			Email:      email,
			AppSecrets: appSecrets,
		})
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(cmd.stdout, "Database initialized.")
	writeDatabaseStatus(cmd.stdout, status)
	if appSecrets {
		_, _ = fmt.Fprintln(cmd.stdout, "Environment updated. Run 'ovek run <project> <capsule-ref>' to apply changes.")
	}
	return nil
}

func (cmd *dbCommand) runStatus(ctx context.Context, brainClient *client.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("ovek db status requires <project>")
	}

	status, err := brainClient.GetProjectPocketBase(ctx, args[0])
	if err != nil {
		return err
	}

	writeDatabaseStatus(cmd.stdout, status)
	return nil
}

func (cmd *dbCommand) runTunnel(ctx context.Context, brainClient *client.Client, args []string) error {
	projectName, listenAddress, err := parseDatabaseTunnelArgs(args)
	if err != nil {
		return err
	}
	if !isLoopbackListenAddress(listenAddress) {
		return fmt.Errorf("ovek db tunnel --listen must be a loopback host:port")
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen for Database tunnel: %w", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response, err := brainClient.ProxyProjectPocketBase(r.Context(), projectName, r)
			if err != nil {
				http.Error(w, "Database tunnel request failed", http.StatusBadGateway)
				return
			}
			defer response.Body.Close()

			copyHTTPHeaders(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
		}),
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	dashboardURL := databaseDashboardURL(listener.Addr().String())
	_, _ = fmt.Fprintf(cmd.stdout, "Database tunnel: %s\n", dashboardURL)
	_, _ = fmt.Fprintln(cmd.stdout, "Press Ctrl-C to stop.")

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func parseDatabaseInitArgs(args []string) (string, string, bool, error) {
	parsed, err := chomp.New("ovek db init").
		String("email").
		Bool("app-secrets").
		Positionals(1, 1, "project").
		Parse(args)
	if err != nil {
		return "", "", false, normalizeChompError(err)
	}
	return parsed.Positional(0), parsed.String("email"), parsed.Bool("app-secrets"), nil
}

func parseDatabaseTunnelArgs(args []string) (string, string, error) {
	listenAddress := defaultDatabaseTunnelListen
	parsed, err := chomp.New("ovek db tunnel").
		String("listen").
		Positionals(1, 1, "project").
		Parse(args)
	if err != nil {
		return "", "", normalizeChompError(err)
	}
	if parsed.String("listen") != "" {
		listenAddress = parsed.String("listen")
	}
	return parsed.Positional(0), listenAddress, nil
}

func runDatabaseInitMutation(ctx context.Context, brainClient *client.Client, prompts prompter, mutate func() (brainapi.ProjectPocketBaseStatus, error)) (brainapi.ProjectPocketBaseStatus, error) {
	status, err := mutate()
	if err == nil {
		return status, nil
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "reauth_required" {
		return brainapi.ProjectPocketBaseStatus{}, err
	}

	password, promptErr := prompts.PromptPassword("Password: ")
	if promptErr != nil {
		return brainapi.ProjectPocketBaseStatus{}, promptErr
	}
	if password == "" {
		return brainapi.ProjectPocketBaseStatus{}, fmt.Errorf("password is required")
	}

	reauthResponse, reauthErr := brainClient.Reauth(ctx, password)
	if reauthErr != nil {
		return brainapi.ProjectPocketBaseStatus{}, fmt.Errorf("reauthenticate: %w", reauthErr)
	}
	brainClient.SetReauthToken(reauthResponse.ReauthToken)

	return mutate()
}

func writeDatabaseStatus(w io.Writer, status brainapi.ProjectPocketBaseStatus) {
	output.WriteSection(w, "Database")
	pairs := [][2]string{
		{"Project", status.ProjectName},
		{"Provider", "PocketBase"},
		{"Container", status.ContainerName},
		{"Running", boolText(status.Running)},
		{"Initialized", boolText(status.Initialized)},
		{"Superuser Email", pointerOrDash(status.SuperuserEmail)},
		{"App Secrets", boolText(status.AppSecretsConfigured)},
		{"Updated", valueOrDash(status.UpdatedAt)},
	}
	if status.AppSecretsRevisionID != "" {
		pairs = append(pairs, [2]string{"Revision", status.AppSecretsRevisionID})
	}
	output.WriteKeyValues(w, pairs)
}

func isLoopbackListenAddress(address string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || strings.TrimSpace(port) == "" || strings.TrimSpace(host) == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func databaseDashboardURL(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "http://" + address + "/_/"
	}
	if host == "::1" {
		host = "[::1]"
	}
	return "http://" + host + ":" + port + "/_/"
}

func copyHTTPHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func pointerOrDash(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	return *value
}
