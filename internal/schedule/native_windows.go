//go:build windows

package schedule

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const managedTaskSource = "spolia"

type taskDocument struct {
	RegistrationInfo struct {
		Source string `xml:"Source"`
		URI    string `xml:"URI"`
	} `xml:"RegistrationInfo"`
	Triggers struct {
		Calendar struct {
			StartBoundary string `xml:"StartBoundary"`
			ScheduleByDay struct {
				DaysInterval int `xml:"DaysInterval"`
			} `xml:"ScheduleByDay"`
		} `xml:"CalendarTrigger"`
	} `xml:"Triggers"`
	Settings struct {
		StartWhenAvailable bool  `xml:"StartWhenAvailable"`
		Enabled            *bool `xml:"Enabled"`
	} `xml:"Settings"`
	Actions struct {
		Exec struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

func (manager Manager) Kind() string {
	return "windows_task_scheduler"
}

func (manager Manager) Artifacts() ([]string, error) {
	return []string{"Task Scheduler \\" + manager.identifier}, nil
}

func (manager Manager) Install(ctx context.Context, definition Definition) (Inspection, error) {
	if err := ValidateDefinition(definition); err != nil {
		return Inspection{}, err
	}
	current, exists, err := manager.queryTask(ctx)
	if err != nil {
		return Inspection{}, err
	}
	if exists {
		parsed, parseErr := parseTask(current)
		if parseErr != nil {
			return Inspection{}, fmt.Errorf("parse existing scheduled task: %w", parseErr)
		}
		if !manager.isManaged(parsed) {
			return Inspection{}, fmt.Errorf("scheduled task \\%s already exists and is not managed by spolia", manager.identifier)
		}
	}
	contents, err := manager.renderTask(ctx, definition)
	if err != nil {
		return Inspection{}, err
	}
	temporary, err := os.CreateTemp("", "spolia-task-*.xml")
	if err != nil {
		return Inspection{}, fmt.Errorf("create temporary task definition: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(utf16LEWithBOM(contents)); err != nil {
		_ = temporary.Close()
		return Inspection{}, fmt.Errorf("write temporary task definition: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Inspection{}, fmt.Errorf("close temporary task definition: %w", err)
	}
	if output, err := schedulerCommand(ctx, "schtasks.exe", "/Create", "/TN", "\\"+manager.identifier, "/XML", temporaryPath, "/F"); err != nil {
		return Inspection{}, fmt.Errorf("register scheduled task: %s", commandFailure(output, err))
	}
	inspection, err := manager.Inspect(ctx, definition)
	if err != nil {
		return Inspection{}, err
	}
	if !inspection.Enabled || !inspection.Matches {
		return inspection, fmt.Errorf("scheduled task did not reach the expected enabled state: %s", inspection.Detail)
	}
	return inspection, nil
}

func (manager Manager) Inspect(ctx context.Context, definition Definition) (Inspection, error) {
	if err := ValidateDefinition(definition); err != nil {
		return Inspection{}, err
	}
	artifacts, _ := manager.Artifacts()
	inspection := Inspection{Kind: manager.Kind(), Identifier: manager.identifier, Artifacts: artifacts, Status: "not_installed"}
	contents, exists, err := manager.queryTask(ctx)
	if err != nil {
		return Inspection{}, err
	}
	if !exists {
		return inspection, nil
	}
	parsed, err := parseTask(contents)
	if err != nil {
		return Inspection{}, fmt.Errorf("parse scheduled task: %w", err)
	}
	inspection.Installed = true
	inspection.Managed = manager.isManaged(parsed)
	inspection.Enabled = taskEnabled(parsed.Settings.Enabled)
	inspection.Matches = manager.matches(parsed, definition)
	switch {
	case !inspection.Managed:
		inspection.Status = "unmanaged"
		inspection.Detail = "the scheduled task does not carry the spolia source and URI"
	case !inspection.Enabled:
		inspection.Status = "disabled"
		inspection.Detail = "the scheduled task is disabled"
	case !inspection.Matches:
		inspection.Status = "changed"
		inspection.Detail = "the scheduled task definition differs from the configured schedule"
	default:
		inspection.Status = "enabled"
	}
	return inspection, nil
}

func (manager Manager) Remove(ctx context.Context) (bool, error) {
	contents, exists, err := manager.queryTask(ctx)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	parsed, err := parseTask(contents)
	if err != nil {
		return false, fmt.Errorf("parse scheduled task: %w", err)
	}
	if !manager.isManaged(parsed) {
		return false, fmt.Errorf("refusing to remove unmanaged scheduled task \\%s", manager.identifier)
	}
	if output, err := schedulerCommand(ctx, "schtasks.exe", "/Delete", "/TN", "\\"+manager.identifier, "/F"); err != nil {
		return false, fmt.Errorf("remove scheduled task: %s", commandFailure(output, err))
	}
	return true, nil
}

func (manager Manager) renderTask(ctx context.Context, definition Definition) ([]byte, error) {
	if err := ValidateDefinition(definition); err != nil {
		return nil, err
	}
	userOutput, err := schedulerCommand(ctx, "whoami.exe")
	if err != nil {
		return nil, fmt.Errorf("resolve current Windows user: %s", commandFailure(userOutput, err))
	}
	userID, err := xmlText(strings.TrimSpace(userOutput))
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
	contents := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.3" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Source>%s</Source>
    <URI>\%s</URI>
  </RegistrationInfo>
  <Triggers>
    <CalendarTrigger>
      <StartBoundary>2000-01-01T%s:00</StartBoundary>
      <ScheduleByDay>
        <DaysInterval>1</DaysInterval>
      </ScheduleByDay>
    </CalendarTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <ExecutionTimeLimit>PT1H</ExecutionTimeLimit>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>sync --if-due --quiet</Arguments>
    </Exec>
  </Actions>
</Task>
`, managedTaskSource, identifier, definition.LocalTime, userID, executable)
	return []byte(contents), nil
}

// schtasks /Create /XML only accepts UTF-16 input; a UTF-8 file fails with
// "unable to switch the encoding" even when the declaration says UTF-8.
func utf16LEWithBOM(contents []byte) []byte {
	units := utf16.Encode([]rune(string(contents)))
	result := make([]byte, 2+len(units)*2)
	result[0] = 0xff
	result[1] = 0xfe
	for index, unit := range units {
		binary.LittleEndian.PutUint16(result[2+index*2:], unit)
	}
	return result
}

func (manager Manager) queryTask(ctx context.Context) ([]byte, bool, error) {
	command := exec.CommandContext(ctx, "schtasks.exe", "/Query", "/TN", "\\"+manager.identifier, "/XML", "/HRESULT")
	output, err := command.CombinedOutput()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := uint32(exitError.ExitCode())
			if exitCode == uint32(0x80070002) || exitCode == uint32(0x8004130f) {
				return nil, false, nil
			}
		}
		return nil, false, fmt.Errorf("query scheduled task: %s", commandFailure(strings.TrimSpace(string(output)), err))
	}
	return normalizeTaskXML(output), true, nil
}

func parseTask(contents []byte) (taskDocument, error) {
	var result taskDocument
	if err := xml.Unmarshal(normalizeTaskXML(contents), &result); err != nil {
		return taskDocument{}, err
	}
	return result, nil
}

func (manager Manager) isManaged(task taskDocument) bool {
	return task.RegistrationInfo.Source == managedTaskSource && task.RegistrationInfo.URI == "\\"+manager.identifier
}

func (manager Manager) matches(task taskDocument, definition Definition) bool {
	boundaryTime := ""
	if separator := strings.IndexByte(task.Triggers.Calendar.StartBoundary, 'T'); separator >= 0 && len(task.Triggers.Calendar.StartBoundary) >= separator+6 {
		boundaryTime = task.Triggers.Calendar.StartBoundary[separator+1 : separator+6]
	}
	return manager.isManaged(task) &&
		task.Triggers.Calendar.ScheduleByDay.DaysInterval == 1 &&
		boundaryTime == definition.LocalTime &&
		task.Settings.StartWhenAvailable &&
		taskEnabled(task.Settings.Enabled) &&
		strings.EqualFold(filepath.Clean(task.Actions.Exec.Command), filepath.Clean(definition.Executable)) &&
		task.Actions.Exec.Arguments == strings.Join(commandArguments(), " ")
}

func normalizeTaskXML(contents []byte) []byte {
	start := 0
	var order binary.ByteOrder
	if len(contents) >= 2 && contents[0] == 0xff && contents[1] == 0xfe {
		start = 2
		order = binary.LittleEndian
	} else if len(contents) >= 2 && contents[0] == 0xfe && contents[1] == 0xff {
		start = 2
		order = binary.BigEndian
	} else if len(contents) >= 4 && contents[1] == 0 && contents[3] == 0 {
		order = binary.LittleEndian
	}
	if order != nil {
		units := make([]uint16, 0, (len(contents)-start)/2)
		for offset := start; offset+1 < len(contents); offset += 2 {
			units = append(units, order.Uint16(contents[offset:offset+2]))
		}
		contents = []byte(string(utf16.Decode(units)))
	}
	contents = bytes.ReplaceAll(contents, []byte(`encoding="UTF-16"`), []byte(`encoding="UTF-8"`))
	contents = bytes.ReplaceAll(contents, []byte(`encoding="utf-16"`), []byte(`encoding="UTF-8"`))
	return bytes.TrimSpace(contents)
}

// Exported task XML omits Settings.Enabled unless the task is disabled, so an
// absent element means enabled.
func taskEnabled(enabled *bool) bool {
	return enabled == nil || *enabled
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
