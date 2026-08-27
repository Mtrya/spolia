package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const repositoryAPI = "https://api.github.com/repos/MoonshotAI/kimi-code/releases"

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func main() {
	version := flag.String("version", "latest", "Kimi Code version or latest")
	binDir := flag.String("bin-dir", "", "destination directory")
	addToGitHubPath := flag.Bool("add-to-github-path", false, "append the destination to GITHUB_PATH")
	flag.Parse()
	if flag.NArg() != 0 || *binDir == "" {
		flag.Usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	installedVersion, err := install(ctx, *version, *binDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *addToGitHubPath {
		if err := appendGitHubPath(*binDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("installed Kimi Code %s for %s/%s\n", installedVersion, runtime.GOOS, runtime.GOARCH)
}

func appendGitHubPath(path string) error {
	destination := os.Getenv("GITHUB_PATH")
	if destination == "" {
		return errors.New("GITHUB_PATH is unavailable")
	}
	file, err := os.OpenFile(destination, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open GITHUB_PATH: %w", err)
	}
	if _, err := fmt.Fprintln(file, path); err != nil {
		file.Close()
		return fmt.Errorf("write GITHUB_PATH: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close GITHUB_PATH: %w", err)
	}
	return nil
}

func install(ctx context.Context, version, binDir string) (string, error) {
	asset, err := platformAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	releaseURL := repositoryAPI + "/latest"
	if version != "latest" {
		version = strings.TrimPrefix(version, "v")
		if version == "" || strings.ContainsAny(version, "/\\ \t\r\n") {
			return "", fmt.Errorf("invalid Kimi Code version %q", version)
		}
		tag := "@moonshot-ai/kimi-code@" + version
		releaseURL = repositoryAPI + "/tags/" + url.PathEscape(tag)
	}
	client := &http.Client{Timeout: 8 * time.Minute}
	contents, err := download(ctx, client, releaseURL, true)
	if err != nil {
		return "", err
	}
	var current release
	if err := json.Unmarshal(contents, &current); err != nil {
		return "", fmt.Errorf("decode Kimi Code release: %w", err)
	}
	assetURL := findAsset(current, asset)
	checksumURL := findAsset(current, asset+".sha256")
	if assetURL == "" || checksumURL == "" {
		return "", fmt.Errorf("Kimi Code release %q has no verified %s asset", current.TagName, asset)
	}
	archive, err := download(ctx, client, assetURL, false)
	if err != nil {
		return "", err
	}
	checksumContents, err := download(ctx, client, checksumURL, false)
	if err != nil {
		return "", err
	}
	expected, err := parseChecksum(checksumContents, asset)
	if err != nil {
		return "", err
	}
	actualBytes := sha256.Sum256(archive)
	actual := hex.EncodeToString(actualBytes[:])
	if actual != expected {
		return "", fmt.Errorf("Kimi Code checksum verification failed for %s", asset)
	}
	if err := extractKimi(archive, binDir); err != nil {
		return "", err
	}
	return strings.TrimPrefix(current.TagName, "@moonshot-ai/kimi-code@"), nil
}

func platformAsset(goos, goarch string) (string, error) {
	osName := map[string]string{"darwin": "darwin", "linux": "linux", "windows": "win32"}[goos]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if osName == "" || arch == "" {
		return "", fmt.Errorf("Kimi Code has no supported asset for %s/%s", goos, goarch)
	}
	return fmt.Sprintf("kimi-code-%s-%s.zip", osName, arch), nil
}

func findAsset(current release, name string) string {
	for _, asset := range current.Assets {
		if asset.Name == name {
			return asset.URL
		}
	}
	return ""
}

func download(ctx context.Context, client *http.Client, location string, api bool) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "llmloot-release-check")
	if api {
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download Kimi Code asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download Kimi Code asset: HTTP %d", response.StatusCode)
	}
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Kimi Code asset: %w", err)
	}
	return contents, nil
}

func parseChecksum(contents []byte, asset string) (string, error) {
	var found string
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("Kimi Code published duplicate checksums for %s", asset)
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("Kimi Code published an invalid checksum for %s", asset)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("Kimi Code published an invalid checksum for %s", asset)
		}
		found = strings.ToLower(fields[0])
	}
	if found == "" {
		return "", fmt.Errorf("Kimi Code published no checksum for %s", asset)
	}
	return found, nil
}

func extractKimi(contents []byte, binDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return fmt.Errorf("open Kimi Code archive: %w", err)
	}
	wanted := "kimi"
	if runtime.GOOS == "windows" {
		wanted += ".exe"
	}
	var archived *zip.File
	for _, file := range reader.File {
		if filepath.Base(filepath.FromSlash(file.Name)) == wanted && !file.FileInfo().IsDir() {
			if archived != nil {
				return fmt.Errorf("Kimi Code archive contains multiple %s files", wanted)
			}
			archived = file
		}
	}
	if archived == nil {
		return fmt.Errorf("Kimi Code archive contains no %s", wanted)
	}
	input, err := archived.Open()
	if err != nil {
		return fmt.Errorf("open archived Kimi Code binary: %w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create Kimi Code bin directory: %w", err)
	}
	destination := filepath.Join(binDir, wanted)
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("create Kimi Code binary: %w", err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return fmt.Errorf("extract Kimi Code binary: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close Kimi Code binary: %w", closeErr)
	}
	return nil
}
