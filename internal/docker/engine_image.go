package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/pkg/jsonmessage"
)

// imageExistsLocally reports whether the image reference is already pulled.
func (d *Docker) imageExistsLocally(ctx context.Context, ref string) (bool, error) {
	args := filters.NewArgs()
	args.Add("reference", ref)
	images, err := d.cli.ImageList(ctx, image.ListOptions{Filters: args})
	if err != nil {
		return false, err
	}
	return len(images) > 0, nil
}

// PullImage pulls ref, streaming progress to onProgress. No-op if already
// present locally. onProgress may be nil (progress is dropped). Idempotent
// and retry-wrapped for the BuildKit snapshot race.
func (d *Docker) PullImage(ctx context.Context, ref string, onProgress func(PullProgress)) error {
	if ref == "" {
		return errors.New("image reference required")
	}

	exists, err := d.imageExistsLocally(ctx, ref)
	if err != nil {
		return fmt.Errorf("listing images for %q: %w", ref, err)
	}
	if exists {
		if onProgress != nil {
			onProgress(PullProgress{Image: ref, Status: "Already present"})
		}
		return nil
	}

	return withRetryErr(ctx, "pull image "+ref, func(ctx context.Context) error {
		return d.pullOnce(ctx, ref, onProgress)
	}, nil)
}

func (d *Docker) pullOnce(ctx context.Context, ref string, onProgress func(PullProgress)) error {
	stream, err := d.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("starting image pull for %q: %w", ref, err)
	}
	defer stream.Close()

	// Aggregate per-layer progress into one rolling PullProgress callback.
	// The Docker daemon emits one JSONMessage per layer per status change;
	// we sum bytes across active layers so the GUI can show "12/18 MB" not
	// "layer xyz: 1.2/2.1 MB".
	type layer struct {
		current int64
		total   int64
	}
	layers := make(map[string]*layer)
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	emit := func(status string) {
		if onProgress == nil {
			return
		}
		var current, total int64
		for _, l := range layers {
			current += l.current
			total += l.total
		}
		onProgress(PullProgress{
			Image:      ref,
			Status:     status,
			Current:    current,
			Total:      total,
			LayerCount: len(layers),
		})
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg jsonmessage.JSONMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Non-JSON lines come from older daemon paths; ignore quietly.
			continue
		}

		if msg.Error != nil {
			return fmt.Errorf("daemon: %s", msg.Error.Message)
		}

		// Track only messages with an ID (per-layer); status-only messages
		// like "Pulling from library/nginx" carry no progress numbers.
		if msg.ID != "" && msg.Progress != nil {
			l, ok := layers[msg.ID]
			if !ok {
				l = &layer{}
				layers[msg.ID] = l
			}
			l.current = msg.Progress.Current
			l.total = msg.Progress.Total
		}

		status := strings.TrimSpace(msg.Status)
		if status != "" {
			emit(status)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading pull stream for %q: %w", ref, err)
	}

	if onProgress != nil {
		onProgress(PullProgress{Image: ref, Status: "Pull complete", LayerCount: len(layers)})
	}
	return nil
}
