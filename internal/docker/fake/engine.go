// Package fake provides an in-memory docker.Engine for unit tests. Tracks
// containers, networks, volumes, and images as plain maps; exposes hooks
// to inject failures or simulate slow operations.
package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
)

// Engine is the test double for docker.Engine. All methods are safe for
// concurrent use; the same Engine can be shared across goroutines.
type Engine struct {
	mu sync.Mutex

	Containers    map[string]*Container
	Networks      map[string]*Network
	Volumes       map[string]*Volume
	PulledImages  map[string]bool
	Provider      docker.ProviderInfo
	PingErr       error
	PullErr       error
	WaitReadyErr  error
	WaitReadyHook func(ctx context.Context, name string) error
	OnPull        func(ref string, cb func(docker.PullProgress))
	NextID        int

	// Recordings useful for asserting in tests.
	Pulls        []string
	StartedNames []string
	StoppedNames []string
	RemovedNames []string
	Created      []docker.ContainerSpec
	WaitedFor    []string
	ChownVolumes []ChownEvent
	ChownPaths   []ChownEvent

	// One-shot helpers for §7 of DATABASE.md (volume marker).
	OneShotCalls  []OneShotCall
	OneShotScript []OneShotScripted

	// Disk-usage scripting for the health package's disk-low check.
	DiskReport docker.DiskReport
	DiskErr    error
}

// Container is the fake's record of a created container.
type Container struct {
	ID      string
	Name    string
	Spec    docker.ContainerSpec
	Running bool
	Healthy bool
	Logs    string
}

// Network is the fake's record of a created network.
type Network struct {
	ID     string
	Name   string
	Labels map[string]string
}

// Volume is the fake's record of a created volume.
type Volume struct {
	Name   string
	Labels map[string]string
}

// ChownEvent records one ChownVolume / ChownPath call.
type ChownEvent struct {
	Target string
	UID    int
	GID    int
}

// New returns a fresh fake engine.
func New() *Engine {
	return &Engine{
		Containers:   map[string]*Container{},
		Networks:     map[string]*Network{},
		Volumes:      map[string]*Volume{},
		PulledImages: map[string]bool{},
		Provider: docker.ProviderInfo{
			Name:          "fake",
			OSType:        "linux",
			Architecture:  "amd64",
			ServerVersion: "fake-1.0",
		},
	}
}

func (e *Engine) nextID() string {
	e.NextID++
	return fmt.Sprintf("id-%d", e.NextID)
}

// EnsureContainer records the spec and creates an entry if absent. If a
// container with the same name exists, it's recreated only if the
// ConfigHash differs — matching the production engine's behaviour.
func (e *Engine) EnsureContainer(_ context.Context, spec docker.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if existing, ok := e.Containers[spec.Name]; ok {
		if existing.Spec.ConfigHash() == spec.ConfigHash() {
			return existing.ID, nil
		}
		delete(e.Containers, spec.Name)
	}

	id := e.nextID()
	e.Containers[spec.Name] = &Container{
		ID:   id,
		Name: spec.Name,
		Spec: spec,
	}
	e.Created = append(e.Created, spec)
	return id, nil
}

func (e *Engine) StartContainer(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.Containers[name]
	if !ok {
		return fmt.Errorf("%w: %s", docker.ErrNotFound, name)
	}
	c.Running = true
	c.Healthy = true
	e.StartedNames = append(e.StartedNames, name)
	return nil
}

func (e *Engine) StopContainer(_ context.Context, name string, _ time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.Containers[name]; ok {
		c.Running = false
	}
	e.StoppedNames = append(e.StoppedNames, name)
	return nil
}

func (e *Engine) RemoveContainer(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.Containers, name)
	e.RemovedNames = append(e.RemovedNames, name)
	return nil
}

func (e *Engine) EnsureNetwork(_ context.Context, spec docker.NetworkSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n, ok := e.Networks[spec.Name]; ok {
		return n.ID, nil
	}
	id := e.nextID()
	e.Networks[spec.Name] = &Network{ID: id, Name: spec.Name, Labels: spec.Labels}
	return id, nil
}

func (e *Engine) EnsureVolume(_ context.Context, spec docker.VolumeSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.Volumes[spec.Name]; !ok {
		e.Volumes[spec.Name] = &Volume{Name: spec.Name, Labels: spec.Labels}
	}
	return spec.Name, nil
}

func (e *Engine) PullImage(_ context.Context, ref string, onProgress func(docker.PullProgress)) error {
	e.mu.Lock()
	if e.PullErr != nil {
		err := e.PullErr
		e.mu.Unlock()
		return err
	}
	e.Pulls = append(e.Pulls, ref)
	e.PulledImages[ref] = true
	hook := e.OnPull
	e.mu.Unlock()

	if onProgress != nil {
		onProgress(docker.PullProgress{Image: ref, Status: "Already present"})
	}
	if hook != nil {
		hook(ref, onProgress)
	}
	return nil
}

func (e *Engine) WaitReady(ctx context.Context, name string, _ time.Duration) error {
	e.mu.Lock()
	e.WaitedFor = append(e.WaitedFor, name)
	hook := e.WaitReadyHook
	staticErr := e.WaitReadyErr
	e.mu.Unlock()

	if hook != nil {
		return hook(ctx, name)
	}
	if staticErr != nil {
		return staticErr
	}
	return nil
}

func (e *Engine) ContainerLogs(_ context.Context, name string, _ int) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.Containers[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", docker.ErrNotFound, name)
	}
	return c.Logs, nil
}

func (e *Engine) ChownVolume(_ context.Context, vol string, uid, gid int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ChownVolumes = append(e.ChownVolumes, ChownEvent{Target: vol, UID: uid, GID: gid})
	return nil
}

func (e *Engine) ChownPath(_ context.Context, host string, uid, gid int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ChownPaths = append(e.ChownPaths, ChownEvent{Target: host, UID: uid, GID: gid})
	return nil
}

func (e *Engine) ContainersByLabel(_ context.Context, match map[string]string) ([]docker.ContainerInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []docker.ContainerInfo
	for _, c := range e.Containers {
		if labelsMatch(c.Spec.Labels, match) {
			out = append(out, docker.ContainerInfo{
				ID:     c.ID,
				Names:  []string{c.Name},
				Image:  c.Spec.Image,
				Labels: c.Spec.Labels,
			})
		}
	}
	return out, nil
}

func (e *Engine) RemoveContainersByLabel(_ context.Context, match map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for n, c := range e.Containers {
		if labelsMatch(c.Spec.Labels, match) {
			delete(e.Containers, n)
		}
	}
	return nil
}

func (e *Engine) NetworksByLabel(_ context.Context, match map[string]string) ([]docker.NetworkInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []docker.NetworkInfo
	for _, n := range e.Networks {
		if labelsMatch(n.Labels, match) {
			out = append(out, docker.NetworkInfo{ID: n.ID, Name: n.Name, Labels: n.Labels})
		}
	}
	return out, nil
}

func (e *Engine) RemoveNetworksByLabel(_ context.Context, match map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for n, net := range e.Networks {
		if labelsMatch(net.Labels, match) {
			delete(e.Networks, n)
		}
	}
	return nil
}

func (e *Engine) ContainerExists(_ context.Context, name string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.Containers[name]
	return ok, nil
}

func (e *Engine) ContainerIsRunning(_ context.Context, name string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.Containers[name]
	if !ok {
		return false, nil
	}
	return c.Running, nil
}

func (e *Engine) ProviderInfo(_ context.Context) (docker.ProviderInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Provider, nil
}

// SetProvider replaces the cached provider info under lock. Health-check
// tests use this to drive the "what daemon are we talking to" branch
// without having to construct an engine_provider.go pipeline.
func (e *Engine) SetProvider(p docker.ProviderInfo) {
	e.mu.Lock()
	e.Provider = p
	e.mu.Unlock()
}

// SetPingErr replaces the ping error under lock. Health-check tests use
// this to simulate "Docker is down" branches without racing with the
// PingErr field directly.
func (e *Engine) SetPingErr(err error) {
	e.mu.Lock()
	e.PingErr = err
	e.mu.Unlock()
}

func (e *Engine) Ping(_ context.Context) error { return e.PingErr }

// DiskUsage returns the scripted DiskReport. Tests can set DiskReport and
// DiskErr to drive behaviour in the disk-low health check without a real
// daemon.
func (e *Engine) DiskUsage(_ context.Context) (docker.DiskReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.DiskErr != nil {
		return docker.DiskReport{}, e.DiskErr
	}
	return e.DiskReport, nil
}

// RunOneShotCapture is a recorded one-shot run. Tests pre-load
// OneShotScript with the responses to return for each call in order.
func (e *Engine) RunOneShotCapture(_ context.Context, name, image string, cmd []string, mounts []docker.OneShotMount) (docker.OneShotResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.OneShotCalls = append(e.OneShotCalls, OneShotCall{Name: name, Image: image, Cmd: copyStrings(cmd), Mounts: mounts})
	if len(e.OneShotScript) == 0 {
		return docker.OneShotResult{}, nil
	}
	r := e.OneShotScript[0]
	e.OneShotScript = e.OneShotScript[1:]
	return r.Result, r.Err
}

// OneShotCall records one RunOneShotCapture invocation.
type OneShotCall struct {
	Name   string
	Image  string
	Cmd    []string
	Mounts []docker.OneShotMount
}

// OneShotScripted is one queued response.
type OneShotScripted struct {
	Result docker.OneShotResult
	Err    error
}

func copyStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func labelsMatch(have, want map[string]string) bool {
	for k, v := range want {
		hv, ok := have[k]
		if !ok {
			return false
		}
		if v != "" && hv != v {
			return false
		}
	}
	return true
}

// Compile-time guard: Engine satisfies docker.Engine.
var _ docker.Engine = (*Engine)(nil)

// Verify that errors.Is works against fake Engine return paths in tests
// — keeps the import lively.
var _ = errors.Is
