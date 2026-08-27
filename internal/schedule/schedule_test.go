package schedule

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentBoundaryUsesLocalCalendarDaysAcrossDST(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	afterSpringChange := time.Date(2026, 3, 8, 8, 30, 0, 0, location)
	springBoundary, err := CurrentBoundary(afterSpringChange, "09:00")
	if err != nil {
		t.Fatal(err)
	}
	if springBoundary.Day() != 7 || springBoundary.Hour() != 9 || afterSpringChange.Sub(springBoundary) != 22*time.Hour+30*time.Minute {
		t.Fatalf("spring boundary = %s, distance = %s", springBoundary, afterSpringChange.Sub(springBoundary))
	}

	afterFallChange := time.Date(2026, 11, 1, 8, 30, 0, 0, location)
	fallBoundary, err := CurrentBoundary(afterFallChange, "09:00")
	if err != nil {
		t.Fatal(err)
	}
	if fallBoundary.Day() != 31 || fallBoundary.Hour() != 9 || afterFallChange.Sub(fallBoundary) != 24*time.Hour+30*time.Minute {
		t.Fatalf("fall boundary = %s, distance = %s", fallBoundary, afterFallChange.Sub(fallBoundary))
	}
}

func TestIsDueUsesTheCurrentBoundary(t *testing.T) {
	t.Parallel()
	location := time.FixedZone("test", 8*60*60)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, location)
	boundary := time.Date(2026, 8, 27, 9, 0, 0, 0, location)

	due, actualBoundary, err := IsDue(now, "09:00", nil)
	if err != nil || !due || !actualBoundary.Equal(boundary) {
		t.Fatalf("due=%t boundary=%s err=%v", due, actualBoundary, err)
	}
	satisfied := boundary.UTC()
	due, _, err = IsDue(now, "09:00", &satisfied)
	if err != nil || due {
		t.Fatalf("due=%t err=%v", due, err)
	}
	previous := boundary.Add(-time.Nanosecond)
	due, _, err = IsDue(now, "09:00", &previous)
	if err != nil || !due {
		t.Fatalf("due=%t err=%v", due, err)
	}
}

func TestDefinitionRequiresAbsoluteExecutableAndStrictTime(t *testing.T) {
	t.Parallel()
	if err := ValidateDefinition(Definition{Executable: "llmloot", LocalTime: "09:00"}); err == nil {
		t.Fatal("relative executable was accepted")
	}
	if err := ValidateDefinition(Definition{Executable: filepath.Join(string(filepath.Separator), "bin", "llmloot"), LocalTime: "9:00"}); err == nil {
		t.Fatal("non-canonical time was accepted")
	}
}
