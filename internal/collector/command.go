package collector

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type runner func(context.Context, string, ...string) ([]byte, error)

func command(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C", "LANG=C"}
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s: %w", path, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", path, strings.Join(args, " "), err)
	}
	return out, nil
}
