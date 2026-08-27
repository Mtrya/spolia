//go:build linux

package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Mtrya/llmloot/internal/atomicfile"
)

const managedSystemdMarker = "# Managed by llmloot.\n"

func (manager Manager) Kind() string {
	return "systemd_user_timer"
}

func (manager Manager) Artifacts() ([]string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user configuration directory: %w", err)
	}
	unitRoot := filepath.Join(root, "systemd", "user")
	return []string{
		filepath.Join(unitRoot, manager.identifier+".service"),
		filepath.Join(unitRoot, manager.identifier+".timer"),
	}, nil
}

func (manager Manager) Install(ctx context.Context, definition Definition) (Inspection, error) {
	if err := ValidateDefinition(definition); err != nil {
		return Inspection{}, err
	}
	artifacts, err := manager.Artifacts()
	if err != nil {
		return Inspection{}, err
	}
	for _, path := range artifacts {
		contents, readErr := os.ReadFile(path)
		if readErr == nil && !bytes.HasPrefix(contents, []byte(managedSystemdMarker)) {
			return Inspection{}, fmt.Errorf("scheduler artifact %s already exists and is not managed by llmloot", path)
		}
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return Inspection{}, fmt.Errorf("inspect scheduler artifact %s: %w", path, readErr)
		}
	}
	service, timer, err := manager.renderSystemd(definition)
	if err != nil {
		return Inspection{}, err
	}
	if err := atomicfile.Write(artifacts[0], service, 0o644); err != nil {
		return Inspection{}, fmt.Errorf("write systemd service: %w", err)
	}
	if err := atomicfile.Write(artifacts[1], timer, 0o644); err != nil {
		return Inspection{}, fmt.Errorf("write systemd timer: %w", err)
	}
	if output, err := schedulerCommand(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return Inspection{}, fmt.Errorf("reload systemd user manager: %s", commandFailure(output, err))
	}
	if output, err := schedulerCommand(ctx, "systemctl", "--user", "enable", "--now", manager.identifier+".timer"); err != nil {
		return Inspection{}, fmt.Errorf("enable systemd user timer: %s", commandFailure(output, err))
	}
	inspection, err := manager.Inspect(ctx, definition)
	if err != nil {
		return Inspection{}, err
	}
	if !inspection.Enabled || !inspection.Matches {
		return inspection, fmt.Errorf("systemd user timer did not reach the expected enabled state: %s", inspection.Detail)
	}
	return inspection, nil
}

func (manager Manager) Inspect(ctx context.Context, definition Definition) (Inspection, error) {
	if err := ValidateDefinition(definition); err != nil {
		return Inspection{}, err
	}
	artifacts, err := manager.Artifacts()
	if err != nil {
		return Inspection{}, err
	}
	inspection := Inspection{Kind: manager.Kind(), Identifier: manager.identifier, Artifacts: artifacts, Status: "not_installed"}
	service, timer, err := manager.renderSystemd(definition)
	if err != nil {
		return Inspection{}, err
	}
	expected := [][]byte{service, timer}
	allExist := true
	allManaged := true
	allMatch := true
	for index, path := range artifacts {
		contents, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			allExist = false
			allManaged = false
			allMatch = false
			continue
		}
		if readErr != nil {
			return Inspection{}, fmt.Errorf("inspect scheduler artifact %s: %w", path, readErr)
		}
		inspection.Installed = true
		if !bytes.HasPrefix(contents, []byte(managedSystemdMarker)) {
			allManaged = false
		}
		if !bytes.Equal(contents, expected[index]) {
			allMatch = false
		}
	}
	inspection.Managed = allExist && allManaged
	inspection.Matches = allExist && allMatch
	if !inspection.Installed {
		return inspection, nil
	}
	if !allExist {
		inspection.Status = "incomplete"
		inspection.Detail = "one of the managed systemd unit files is missing"
		return inspection, nil
	}
	if !inspection.Managed {
		inspection.Status = "unmanaged"
		inspection.Detail = "the systemd unit files do not carry the llmloot ownership marker"
		return inspection, nil
	}
	enabledOutput, enabledErr := schedulerCommand(ctx, "systemctl", "--user", "is-enabled", manager.identifier+".timer")
	inspection.Enabled = enabledErr == nil && strings.TrimSpace(enabledOutput) == "enabled"
	if !inspection.Enabled {
		inspection.Status = "disabled"
		inspection.Detail = commandFailure(enabledOutput, enabledErr)
		return inspection, nil
	}
	activeOutput, activeErr := schedulerCommand(ctx, "systemctl", "--user", "is-active", manager.identifier+".timer")
	if activeErr != nil || strings.TrimSpace(activeOutput) != "active" {
		inspection.Status = "inactive"
		inspection.Detail = commandFailure(activeOutput, activeErr)
		return inspection, nil
	}
	inspection.Status = "enabled"
	if !inspection.Matches {
		inspection.Status = "changed"
		inspection.Detail = "the systemd unit definition differs from the configured schedule"
	}
	return inspection, nil
}

func (manager Manager) Remove(ctx context.Context) (bool, error) {
	artifacts, err := manager.Artifacts()
	if err != nil {
		return false, err
	}
	existed := false
	for _, path := range artifacts {
		contents, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return false, fmt.Errorf("inspect scheduler artifact %s: %w", path, readErr)
		}
		existed = true
		if !bytes.HasPrefix(contents, []byte(managedSystemdMarker)) {
			return false, fmt.Errorf("refusing to remove unmanaged scheduler artifact %s", path)
		}
	}
	if output, commandErr := schedulerCommand(ctx, "systemctl", "--user", "disable", "--now", manager.identifier+".timer"); commandErr != nil {
		if !strings.Contains(output, "not loaded") && !strings.Contains(output, "does not exist") && !strings.Contains(output, "not found") {
			return false, fmt.Errorf("disable systemd user timer: %s", commandFailure(output, commandErr))
		}
	} else {
		existed = true
	}
	if !existed {
		return false, nil
	}
	for _, path := range artifacts {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("remove scheduler artifact %s: %w", path, err)
		}
	}
	if output, commandErr := schedulerCommand(ctx, "systemctl", "--user", "daemon-reload"); commandErr != nil {
		return false, fmt.Errorf("reload systemd user manager: %s", commandFailure(output, commandErr))
	}
	return true, nil
}

func (manager Manager) renderSystemd(definition Definition) ([]byte, []byte, error) {
	executable, err := systemdQuote(definition.Executable)
	if err != nil {
		return nil, nil, err
	}
	service := fmt.Sprintf("%s[Unit]\nDescription=Refresh llmloot model opportunities\n\n[Service]\nType=oneshot\nExecStart=%s sync --if-due --quiet\n", managedSystemdMarker, executable)
	timer := fmt.Sprintf("%s[Unit]\nDescription=Daily llmloot model refresh\n\n[Timer]\nOnCalendar=*-*-* %s:00\nPersistent=true\nUnit=%s.service\n\n[Install]\nWantedBy=timers.target\n", managedSystemdMarker, definition.LocalTime, manager.identifier)
	return []byte(service), []byte(timer), nil
}

func schedulerCommand(ctx context.Context, name string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func commandFailure(output string, err error) string {
	if output != "" {
		return output
	}
	if err != nil {
		return err.Error()
	}
	return "unexpected scheduler state"
}
