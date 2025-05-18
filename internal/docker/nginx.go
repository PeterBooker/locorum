package docker

import (
	"bytes"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
)

func (d *Docker) TestGlobalNginxConfig() error {
	// 1) Create the exec instance
	execConfig := container.ExecOptions{
		Cmd:          []string{"nginx", "-t", "-c", "/etc/nginx/nginx.conf"},
		AttachStdout: true,
		AttachStderr: true,
	}
	execIDResp, err := d.cli.ContainerExecCreate(d.ctx, "locorum-global-webserver", execConfig)
	if err != nil {
		return fmt.Errorf("creating exec instance: %w", err)
	}

	// 2) Attach to capture the output
	attachResp, err := d.cli.ContainerExecAttach(d.ctx, execIDResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attaching to exec instance: %w", err)
	}
	defer attachResp.Close()

	// 3) Read the combined stdout+stderr
	var outputBuf bytes.Buffer
	if _, err := io.Copy(&outputBuf, attachResp.Reader); err != nil {
		return fmt.Errorf("reading nginx test output: %w", err)
	}
	output := outputBuf.String()

	// 4) Inspect to get the exit code
	inspectResp, err := d.cli.ContainerExecInspect(d.ctx, execIDResp.ID)
	if err != nil {
		return fmt.Errorf("inspecting exec result: %w", err)
	}

	// 5) Return error if nginx -t failed, or print success otherwise
	if inspectResp.ExitCode != 0 {
		return fmt.Errorf("nginx config test failed (exit %d):\n%s", inspectResp.ExitCode, output)
	}

	fmt.Printf("nginx config OK:\n%s\n", output)

	return nil
}

func (d *Docker) ReloadGlobalNginx() error {
	// 1) Create the exec instance for "nginx -s reload"
	execConfig := container.ExecOptions{
		Cmd:          []string{"nginx", "-s", "reload"},
		AttachStdout: true,
		AttachStderr: true,
	}
	execID, err := d.cli.ContainerExecCreate(d.ctx, "locorum-global-webserver", execConfig)
	if err != nil {
		return fmt.Errorf("creating reload exec instance: %w", err)
	}

	// 2) Start it (fire-and-forget or capture output)
	if err := d.cli.ContainerExecStart(d.ctx, execID.ID, container.ExecStartOptions{}); err != nil {
		return fmt.Errorf("starting reload exec: %w", err)
	}

	// 3) (optional) inspect exit code / logs to ensure it succeeded
	insp, err := d.cli.ContainerExecInspect(d.ctx, execID.ID)
	if err != nil {
		return fmt.Errorf("inspecting reload exec: %w", err)
	}
	if insp.ExitCode != 0 {
		return fmt.Errorf("nginx reload failed (exit %d)", insp.ExitCode)
	}

	return nil
}
