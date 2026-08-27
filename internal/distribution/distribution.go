package distribution

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const ChecksumFile = "SHA256SUMS"

type Target struct {
	GOOS      string
	GOARCH    string
	Extension string
}

var Targets = []Target{
	{GOOS: "darwin", GOARCH: "amd64", Extension: ".tar.gz"},
	{GOOS: "darwin", GOARCH: "arm64", Extension: ".tar.gz"},
	{GOOS: "linux", GOARCH: "amd64", Extension: ".tar.gz"},
	{GOOS: "linux", GOARCH: "arm64", Extension: ".tar.gz"},
	{GOOS: "windows", GOARCH: "amd64", Extension: ".zip"},
	{GOOS: "windows", GOARCH: "arm64", Extension: ".zip"},
}

type Builder struct {
	Root      string
	Output    string
	Version   string
	GoBinary  string
	BuildTime time.Time
}

func (builder Builder) Build(ctx context.Context) error {
	if builder.Root == "" {
		builder.Root = "."
	}
	if builder.Output == "" {
		builder.Output = "dist"
	}
	if builder.GoBinary == "" {
		builder.GoBinary = "go"
	}
	version, err := NormalizeVersion(builder.Version)
	if err != nil {
		return err
	}
	if builder.BuildTime.IsZero() {
		builder.BuildTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	if err := prepareOutput(builder.Output); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "llmloot-release-")
	if err != nil {
		return fmt.Errorf("create release workspace: %w", err)
	}
	defer os.RemoveAll(temporary)

	license, err := os.ReadFile(filepath.Join(builder.Root, "LICENSE"))
	if err != nil {
		return fmt.Errorf("read LICENSE: %w", err)
	}
	readme, err := os.ReadFile(filepath.Join(builder.Root, "README.md"))
	if err != nil {
		return fmt.Errorf("read README.md: %w", err)
	}

	checksums := make(map[string]string, len(Targets))
	for _, target := range Targets {
		binaryName := "llmloot"
		if target.GOOS == "windows" {
			binaryName += ".exe"
		}
		binaryPath := filepath.Join(temporary, target.GOOS+"-"+target.GOARCH+"-"+binaryName)
		command := exec.CommandContext(ctx, builder.GoBinary, "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X github.com/Mtrya/llmloot/internal/cli.Version="+version, "-o", binaryPath, "./cmd/llmloot")
		command.Dir = builder.Root
		command.Env = buildEnvironment(os.Environ(), target)
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s/%s: %w: %s", target.GOOS, target.GOARCH, err, strings.TrimSpace(string(output)))
		}
		binary, err := os.ReadFile(binaryPath)
		if err != nil {
			return fmt.Errorf("read %s/%s binary: %w", target.GOOS, target.GOARCH, err)
		}
		assetName := AssetName(version, target)
		assetPath := filepath.Join(builder.Output, assetName)
		rootName := strings.TrimSuffix(assetName, target.Extension)
		files := []archiveFile{
			{Name: rootName + "/" + binaryName, Contents: binary, Mode: 0o755},
			{Name: rootName + "/LICENSE", Contents: license, Mode: 0o644},
			{Name: rootName + "/README.md", Contents: readme, Mode: 0o644},
		}
		if target.Extension == ".zip" {
			err = writeZIP(assetPath, files, builder.BuildTime)
		} else {
			err = writeTarGzip(assetPath, files, builder.BuildTime)
		}
		if err != nil {
			return err
		}
		digest, err := FileChecksum(assetPath)
		if err != nil {
			return err
		}
		checksums[assetName] = digest
	}
	return WriteChecksums(filepath.Join(builder.Output, ChecksumFile), checksums)
}

func NormalizeVersion(version string) (string, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !releaseVersionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid release version %q", version)
	}
	return version, nil
}

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func AssetName(version string, target Target) string {
	return fmt.Sprintf("llmloot_%s_%s_%s%s", version, target.GOOS, target.GOARCH, target.Extension)
}

func TargetFor(goos, goarch string) (Target, error) {
	for _, target := range Targets {
		if target.GOOS == goos && target.GOARCH == goarch {
			return target, nil
		}
	}
	return Target{}, fmt.Errorf("unsupported release target %s/%s", goos, goarch)
}

func FileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for checksum: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("checksum %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func WriteChecksums(path string, checksums map[string]string) error {
	names := make([]string, 0, len(checksums))
	for name := range checksums {
		names = append(names, name)
	}
	sort.Strings(names)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create checksums: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, name := range names {
		if _, err := fmt.Fprintf(writer, "%s  %s\n", checksums[name], name); err != nil {
			file.Close()
			return fmt.Errorf("write checksums: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		file.Close()
		return fmt.Errorf("flush checksums: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close checksums: %w", err)
	}
	return nil
}

func ReadChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open checksums: %w", err)
	}
	defer file.Close()
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid checksum line")
		}
		if len(fields[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid checksum for %q", fields[1])
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("invalid checksum for %q", fields[1])
		}
		if _, exists := checksums[fields[1]]; exists {
			return nil, fmt.Errorf("duplicate checksum for %q", fields[1])
		}
		checksums[fields[1]] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	if len(checksums) == 0 {
		return nil, errors.New("checksum file is empty")
	}
	return checksums, nil
}

func prepareOutput(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory %s is not empty", path)
	}
	return nil
}

func buildEnvironment(environment []string, target Target) []string {
	blocked := map[string]bool{"GOOS": true, "GOARCH": true, "CGO_ENABLED": true}
	result := make([]string, 0, len(environment)+3)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !blocked[name] {
			result = append(result, entry)
		}
	}
	return append(result, "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "CGO_ENABLED=0")
}

type archiveFile struct {
	Name     string
	Contents []byte
	Mode     int64
}

func writeTarGzip(path string, files []archiveFile, timestamp time.Time) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		file.Close()
		return fmt.Errorf("create gzip writer: %w", err)
	}
	gzipWriter.Header.ModTime = timestamp
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, archived := range files {
		header := &tar.Header{Name: archived.Name, Mode: archived.Mode, Size: int64(len(archived.Contents)), ModTime: timestamp, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
		if err := tarWriter.WriteHeader(header); err != nil {
			return closeArchive(file, tarWriter, gzipWriter, fmt.Errorf("write tar header: %w", err))
		}
		if _, err := tarWriter.Write(archived.Contents); err != nil {
			return closeArchive(file, tarWriter, gzipWriter, fmt.Errorf("write tar contents: %w", err))
		}
	}
	return closeArchive(file, tarWriter, gzipWriter, nil)
}

func closeArchive(file *os.File, tarWriter *tar.Writer, gzipWriter *gzip.Writer, previous error) error {
	for _, err := range []error{tarWriter.Close(), gzipWriter.Close(), file.Close()} {
		if previous == nil && err != nil {
			previous = err
		}
	}
	return previous
}

func writeZIP(path string, files []archiveFile, timestamp time.Time) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	writer := zip.NewWriter(file)
	for _, archived := range files {
		header := &zip.FileHeader{Name: archived.Name, Method: zip.Deflate}
		header.SetMode(os.FileMode(archived.Mode))
		header.SetModTime(timestamp)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			writer.Close()
			file.Close()
			return fmt.Errorf("write zip header: %w", err)
		}
		if _, err := entry.Write(archived.Contents); err != nil {
			writer.Close()
			file.Close()
			return fmt.Errorf("write zip contents: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		file.Close()
		return fmt.Errorf("close zip: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	return nil
}
