package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/cocoa-xu/hako/internal/agent"
	"github.com/cocoa-xu/hako/internal/control"
	"github.com/cocoa-xu/hako/internal/identity"
)

func join(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("join", flag.ContinueOnError)
	server := flags.String("server", "", "receiver HTTPS origin")
	config := flags.String("config", "", "worker configuration destination")
	keyFile := flags.String("key-file", "", "private file containing the enrollment key")
	keyStdin := flags.Bool("key-stdin", false, "read the enrollment key from standard input")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("workers require macOS")
	}
	if flags.NArg() != 0 || *server == "" || *config == "" || (*keyFile != "") == *keyStdin {
		return fmt.Errorf("provide --server, --config, and exactly one of --key-file or --key-stdin")
	}
	var key string
	var err error
	if *keyStdin {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 129))
		key = strings.TrimSpace(string(data))
	} else {
		key, err = agent.ReadSecret(*keyFile)
	}
	if err != nil {
		return err
	}
	if err := agent.Join(ctx, *server, key, *config); err != nil {
		return err
	}
	fmt.Println("Worker enrolled. Configuration saved to", *config)
	return nil
}

func manage(ctx context.Context, command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	server := flags.String("server", "http://127.0.0.1:8787", "receiver origin")
	keyFile := flags.String("admin-key-file", "", "private administrator key file")
	ttl := flags.Duration("ttl", 15*time.Minute, "enrollment lifetime, between 1 minute and 24 hours")
	label := flags.String("label", "", "optional enrollment label")
	keyOut := flags.String("key-out", "", "save the new enrollment key to a private file")
	id := flags.String("id", "", "device or invitation ID")
	invitation := flags.Bool("invitation", false, "revoke an unused enrollment key instead of a machine")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *keyFile == "" {
		return fmt.Errorf("provide --admin-key-file")
	}
	key, err := agent.ReadSecret(*keyFile)
	if err != nil {
		return err
	}
	method, path := http.MethodGet, "/api/machines"
	var body any
	switch command {
	case "invite":
		if *ttl < time.Minute || *ttl > 24*time.Hour {
			return fmt.Errorf("enrollment lifetime must be between 1 minute and 24 hours")
		}
		if *keyOut != "" {
			if _, err := os.Lstat(*keyOut); !os.IsNotExist(err) {
				return fmt.Errorf("key output already exists or cannot be checked")
			}
		}
		method, path = http.MethodPost, "/api/enrollments"
		body = map[string]any{"label": *label, "ttl_seconds": int64(ttl.Seconds())}
	case "invitations":
		path = "/api/enrollments"
	case "revoke":
		if (!*invitation && !identity.Valid(*id)) || (*invitation && (len(*id) != 32 || strings.Trim(*id, "0123456789abcdef") != "")) {
			return fmt.Errorf("provide a valid --id")
		}
		method = http.MethodDelete
		path = "/api/machines/" + *id
		if *invitation {
			path = "/api/enrollments/" + *id
		}
	}
	data, err := control.Call(ctx, *server, key, method, path, body)
	if err != nil {
		return err
	}
	if command == "invite" && *keyOut != "" {
		var response map[string]json.RawMessage
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		var enrollmentKey string
		if err := json.Unmarshal(response["key"], &enrollmentKey); err != nil {
			return err
		}
		if err := agent.SavePrivate(*keyOut, []byte(enrollmentKey+"\n")); err != nil {
			return err
		}
		delete(response, "key")
		data, _ = json.Marshal(response)
	}
	if len(data) > 0 {
		fmt.Println(string(data))
	} else {
		fmt.Println("Revoked.")
	}
	return nil
}
