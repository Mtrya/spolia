//go:build windows

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

func TestWindowsTaskSeparatesCommandAndArguments(t *testing.T) {
	t.Parallel()
	manager, err := New("io.github.mtrya.spolia.test")
	if err != nil {
		t.Fatal(err)
	}
	contents, err := manager.renderTask(context.Background(), Definition{Executable: `C:\Program Files\spolia & test\spolia.exe`, LocalTime: "09:05"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, `<Command>C:\Program Files\spolia &amp; test\spolia.exe</Command>`) || !strings.Contains(text, `<Arguments>sync --if-due --quiet</Arguments>`) || !strings.Contains(text, `<StartWhenAvailable>true</StartWhenAvailable>`) {
		t.Fatalf("task XML = %s", text)
	}
}

func TestNativeWindowsTaskLifecycle(t *testing.T) {
	if os.Getenv("SPOLIA_NATIVE_SCHEDULE_TEST") != "1" {
		t.Skip("set SPOLIA_NATIVE_SCHEDULE_TEST=1 to exercise the real user scheduler")
	}
	executable, err := exec.LookPath("where.exe")
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
		queried, queryErr := schedulerCommand(ctx, "schtasks.exe", "/Query", "/TN", "\\"+identifier, "/XML")
		t.Logf("queried task XML (err=%v): %q", queryErr, queried)
		t.Fatal(err)
	}
	if !inspection.Installed || !inspection.Managed || !inspection.Enabled || !inspection.Matches {
		t.Fatalf("inspection = %#v", inspection)
	}
	if output, err := schedulerCommand(ctx, "schtasks.exe", "/Run", "/TN", "\\"+identifier); err != nil {
		t.Fatalf("trigger scheduled task: %s", commandFailure(output, err))
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
