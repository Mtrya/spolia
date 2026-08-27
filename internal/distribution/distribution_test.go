package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseTargetAndAssetContract(t *testing.T) {
	t.Parallel()
	wanted := []string{
		"spolia_1.2.3_darwin_amd64.tar.gz",
		"spolia_1.2.3_darwin_arm64.tar.gz",
		"spolia_1.2.3_linux_amd64.tar.gz",
		"spolia_1.2.3_linux_arm64.tar.gz",
		"spolia_1.2.3_windows_amd64.zip",
		"spolia_1.2.3_windows_arm64.zip",
	}
	if len(Targets) != len(wanted) {
		t.Fatalf("targets = %#v", Targets)
	}
	for index, target := range Targets {
		if actual := AssetName("1.2.3", target); actual != wanted[index] {
			t.Fatalf("asset %d = %q", index, actual)
		}
	}
}

func TestChecksumFileIsSortedAndStrict(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ChecksumFile)
	checksums := map[string]string{
		"z.zip": strings.Repeat("b", 64),
		"a.zip": strings.Repeat("a", 64),
	}
	if err := WriteChecksums(path, checksums); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != strings.Repeat("a", 64)+"  a.zip\n"+strings.Repeat("b", 64)+"  z.zip\n" {
		t.Fatalf("checksums = %q", contents)
	}
	parsed, err := ReadChecksums(path)
	if err != nil || len(parsed) != 2 || parsed["a.zip"] != strings.Repeat("a", 64) {
		t.Fatalf("parsed = %#v, err = %v", parsed, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)+"  a.zip\n"+strings.Repeat("b", 64)+"  a.zip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadChecksums(path); err == nil {
		t.Fatal("duplicate checksum was accepted")
	}
}
