// Command runner is the entry point for the execution node project.
package main

import (
	"7link.com/runner/internal/control"
	"7link.com/runner/internal/executor"
	"7link.com/runner/internal/inspection"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// version is injected from the release tag with -ldflags.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(out, version)
		return err
	}
	if len(args) > 0 && args[0] == "service" {
		if len(args) < 2 {
			return fmt.Errorf("service requires install, uninstall or status")
		}
		flags := flag.NewFlagSet("service", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		profile := flags.String("profile", "", "Existing private profile directory")
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 || *profile == "" {
			return fmt.Errorf("invalid service arguments; --profile directory is required")
		}
		return control.Service(ctx, args[1], *profile, out)
	}
	if len(args) > 0 && args[0] == "hidden-verifier" {
		flags := flag.NewFlagSet("hidden-verifier", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		runtime := flags.String("runtime", "", "")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return fmt.Errorf("invalid hidden verifier configuration")
		}
		return executor.ServeHiddenVerifier(ctx, *runtime, os.Stdin, out)
	}
	if len(args) > 0 && args[0] == "agent-worker" {
		flags := flag.NewFlagSet("agent-worker", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		runtime := flags.String("runtime", "", "")
		model := flags.String("model", "", "")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*runtime != "CODEX" && *runtime != "CLAUDE_CODE") || *model == "" {
			return fmt.Errorf("invalid worker configuration")
		}
		return executor.ServeWorker(ctx, *runtime, *model, os.Stdin, out)
	}
	if len(args) == 0 {
		_, err := fmt.Fprintln(out, "runner: project initialized; task execution and daemon are not implemented.")
		return err
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help") {
		_, err := fmt.Fprintln(out, "Usage: runner inspect | login --profile DIR --server URL --tenant ID --client ID --name NAME | daemon --profile DIR | status --profile DIR | service install|uninstall|status --profile DIR\nDaemon runs in the foreground; service manages background startup for an enrolled profile. READ_ONLY, isolated WORKSPACE_WRITE and native ON_REQUEST item decisions are supported. Workspace commits and diffs submit a Claim for independent verification; FULL_AUTO and remote access are unavailable.")
		return err
	}
	if len(args) == 1 && args[0] == "inspect" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(inspection.Inspect(ctx))
	}
	if args[0] == "login" || args[0] == "daemon" || args[0] == "status" {
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		profile := flags.String("profile", "", "Explicit private profile directory")
		var options control.LoginOptions
		if args[0] == "login" {
			flags.StringVar(&options.Server, "server", "", "HTTPS API base URL")
			flags.StringVar(&options.TenantID, "tenant", "", "Tenant ID")
			flags.StringVar(&options.ClientID, "client", "", "IAM public device-client ID")
			flags.StringVar(&options.Name, "name", "", "Machine display name")
		}
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *profile == "" {
			return fmt.Errorf("invalid arguments; --profile directory is required; use runner --help")
		}
		store, err := control.OpenStore(*profile)
		if err != nil {
			return err
		}
		defer store.Close()
		switch args[0] {
		case "login":
			return control.Login(ctx, store, options, out)
		case "daemon":
			return control.Daemon(ctx, store, out)
		default:
			return control.Status(store, out)
		}
	}
	return fmt.Errorf("unsupported command; use runner --help")
}
