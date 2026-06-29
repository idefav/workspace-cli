package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAssetNameForPlatform(t *testing.T) {
	got := AssetName("v1.2.3", "darwin", "arm64")
	want := "workspace-cli_v1.2.3_darwin_arm64.tar.gz"
	if got != want {
		t.Fatalf("AssetName() = %q, want %q", got, want)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		current string
		latest  string
		newer   bool
	}{
		{current: "v0.1.0", latest: "v0.2.0", newer: true},
		{current: "0.1.0", latest: "v0.1.1", newer: true},
		{current: "v0.2.0", latest: "v0.2.0", newer: false},
		{current: "dev", latest: "v0.2.0", newer: true},
		{current: "v0.3.0", latest: "v0.2.0", newer: false},
	} {
		if got := IsNewer(tc.current, tc.latest); got != tc.newer {
			t.Fatalf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.newer)
		}
	}
}

func TestCheckLatestSelectsPlatformAssetAndChecksum(t *testing.T) {
	asset := AssetName("v1.2.3", "linux", "amd64")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/idefav/workspace-cli/releases/latest" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{
			"tag_name":"v1.2.3",
			"html_url":"https://github.com/idefav/workspace-cli/releases/tag/v1.2.3",
			"assets":[
				{"name":"%s","browser_download_url":"%s/download/%s"},
				{"name":"checksums.txt","browser_download_url":"%s/download/checksums.txt"}
			]
		}`, asset, serverURL(r), asset, serverURL(r))
	}))
	defer server.Close()

	client := Client{
		OwnerRepo:      "idefav/workspace-cli",
		APIBaseURL:     server.URL,
		HTTPClient:     server.Client(),
		CurrentVersion: "v1.0.0",
		GOOS:           "linux",
		GOARCH:         "amd64",
	}
	info, err := client.CheckLatest(context.Background())
	if err != nil {
		t.Fatalf("CheckLatest() error = %v", err)
	}
	if !info.UpdateAvailable {
		t.Fatal("UpdateAvailable = false, want true")
	}
	if info.Asset.Name != asset {
		t.Fatalf("asset = %q, want %q", info.Asset.Name, asset)
	}
	if info.ChecksumAsset.Name != "checksums.txt" {
		t.Fatalf("checksum asset = %+v", info.ChecksumAsset)
	}
	if info.ExpectedChecksum != "" {
		t.Fatalf("expected checksum should be empty until checksums are downloaded, got %q", info.ExpectedChecksum)
	}
}

func TestParseChecksumsFindsAsset(t *testing.T) {
	body := "1111111111111111111111111111111111111111111111111111111111111111  workspace-cli_v1.2.3_linux_amd64.tar.gz\n"
	sum, err := ParseChecksums(body, "workspace-cli_v1.2.3_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("ParseChecksums() error = %v", err)
	}
	if sum != strings.Repeat("1", 64) {
		t.Fatalf("checksum = %q", sum)
	}
}

func TestInstallLatestDownloadsVerifiesAndReplacesBinary(t *testing.T) {
	archive := buildTarGz(t, "workspace", []byte("new-binary\n"))
	hash := sha256.Sum256(archive)
	checksums := hex.EncodeToString(hash[:]) + "  " + AssetName("v1.2.3", runtime.GOOS, runtime.GOARCH) + "\n"
	target := filepath.Join(t.TempDir(), "workspace")
	if err := os.WriteFile(target, []byte("old-binary\n"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/idefav/workspace-cli/releases/latest":
			asset := AssetName("v1.2.3", runtime.GOOS, runtime.GOARCH)
			fmt.Fprintf(w, `{
				"tag_name":"v1.2.3",
				"html_url":"https://github.com/idefav/workspace-cli/releases/tag/v1.2.3",
				"assets":[
					{"name":"%s","browser_download_url":"%s/download/%s"},
					{"name":"checksums.txt","browser_download_url":"%s/download/checksums.txt"}
				]
			}`, asset, serverURL(r), asset, serverURL(r))
		case "/download/" + AssetName("v1.2.3", runtime.GOOS, runtime.GOARCH):
			_, _ = w.Write(archive)
		case "/download/checksums.txt":
			_, _ = io.WriteString(w, checksums)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := Client{
		OwnerRepo:      "idefav/workspace-cli",
		APIBaseURL:     server.URL,
		HTTPClient:     server.Client(),
		CurrentVersion: "v1.0.0",
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		ExecutablePath: target,
	}
	info, err := client.InstallLatest(context.Background())
	if err != nil {
		t.Fatalf("InstallLatest() error = %v", err)
	}
	if info.LatestVersion != "v1.2.3" {
		t.Fatalf("latest = %q", info.LatestVersion)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new-binary\n" {
		t.Fatalf("target content = %q", got)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func buildTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}
