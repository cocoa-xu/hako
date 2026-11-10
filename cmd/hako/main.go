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

	"hako/internal/agent"
	"hako/internal/collector"
	"hako/internal/server"
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
		return fmt.Errorf("usage: hako <serve|join|agent|invite|invitations|revoke|machines|init|inspect|version> [options]")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Println("Usage: hako <serve|join|agent|invite|invitations|revoke|machines|init|inspect|version> [options]\nUse hako <command> --help for command options.")
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
	case "init":
		flags := flag.NewFlagSet("init", flag.ContinueOnError)
		directory := flags.String("data-dir", ".local", "receiver state directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		s, err := server.New(*directory)
		if err != nil {
			return err
		}
		defer s.Close()
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
		s, err := server.NewWithOrigin(*directory, *publicURL)
		if err != nil {
			return err
		}
		defer s.Close()
		return s.ListenAndServe(ctx, addresses)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
