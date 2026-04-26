package sitesteps

import (
	"context"
	"errors"
	"testing"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/docker/fake"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/types"
)

func testSite() *types.Site {
	return &types.Site{
		ID:           "id1",
		Name:         "demo",
		Slug:         "demo",
		Domain:       "demo.localhost",
		FilesDir:     "/tmp/demo",
		PHPVersion:   "8.2",
		MySQLVersion: "8.0",
		RedisVersion: "7",
		WebServer:    "nginx",
		DBPassword:   "p",
	}
}

func TestEnsureNetworkStep_Apply(t *testing.T) {
	eng := fake.New()
	site := testSite()

	step := &EnsureNetworkStep{Engine: eng, Site: site}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := eng.Networks["locorum-demo"]; !ok {
		t.Errorf("network not created")
	}
}

func TestPullImagesStep_PullsAllSpecsOnce(t *testing.T) {
	eng := fake.New()
	site := testSite()
	specs := []docker.ContainerSpec{
		{Name: "a", Image: "nginx:1"},
		{Name: "b", Image: "nginx:1"}, // duplicate; should pull only once
		{Name: "c", Image: "mysql:8"},
	}

	step := &PullImagesStep{Engine: eng, Site: site, Specs: specs}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Pull dedupe — 2 unique images.
	if len(eng.Pulls) != 2 {
		t.Errorf("Pulls = %d, want 2 (deduped); got %v", len(eng.Pulls), eng.Pulls)
	}
}

func TestCreateContainersStep_CreatesAndStarts(t *testing.T) {
	eng := fake.New()
	specs := []docker.ContainerSpec{
		{Name: "x", Image: "nginx:1"},
		{Name: "y", Image: "mysql:8"},
	}
	step := &CreateContainersStep{Engine: eng, Specs: specs}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, name := range []string{"x", "y"} {
		c, ok := eng.Containers[name]
		if !ok {
			t.Errorf("container %q not created", name)
			continue
		}
		if !c.Running {
			t.Errorf("container %q not started", name)
		}
	}
}

func TestCreateContainersStep_RollbackRemovesAll(t *testing.T) {
	eng := fake.New()
	specs := []docker.ContainerSpec{{Name: "x", Image: "nginx:1"}}
	step := &CreateContainersStep{Engine: eng, Specs: specs}
	_ = step.Apply(context.Background())
	if err := step.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, ok := eng.Containers["x"]; ok {
		t.Errorf("rollback did not remove container")
	}
}

func TestPlanRollbackOnFailure(t *testing.T) {
	eng := fake.New()
	site := testSite()
	specs := []docker.ContainerSpec{{Name: "demo-web", Image: "nginx:1"}}

	plan := orch.Plan{
		Name: "test",
		Steps: []orch.Step{
			&EnsureNetworkStep{Engine: eng, Site: site},
			&CreateContainersStep{Engine: eng, Specs: specs},
			// Force failure to trigger rollback.
			&FuncStep{
				Label: "fail-here",
				Do:    func(_ context.Context) error { return errors.New("boom") },
			},
		},
	}
	res := orch.Run(context.Background(), plan, orch.Callbacks{})
	if !res.RolledBack {
		t.Errorf("RolledBack = false, want true")
	}
	if res.FinalError == nil {
		t.Errorf("expected FinalError")
	}
	// Network was rolled back.
	if _, ok := eng.Networks["locorum-demo"]; ok {
		t.Errorf("network still present after rollback")
	}
	// Containers rolled back.
	if _, ok := eng.Containers["demo-web"]; ok {
		t.Errorf("container still present after rollback")
	}
}

func TestWaitReadyStep_PerContainerTimeouts(t *testing.T) {
	eng := fake.New()
	step := &WaitReadyStep{
		Engine:     eng,
		Containers: []string{"a", "b"},
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(eng.WaitedFor) != 2 {
		t.Errorf("WaitedFor = %v, want 2 entries", eng.WaitedFor)
	}
}

func TestEngineFakeImplementsInterface(t *testing.T) {
	var _ docker.Engine = fake.New()
}
