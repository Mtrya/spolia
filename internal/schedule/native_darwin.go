//go:build darwin

package schedule

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Mtrya/llmloot/internal/atomicfile"
)

const managedLaunchdMarker = "<!-- Managed by llmloot. -->"

func (manager Manager) Kind() string {
	return "launch_agent"
}

func (manager Manager) Artifacts() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}
	return []string{filepath.Join(home, "Library", "LaunchAgents", manager.identifier+".plist")}, nil
}

func (manager Manager) Install(ctx context.Context, definition Definition) (Inspection, error) {
	if err := ValidateDefinition(definition); err != nil {
		return Inspection{}, err
	}
	artifacts, err := manager.Artifacts()
	if err != nil {
		return Inspection{}, err
	}
	existing, readErr := os.ReadFile(artifacts[0])
	if readErr == nil && !bytes.Contains(existing, []byte(managedLaunchdMarker)) {
		return Inspection{}, fmt.Errorf("scheduler artifact %s already exists and is not managed by llmloot", artifacts[0])
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return Inspection{}, fmt.Errorf("inspect scheduler artifact %s: %w", artifacts[0], readErr)
	}
	contents, err := manager.renderLaunchAgent(definition)
	if err != nil {
		return Inspection{}, err
	}
	if err := atomicfile.Write(artifacts[0], contents, 0o644); err != nil {
		return Inspection{}, fmt.Errorf("write LaunchAgent: %w", err)
	}
	domain := launchdDomain()
	_, _ = schedulerCommand(ctx, "launchctl", "bootout", domain+"/"+manager.identifier)
	if output, err := schedulerCommand(ctx, "launchctl", "bootstrap", domain, artifacts[0]); err != nil {
		return Inspection{}, fmt.Errorf("bootstrap LaunchAgent: %s", commandFailure(output, err))
	}
	if output, err := schedulerCommand(ctx, "launchctl", "enable", domain+"/"+manager.identifier); err != nil {
		return Inspection{}, fmt.Errorf("enable LaunchAgent: %s", commandFailure(output, err))
	}
	inspection, err := manager.Inspect(ctx, definition)
	if err != nil {
		return Inspection{}, err
	}
	if !inspection.Enabled || !inspection.Matches {
		return inspection, fmt.Errorf("LaunchAgent did not reach the expected enabled state: %s", inspection.Detail)
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
	contents, readErr := os.ReadFile(artifacts[0])
	if errors.Is(readErr, os.ErrNotExist) {
		return inspection, nil
	}
	if readErr != nil {
		return Inspection{}, fmt.Errorf("inspect scheduler artifact %s: %w", artifacts[0], readErr)
	}
	expected, err := manager.renderLaunchAgent(definition)
	if err != nil {
		return Inspection{}, err
	}
	inspection.Installed = true
	inspection.Managed = bytes.Contains(contents, []byte(managedLaunchdMarker))
	inspection.Matches = bytes.Equal(contents, expected)
	if !inspection.Managed {
		inspection.Status = "unmanaged"
		inspection.Detail = "the LaunchAgent does not carry the llmloot ownership marker"
		return inspection, nil
	}
	output, commandErr := schedulerCommand(ctx, "launchctl", "print", launchdDomain()+"/"+manager.identifier)
	if commandErr != nil {
		inspection.Status = "not_loaded"
		inspection.Detail = commandFailure(output, commandErr)
		return inspection, nil
	}
	inspection.Enabled = true
	inspection.Status = "enabled"
	if !inspection.Matches {
		inspection.Status = "changed"
		inspection.Detail = "the LaunchAgent definition differs from the configured schedule"
	}
	return inspection, nil
}

func (manager Manager) Remove(ctx context.Context) (bool, error) {
	artifacts, err := manager.Artifacts()
	if err != nil {
		return false, err
	}
	contents, readErr := os.ReadFile(artifacts[0])
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect scheduler artifact %s: %w", artifacts[0], readErr)
	}
	if existed && !bytes.Contains(contents, []byte(managedLaunchdMarker)) {
		return false, fmt.Errorf("refusing to remove unmanaged scheduler artifact %s", artifacts[0])
	}
	output, commandErr := schedulerCommand(ctx, "launchctl", "bootout", launchdDomain()+"/"+manager.identifier)
	if commandErr == nil {
		existed = true
	} else if !strings.Contains(output, "Could not find service") && !strings.Contains(output, "No such process") {
		return false, fmt.Errorf("bootout LaunchAgent: %s", commandFailure(output, commandErr))
	}
	if err := os.Remove(artifacts[0]); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove scheduler artifact %s: %w", artifacts[0], err)
	}
	return existed, nil
}

func (manager Manager) renderLaunchAgent(definition Definition) ([]byte, error) {
	hour, minute, err := ParseLocalTime(definition.LocalTime)
	if err != nil {
		return nil, err
	}
	identifier, err := xmlText(manager.identifier)
	if err != nil {
		return nil, err
	}
	executable, err := xmlText(definition.Executable)
	if err != nil {
		return nil, err
	}
	contents := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
%s
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>sync</string>
        <string>--if-due</string>
        <string>--quiet</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>%d</integer>
        <key>Minute</key>
        <integer>%d</integer>
    </dict>
</dict>
</plist>
`, managedLaunchdMarker, identifier, executable, hour, minute)
	return []byte(contents), nil
}

func launchdDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func xmlText(value string) (string, error) {
	var result bytes.Buffer
	if err := xml.EscapeText(&result, []byte(value)); err != nil {
		return "", err
	}
	return result.String(), nil
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
