package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cocoa-xu/hako/internal/agent"
	"github.com/cocoa-xu/hako/internal/collector"
	"github.com/cocoa-xu/hako/internal/server"
)

var version = "0.1.0-dev"
var commit = "unknown"
var buildDate = "unknown"

type listenAddresses []string

func (addresses *listenAddresses) String() string { return strings.Join(*addresses, ", ") }

func (addresses *listenAddresses) Set(value string) error {
	*addresses = append(*addresses, value)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		slog.Error("hako", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hako <serve|join|agent|invite|invitations|revoke|machines|init|import|inspect|version> [options]")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Println("Usage: hako <serve|join|agent|invite|invitations|revoke|machines|init|import|inspect|version> [options]\nUse hako <command> --help for command options.")
		return nil
	case "version":
		fmt.Printf("hako %s (%s, %s)\n", version, commit, buildDate)
		return nil
	case "inspect":
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("the collector requires macOS")
		}
		if len(args) != 1 {
			return fmt.Errorf("usage: hako inspect")
		}
		c := collector.New(version)
		hardware := c.Inventory(ctx)
		snapshot := c.Sample(ctx, "local-inspection")
		snapshot.Hardware = &hardware
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(snapshot)
	case "join", "enroll":
		return join(ctx, args[1:])
	case "invite", "invitations", "revoke", "machines":
		return manage(ctx, args[0], args[1:])
	case "init", "import":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		databaseFile := flags.String("database-url-file", "", "private file containing the PostgreSQL connection URL")
		importDirectory := flags.String("from-dir", "", "exported registry directory (import only)")
		directory := flags.String("data-dir", ".local", "receiver state directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		databaseURL, err := databaseConnection(*databaseFile)
		if err != nil {
			return err
		}
		s, err := server.New(*directory, databaseURL)
		if err != nil {
			return err
		}
		defer s.Close()
		if args[0] == "import" {
			if *importDirectory == "" {
				return fmt.Errorf("provide --from-dir")
			}
			if err := s.ImportLegacy(*importDirectory); err != nil {
				return err
			}
			fmt.Println("Imported device identities and state into PostgreSQL.")
		} else if *importDirectory != "" {
			return fmt.Errorf("--from-dir requires the import command")
		}
		absolute, err := filepath.Abs(filepath.Join(*directory, "admin.key"))
		if err != nil {
			return err
		}
		fmt.Println(absolute)
		return nil
	case "agent":
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("the collector requires macOS")
		}
		flags := flag.NewFlagSet("agent", flag.ContinueOnError)
		configPath := flags.String("config", "", "agent credential file")
		interval := flags.Duration("interval", 2*time.Second, "sampling interval")
		once := flags.Bool("once", false, "collect and send one report")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		data, err := agent.ReadSecret(*configPath)
		if err != nil {
			return err
		}
		var config agent.Config
		if err := json.Unmarshal([]byte(data), &config); err != nil {
			return fmt.Errorf("invalid agent credential file")
		}
		a, err := agent.New(config, collector.New(version))
		if err != nil {
			return err
		}
		if *once {
			return a.Step(ctx)
		}
		err = a.Run(ctx, *interval)
		if errors.Is(err, agent.ErrUnauthorized) {
			slog.Error("Worker stopped: credential revoked or rejected. Enroll again before restarting.")
			return nil
		}
		return err
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		databaseFile := flags.String("database-url-file", "", "private file containing the PostgreSQL connection URL")
		directory := flags.String("data-dir", ".local", "receiver state directory")
		publicURL := flags.String("public-url", "", "external HTTPS origin when behind a reverse proxy")
		var addresses listenAddresses
		flags.Var(&addresses, "listen", "HTTP listen address (repeatable; defaults to 127.0.0.1:8787)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(addresses) == 0 {
			addresses = append(addresses, "127.0.0.1:8787")
		}
		databaseURL, err := databaseConnection(*databaseFile)
		if err != nil {
			return err
		}
		s, err := server.NewWithOrigin(*directory, databaseURL, *publicURL)
		if err != nil {
			return err
		}
		defer s.Close()
		return s.ListenAndServe(ctx, addresses)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func databaseConnection(path string) (string, error) {
	if path != "" {
		return agent.ReadSecret(path)
	}
	return os.Getenv("HAKO_DATABASE_URL"), nil
}
