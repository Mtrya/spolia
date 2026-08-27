package schedule

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const DefaultIdentifier = "io.github.mtrya.spolia"

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type Definition struct {
	Executable string
	LocalTime  string
}

type Inspection struct {
	Kind       string   `json:"kind"`
	Identifier string   `json:"identifier"`
	Artifacts  []string `json:"artifacts"`
	Installed  bool     `json:"installed"`
	Managed    bool     `json:"managed"`
	Enabled    bool     `json:"enabled"`
	Matches    bool     `json:"matches"`
	Status     string   `json:"status"`
	Detail     string   `json:"detail,omitempty"`
}

type Manager struct {
	identifier string
}

func New(identifier string) (Manager, error) {
	if !identifierPattern.MatchString(identifier) {
		return Manager{}, fmt.Errorf("invalid scheduler identifier %q", identifier)
	}
	return Manager{identifier: identifier}, nil
}

func Current() Manager {
	manager, err := New(DefaultIdentifier)
	if err != nil {
		panic(err)
	}
	return manager
}

func (manager Manager) Identifier() string {
	return manager.identifier
}

func ValidateDefinition(definition Definition) error {
	if definition.Executable == "" {
		return errors.New("scheduler executable is empty")
	}
	if !filepath.IsAbs(definition.Executable) {
		return fmt.Errorf("scheduler executable %q is not an absolute path", definition.Executable)
	}
	if _, _, err := ParseLocalTime(definition.LocalTime); err != nil {
		return err
	}
	return nil
}

func ParseLocalTime(value string) (int, int, error) {
	if len(value) != 5 || value[2] != ':' || value[0] < '0' || value[0] > '9' || value[1] < '0' || value[1] > '9' || value[3] < '0' || value[3] > '9' || value[4] < '0' || value[4] > '9' {
		return 0, 0, fmt.Errorf("schedule local_time %q must use 24-hour HH:MM format", value)
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, fmt.Errorf("schedule local_time %q must use 24-hour HH:MM format", value)
	}
	return parsed.Hour(), parsed.Minute(), nil
}

func CurrentBoundary(now time.Time, localTime string) (time.Time, error) {
	hour, minute, err := ParseLocalTime(localTime)
	if err != nil {
		return time.Time{}, err
	}
	location := now.Location()
	localNow := now.In(location)
	boundary := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if localNow.Before(boundary) {
		previous := localNow.AddDate(0, 0, -1)
		boundary = time.Date(previous.Year(), previous.Month(), previous.Day(), hour, minute, 0, 0, location)
	}
	return boundary, nil
}

func IsDue(now time.Time, localTime string, lastSuccessfulBoundary *time.Time) (bool, time.Time, error) {
	boundary, err := CurrentBoundary(now, localTime)
	if err != nil {
		return false, time.Time{}, err
	}
	if lastSuccessfulBoundary != nil && !lastSuccessfulBoundary.Before(boundary) {
		return false, boundary, nil
	}
	return true, boundary, nil
}

func commandArguments() []string {
	return []string{"sync", "--if-due", "--quiet"}
}

func systemdQuote(value string) (string, error) {
	// strconv.Quote escapes \" and \\ in a form systemd understands, but emits
	// Go-only escapes (\t, \xNN, \uNNNN) for non-printable runes. Reject those
	// instead of silently writing a unit systemd would misparse.
	for _, character := range value {
		if !unicode.IsPrint(character) {
			return "", fmt.Errorf("scheduler executable %q contains a character systemd cannot portably represent", value)
		}
	}
	value = strings.ReplaceAll(value, "%", "%%")
	return strconv.Quote(value), nil
}
