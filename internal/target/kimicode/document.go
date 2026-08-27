package kimicode

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Document struct {
	path     string
	contents []byte
	exists   bool
	mode     os.FileMode
	hash     [sha256.Size]byte
	newline  string
	root     map[string]any
	sections []section
}

type section struct {
	path      []string
	start     int
	bodyStart int
	end       int
}

type ProviderInspection struct {
	Exists           bool
	Compatible       bool
	CredentialExists bool
	Reason           string
	credential       string
}

func Load(path string) (Document, error) {
	contents, err := os.ReadFile(path)
	exists := true
	mode := os.FileMode(0o600)
	if errors.Is(err, os.ErrNotExist) {
		contents = nil
		exists = false
	} else if err != nil {
		return Document{}, fmt.Errorf("read Kimi Code config: %w", err)
	} else {
		information, err := os.Stat(path)
		if err != nil {
			return Document{}, fmt.Errorf("inspect Kimi Code config: %w", err)
		}
		mode = information.Mode().Perm()
	}
	return parseDocument(path, contents, exists, mode)
}

func parseDocument(path string, contents []byte, exists bool, mode os.FileMode) (Document, error) {
	root := make(map[string]any)
	if len(bytes.TrimSpace(contents)) > 0 {
		if err := toml.Unmarshal(contents, &root); err != nil {
			return Document{}, fmt.Errorf("parse Kimi Code config: %w", err)
		}
	}
	newline := "\n"
	if bytes.Contains(contents, []byte("\r\n")) {
		newline = "\r\n"
	}
	document := Document{path: path, contents: append([]byte(nil), contents...), exists: exists, mode: mode, hash: sha256.Sum256(contents), newline: newline, root: root}
	document.sections = scanSections(contents)
	return document, nil
}

func (document Document) Path() string {
	return document.path
}

func (document Document) Exists() bool {
	return document.exists
}

func (document Document) Provider(specification ProviderSpec) ProviderInspection {
	providers := table(document.root, "providers")
	value, exists := providers[specification.ID]
	if !exists {
		return ProviderInspection{}
	}
	provider, okay := value.(map[string]any)
	if !okay {
		return ProviderInspection{Exists: true, Reason: "provider entry is not a table"}
	}
	inspection := ProviderInspection{Exists: true}
	providerType, _ := provider["type"].(string)
	baseURL, _ := provider["base_url"].(string)
	if baseURL == "" {
		if environment, okay := provider["env"].(map[string]any); okay {
			baseURL, _ = environment["OPENAI_BASE_URL"].(string)
		}
	}
	if providerType != specification.Type {
		inspection.Reason = fmt.Sprintf("provider type must be %q", specification.Type)
		return inspection
	}
	if normalizedURL(baseURL) != normalizedURL(specification.BaseURL) {
		inspection.Reason = fmt.Sprintf("provider base_url must be %q", specification.BaseURL)
		return inspection
	}
	inspection.Compatible = true
	inspection.credential, inspection.CredentialExists = providerCredential(provider)
	return inspection
}

func providerCredential(provider map[string]any) (string, bool) {
	if credential, okay := provider["api_key"].(string); okay && credential != "" {
		return credential, true
	}
	if environment, okay := provider["env"].(map[string]any); okay {
		if credential, okay := environment["OPENAI_API_KEY"].(string); okay && credential != "" {
			return credential, true
		}
	}
	return "", false
}

func normalizedURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func (document Document) model(alias string) (map[string]any, bool) {
	models := table(document.root, "models")
	value, exists := models[alias]
	if !exists {
		return nil, false
	}
	entry, okay := value.(map[string]any)
	return entry, okay
}

func (document Document) KnownReferences(alias string) []string {
	var references []string
	if value, okay := document.root["default_model"].(string); okay && value == alias {
		references = append(references, "default_model")
	}
	if secondary := table(document.root, "secondary_model"); secondary != nil {
		if value, okay := secondary["model"].(string); okay && value == alias {
			references = append(references, "secondary_model.model")
		}
	}
	return references
}

func table(root map[string]any, key string) map[string]any {
	value, _ := root[key].(map[string]any)
	return value
}

func semanticValue(value any) (string, error) {
	contents, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(contents), nil
}

func semanticField(key string, value any) (string, error) {
	if key == "base_url" {
		if baseURL, okay := value.(string); okay {
			return stringSemantic(normalizedURL(baseURL)), nil
		}
	}
	if key == "capabilities" {
		items, okay := value.([]any)
		if okay {
			capabilities := make([]string, 0, len(items))
			for _, item := range items {
				capability, okay := item.(string)
				if !okay {
					return semanticValue(value)
				}
				capabilities = append(capabilities, capability)
			}
			sort.Strings(capabilities)
			return stringsSemantic(capabilities), nil
		}
	}
	return semanticValue(value)
}

func stringSemantic(value string) string {
	contents, _ := json.Marshal(value)
	return string(contents)
}

func intSemantic(value int64) string {
	return strconv.FormatInt(value, 10)
}

func stringsSemantic(values []string) string {
	contents, _ := json.Marshal(values)
	return string(contents)
}

func quote(value string) string {
	return strconv.Quote(value)
}

func renderStrings(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quote(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func scanSections(contents []byte) []section {
	var result []section
	for offset := 0; offset < len(contents); {
		lineEnd := bytes.IndexByte(contents[offset:], '\n')
		if lineEnd < 0 {
			lineEnd = len(contents)
		} else {
			lineEnd += offset + 1
		}
		line := contents[offset:lineEnd]
		if path := parseHeaderPath(line); path != nil {
			if len(result) > 0 {
				result[len(result)-1].end = offset
			}
			result = append(result, section{path: path, start: offset, bodyStart: lineEnd, end: len(contents)})
		}
		if lineEnd == len(contents) {
			break
		}
		offset = lineEnd
	}
	return result
}

func parseHeaderPath(line []byte) []string {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) < 3 || trimmed[0] != '[' || trimmed[1] == '[' {
		return nil
	}
	var decoded map[string]any
	probe := append(append([]byte(nil), trimmed...), []byte("\n__spolia_section_marker__ = true\n")...)
	if toml.Unmarshal(probe, &decoded) != nil {
		return nil
	}
	return findMarker(decoded, nil)
}

func findMarker(current map[string]any, path []string) []string {
	if marker, okay := current["__spolia_section_marker__"].(bool); okay && marker {
		return path
	}
	keys := make([]string, 0, len(current))
	for key := range current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if child, okay := current[key].(map[string]any); okay {
			if result := findMarker(child, append(path, key)); result != nil {
				return result
			}
		}
	}
	return nil
}

func (document Document) section(path ...string) (section, bool) {
	for _, candidate := range document.sections {
		if equalPath(candidate.path, path) {
			return candidate, true
		}
	}
	return section{}, false
}

func equalPath(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func hasPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && equalPath(path[:len(prefix)], prefix)
}

type renderedField struct {
	semantic string
	rendered string
}

func (document Document) setField(path []string, key string, value renderedField) (Document, error) {
	entrySection, exists := document.section(path...)
	if !exists {
		return Document{}, fmt.Errorf("section %q does not exist", strings.Join(path, "."))
	}
	start, end, indent, spelling, comment, found := assignment(document.contents, entrySection, key)
	var updated []byte
	if found {
		replacement := indent + spelling + " = " + value.rendered
		if comment != "" {
			replacement += " " + comment
		}
		if end > start && document.contents[end-1] == '\n' {
			replacement += document.newline
		}
		updated = replaceRange(document.contents, start, end, []byte(replacement))
	} else {
		insertion := []byte(key + " = " + value.rendered + document.newline)
		updated = insertAt(document.contents, entrySection.end, insertion, document.newline)
	}
	return parseDocument(document.path, updated, document.exists, document.mode)
}

func (document Document) removeField(path []string, key string) (Document, error) {
	entrySection, exists := document.section(path...)
	if !exists {
		return document, nil
	}
	start, end, _, _, _, found := assignment(document.contents, entrySection, key)
	if !found {
		return document, nil
	}
	updated := replaceRange(document.contents, start, end, nil)
	return parseDocument(document.path, updated, document.exists, document.mode)
}

func (document Document) appendTable(header string, fields []fieldPair) (Document, error) {
	updated := append([]byte(nil), document.contents...)
	if len(updated) > 0 && updated[len(updated)-1] != '\n' {
		updated = append(updated, []byte(document.newline)...)
	}
	if len(bytes.TrimSpace(updated)) > 0 && !bytes.HasSuffix(updated, []byte(document.newline+document.newline)) {
		updated = append(updated, []byte(document.newline)...)
	}
	updated = append(updated, []byte(header+document.newline)...)
	for _, field := range fields {
		updated = append(updated, []byte(field.key+" = "+field.value.rendered+document.newline)...)
	}
	return parseDocument(document.path, updated, true, document.mode)
}

type fieldPair struct {
	key   string
	value renderedField
}

func (document Document) removeEntry(prefix []string) (Document, error) {
	var spans []section
	for _, candidate := range document.sections {
		if hasPrefix(candidate.path, prefix) {
			spans = append(spans, candidate)
		}
	}
	if len(spans) == 0 {
		return document, nil
	}
	sort.Slice(spans, func(left, right int) bool { return spans[left].start > spans[right].start })
	updated := append([]byte(nil), document.contents...)
	for _, span := range spans {
		updated = replaceRange(updated, span.start, span.end, nil)
	}
	return parseDocument(document.path, updated, document.exists, document.mode)
}

func assignment(contents []byte, entrySection section, key string) (start, end int, indent, spelling, comment string, found bool) {
	for offset := entrySection.bodyStart; offset < entrySection.end; {
		lineEnd := bytes.IndexByte(contents[offset:entrySection.end], '\n')
		if lineEnd < 0 {
			lineEnd = entrySection.end
		} else {
			lineEnd += offset + 1
		}
		line := contents[offset:lineEnd]
		trimmed := bytes.TrimLeft(line, " \t")
		for _, candidate := range []string{key, quote(key), "'" + key + "'"} {
			if !bytes.HasPrefix(trimmed, []byte(candidate)) {
				continue
			}
			remainder := trimmed[len(candidate):]
			remainder = bytes.TrimLeft(remainder, " \t")
			if len(remainder) > 0 && remainder[0] == '=' {
				indent = string(line[:len(line)-len(trimmed)])
				comment = inlineComment(string(trimmed))
				assignmentEnd := lineEnd
				if key == "capabilities" {
					balance := arrayBracketDelta(string(remainder[1:]))
					for balance > 0 && assignmentEnd < entrySection.end {
						nextEnd := bytes.IndexByte(contents[assignmentEnd:entrySection.end], '\n')
						if nextEnd < 0 {
							nextEnd = entrySection.end
						} else {
							nextEnd += assignmentEnd + 1
						}
						balance += arrayBracketDelta(string(contents[assignmentEnd:nextEnd]))
						assignmentEnd = nextEnd
					}
				}
				return offset, assignmentEnd, indent, candidate, comment, true
			}
		}
		if lineEnd == entrySection.end {
			break
		}
		offset = lineEnd
	}
	return 0, 0, "", "", "", false
}

func arrayBracketDelta(value string) int {
	delta := 0
	var quoteCharacter rune
	escaped := false
	for _, character := range value {
		if escaped {
			escaped = false
			continue
		}
		if quoteCharacter == '"' && character == '\\' {
			escaped = true
			continue
		}
		if quoteCharacter != 0 {
			if character == quoteCharacter {
				quoteCharacter = 0
			}
			continue
		}
		if character == '"' || character == '\'' {
			quoteCharacter = character
			continue
		}
		if character == '#' {
			break
		}
		switch character {
		case '[':
			delta++
		case ']':
			delta--
		}
	}
	return delta
}

func inlineComment(line string) string {
	var quoteCharacter rune
	escaped := false
	for index, character := range line {
		if escaped {
			escaped = false
			continue
		}
		if quoteCharacter == '"' && character == '\\' {
			escaped = true
			continue
		}
		if quoteCharacter != 0 {
			if character == quoteCharacter {
				quoteCharacter = 0
			}
			continue
		}
		if character == '"' || character == '\'' {
			quoteCharacter = character
			continue
		}
		if character == '#' {
			return strings.TrimSpace(line[index:])
		}
	}
	return ""
}

func replaceRange(contents []byte, start, end int, replacement []byte) []byte {
	result := make([]byte, 0, len(contents)-(end-start)+len(replacement))
	result = append(result, contents[:start]...)
	result = append(result, replacement...)
	result = append(result, contents[end:]...)
	return result
}

func insertAt(contents []byte, offset int, insertion []byte, newline string) []byte {
	if offset > 0 && contents[offset-1] != '\n' {
		insertion = append([]byte(newline), insertion...)
	}
	return replaceRange(contents, offset, offset, insertion)
}
