package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Mtrya/llmloot/internal/distribution"
)

func main() {
	dist := flag.String("dist", "dist", "directory containing release assets")
	versionFlag := flag.String("version", "", "release version")
	installDir := flag.String("install-dir", "", "directory to retain the verified binary")
	flag.Parse()
	if flag.NArg() != 0 || *versionFlag == "" {
		flag.Usage()
		os.Exit(2)
	}
	version, err := distribution.NormalizeVersion(*versionFlag)
	if err != nil {
		fail(err)
	}
	target, err := distribution.TargetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		fail(err)
	}
	asset := distribution.AssetName(version, target)
	for _, name := range []string{asset, distribution.ChecksumFile} {
		if _, err := os.Stat(filepath.Join(*dist, name)); err != nil {
			fail(fmt.Errorf("release asset %s is unavailable: %w", name, err))
		}
	}

	cleanServer := newReleaseServer(*dist, version, asset, false)
	defer cleanServer.Close()
	installed := *installDir
	if installed == "" {
		installed = filepath.Join(mustTempDir("llmloot-install-check-"), "bin")
		defer os.RemoveAll(filepath.Dir(installed))
	}
	if output, err := runInstaller(context.Background(), cleanServer.URL, installed); err != nil {
		fail(fmt.Errorf("installer rejected a valid archive: %w: %s", err, compact(output)))
	}
	binary := filepath.Join(installed, "llmloot")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.Command(binary, "version")
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != version {
		fail(fmt.Errorf("installed binary version check failed: %w: %s", err, compact(output)))
	}

	corruptServer := newReleaseServer(*dist, version, asset, true)
	defer corruptServer.Close()
	corruptInstall := filepath.Join(mustTempDir("llmloot-install-corrupt-"), "bin")
	defer os.RemoveAll(filepath.Dir(corruptInstall))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if output, err := runInstaller(ctx, corruptServer.URL, corruptInstall); err == nil {
		fail(fmt.Errorf("installer accepted a corrupted archive: %s", compact(output)))
	}
	if _, err := os.Stat(filepath.Join(corruptInstall, filepath.Base(binary))); !os.IsNotExist(err) {
		fail(fmt.Errorf("corrupt archive left an installed binary"))
	}
	fmt.Printf("installer check passed for %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func newReleaseServer(dist, version, asset string, corrupt bool) *httptest.Server {
	files := http.FileServer(http.Dir(dist))
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/releases/latest":
			http.Redirect(writer, request, "/tag/v"+version, http.StatusFound)
			return
		case "/tag/v" + version:
			writer.WriteHeader(http.StatusOK)
			return
		case "/" + asset:
			if corrupt {
				contents, err := os.ReadFile(filepath.Join(dist, asset))
				if err != nil {
					http.Error(writer, "asset unavailable", http.StatusInternalServerError)
					return
				}
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write(append(contents, []byte("corrupt")...))
				return
			}
		}
		files.ServeHTTP(writer, request)
	}))
}

func runInstaller(ctx context.Context, downloadURL, binDir string) ([]byte, error) {
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(ctx, "pwsh", "-NoLogo", "-NoProfile", "-File", "install.ps1")
	} else {
		command = exec.CommandContext(ctx, "bash", "install.sh")
	}
	command.Env = replaceEnvironment(os.Environ(), map[string]string{
		"LLMLOOT_INSTALL_VERSION":      "",
		"LLMLOOT_LATEST_URL":           downloadURL + "/releases/latest",
		"LLMLOOT_RELEASE_DOWNLOAD_URL": downloadURL,
		"LLMLOOT_BIN_DIR":              binDir,
	})
	return command.CombinedOutput()
}

func replaceEnvironment(environment []string, values map[string]string) []string {
	result := make([]string, 0, len(environment)+len(values))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[name]; !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	return result
}

func mustTempDir(pattern string) string {
	path, err := os.MkdirTemp("", pattern)
	if err != nil {
		fail(err)
	}
	return path
}

func compact(contents []byte) string {
	text := strings.Join(strings.Fields(string(contents)), " ")
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
