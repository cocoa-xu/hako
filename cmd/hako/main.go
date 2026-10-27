package main

import (
	"context"
	"encoding/json"
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

type listenAddresses []string

func (addresses *listenAddresses) String() string { return strings.Join(*addresses, ", ") }

func (addresses *listenAddresses) Set(value string) error {
	*addresses = append(*addresses, value)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("hako", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hako <inspect|enroll|agent|serve|version> [options]")
	}
	switch args[0] {
	case "version":
		fmt.Println(version)
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
	case "enroll":
		flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
		directory := flags.String("data-dir", ".local", "receiver state directory")
		endpoint := flags.String("server", "http://127.0.0.1:8787", "receiver origin")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		path, err := server.Enroll(*directory, *endpoint)
		if err != nil {
			return err
		}
		absolute, err := filepath.Abs(path)
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
		interval := flags.Duration("interval", 30*time.Second, "delay between reports")
		once := flags.Bool("once", false, "collect and send one report")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		data, err := os.ReadFile(*configPath)
		if err != nil {
			return err
		}
		var config agent.Config
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("invalid agent credential file")
		}
		a, err := agent.New(config, collector.New(version))
		if err != nil {
			return err
		}
		if *once {
			return a.Step(ctx)
		}
		return a.Run(ctx, *interval)
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		directory := flags.String("data-dir", ".local", "receiver state directory")
		var addresses listenAddresses
		flags.Var(&addresses, "listen", "HTTP listen address (repeatable; defaults to 127.0.0.1:8787)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(addresses) == 0 {
			addresses = append(addresses, "127.0.0.1:8787")
		}
		s, err := server.New(*directory)
		if err != nil {
			return err
		}
		return s.ListenAndServe(ctx, addresses)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
