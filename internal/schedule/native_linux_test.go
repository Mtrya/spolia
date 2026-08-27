//go:build linux

package schedule

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSystemdDefinitionSeparatesTheExecutableFromArguments(t *testing.T) {
	t.Parallel()
	manager, err := New("spolia-test")
	if err != nil {
		t.Fatal(err)
	}
	service, timer, err := manager.renderSystemd(Definition{Executable: "/tmp/spolia path/%build", LocalTime: "09:05"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(service), `ExecStart="/tmp/spolia path/%%build" sync --if-due --quiet`) {
		t.Fatalf("service = %s", service)
	}
	if !strings.Contains(string(timer), "OnCalendar=*-*-* 09:05:00") || !strings.Contains(string(timer), "Persistent=true") {
		t.Fatalf("timer = %s", timer)
	}
}

func TestNativeSystemdLifecycle(t *testing.T) {
	if os.Getenv("SPOLIA_NATIVE_SCHEDULE_TEST") != "1" {
		t.Skip("set SPOLIA_NATIVE_SCHEDULE_TEST=1 to exercise the real user scheduler")
	}
	executable, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	executable, err = exec.LookPath(executable)
	if err != nil {
		t.Fatal(err)
	}
	identifier := fmt.Sprintf("io.github.mtrya.spolia.test.%d.%d", os.Getpid(), time.Now().UnixNano())
	manager, err := New(identifier)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defer func() {
		if _, err := manager.Remove(ctx); err != nil {
			t.Errorf("cleanup scheduler: %v", err)
		}
	}()
	first := Definition{Executable: executable, LocalTime: "09:05"}
	inspection, err := manager.Install(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Installed || !inspection.Managed || !inspection.Enabled || !inspection.Matches {
		t.Fatalf("inspection = %#v", inspection)
	}
	if output, err := schedulerCommand(ctx, "systemctl", "--user", "start", identifier+".service"); err != nil {
		t.Fatalf("trigger service: %s", commandFailure(output, err))
	}
	updated := Definition{Executable: executable, LocalTime: "09:06"}
	inspection, err = manager.Install(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Matches {
		t.Fatalf("updated inspection = %#v", inspection)
	}
	removed, err := manager.Remove(ctx)
	if err != nil || !removed {
		t.Fatalf("removed=%t err=%v", removed, err)
	}
	inspection, err = manager.Inspect(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Installed {
		t.Fatalf("scheduler remains: %#v", inspection)
	}
}
