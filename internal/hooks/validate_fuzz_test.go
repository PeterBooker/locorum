package hooks

import (
	"testing"
)

func FuzzHookValidate(f *testing.F) {
	seeds := []struct {
		taskType, command, service, runAs string
		event                             string
	}{
		{string(TaskExec), "echo hi", "php", "", string(PostStart)},
		{string(TaskExec), "echo hi", "php", "1000:1000", string(PreStart)},
		{string(TaskExec), "echo hi", "", "", string(PostStart)},
		{string(TaskExecHost), "echo hi", "", "", string(PostStop)},
		{string(TaskWPCLI), "core install", "", "", string(PostStart)},
		{"unknown", "echo hi", "php", "", string(PostStart)},
		{string(TaskExec), "", "php", "", string(PostStart)},
		{string(TaskExec), "echo hi", "ftp", "", string(PostStart)},
		{string(TaskExec), "echo hi", "php", "", "bogus-event"},
		{string(TaskExec), "echo hi\x00with-NUL", "php", "", string(PostStart)},
	}
	for _, s := range seeds {
		f.Add(s.taskType, s.command, s.service, s.runAs, s.event)
	}
	f.Fuzz(func(t *testing.T, taskType, command, service, runAs, event string) {
		h := Hook{
			TaskType:  TaskType(taskType),
			Command:   command,
			Service:   service,
			RunAsUser: runAs,
			Event:     Event(event),
			Enabled:   true,
		}
		err1 := h.Validate()
		err2 := h.Validate()
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic: %v vs %v", err1, err2)
		}
	})
}
