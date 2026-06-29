package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultOwnerRepo = "idefav/workspace-cli"
	DefaultAPIBase   = "https://api.github.com"
)

type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

type Info struct {
	CurrentVersion   string
	LatestVersion    string
	ReleaseURL       string
	UpdateAvailable  bool
	Asset            Asset
	ChecksumAsset    Asset
	ExpectedChecksum string
}

type Client struct {
	OwnerRepo      string
	APIBaseURL     string
	HTTPClient     *http.Client
	CurrentVersion string
	GOOS           string
	GOARCH         string
	ExecutablePath string
}

func AssetName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("workspace-cli_%s_%s_%s%s", version, goos, goarch, ext)
}

func IsNewer(current, latest string) bool {
	if latest == "" {
		return false
	}
	if current == "" || current == "dev" {
		return true
	}
	cmp, ok := compareSemver(current, latest)
	if !ok {
		return current != latest
	}
	return cmp < 0
}

func ParseChecksums(body, assetName string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == assetName {
			if len(fields[0]) != 64 {
				return "", fmt.Errorf("invalid checksum for %s", assetName)
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found", assetName)
}

func (c Client) CheckLatest(ctx context.Context) (Info, error) {
	release, err := c.latestRelease(ctx)
	if err != nil {
		return Info{}, err
	}
	goos, goarch := c.platform()
	target := AssetName(release.TagName, goos, goarch)
	info := Info{
		CurrentVersion:  c.CurrentVersion,
		LatestVersion:   release.TagName,
		ReleaseURL:      release.HTMLURL,
		UpdateAvailable: IsNewer(c.CurrentVersion, release.TagName),
	}
	for _, asset := range release.Assets {
		switch asset.Name {
		case target:
			info.Asset = asset
		case "checksums.txt":
			info.ChecksumAsset = asset
		}
	}
	if info.Asset.Name == "" {
		return Info{}, fmt.Errorf("release %s does not contain asset %s", release.TagName, target)
	}
	if info.ChecksumAsset.Name == "" {
		return Info{}, fmt.Errorf("release %s does not contain checksums.txt", release.TagName)
	}
	return info, nil
}

func (c Client) InstallLatest(ctx context.Context) (Info, error) {
	info, err := c.CheckLatest(ctx)
	if err != nil {
		return Info{}, err
	}
	if !info.UpdateAvailable {
		return info, nil
	}
	checksums, err := c.downloadText(ctx, info.ChecksumAsset.DownloadURL)
	if err != nil {
		return Info{}, err
	}
	expected, err := ParseChecksums(checksums, info.Asset.Name)
	if err != nil {
		return Info{}, err
	}
	info.ExpectedChecksum = expected
	archive, err := c.downloadBytes(ctx, info.Asset.DownloadURL)
	if err != nil {
		return Info{}, err
	}
	actual := sha256.Sum256(archive)
	if hex.EncodeToString(actual[:]) != expected {
		return Info{}, fmt.Errorf("checksum mismatch for %s", info.Asset.Name)
	}
	binary, mode, err := extractBinary(archive, info.Asset.Name)
	if err != nil {
		return Info{}, err
	}
	target, err := c.executablePath()
	if err != nil {
		return Info{}, err
	}
	if err := replaceExecutable(target, binary, mode); err != nil {
		return Info{}, err
	}
	return info, nil
}

func (c Client) latestRelease(ctx context.Context) (Release, error) {
	base := c.APIBaseURL
	if base == "" {
		base = DefaultAPIBase
	}
	repo := c.OwnerRepo
	if repo == "" {
		repo = DefaultOwnerRepo
	}
	url := strings.TrimRight(base, "/") + "/repos/" + repo + "/releases/latest"
	var release Release
	if err := c.getJSON(ctx, url, &release); err != nil {
		return Release{}, err
	}
	if release.TagName == "" {
		return Release{}, fmt.Errorf("latest release does not include tag_name")
	}
	return release, nil
}

func (c Client) getJSON(ctx context.Context, url string, target any) error {
	body, err := c.downloadBytes(ctx, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func (c Client) downloadText(ctx context.Context, url string) (string, error) {
	body, err := c.downloadBytes(ctx, url)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c Client) downloadBytes(ctx context.Context, url string) ([]byte, error) {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "workspace-cli-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func (c Client) platform() (string, string) {
	goos := c.GOOS
	goarch := c.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (c Client) executablePath() (string, error) {
	if c.ExecutablePath != "" {
		return c.ExecutablePath, nil
	}
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func extractBinary(archive []byte, assetName string) ([]byte, os.FileMode, error) {
	if strings.HasSuffix(assetName, ".zip") {
		return extractBinaryFromZip(archive)
	}
	return extractBinaryFromTarGz(archive)
}

func extractBinaryFromTarGz(archive []byte) ([]byte, os.FileMode, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if header.FileInfo().IsDir() || !isWorkspaceBinary(header.Name) {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, 0, err
		}
		return body, header.FileInfo().Mode().Perm(), nil
	}
	return nil, 0, fmt.Errorf("workspace binary not found in archive")
}

func extractBinaryFromZip(archive []byte) ([]byte, os.FileMode, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, 0, err
	}
	for _, file := range zr.File {
		if file.FileInfo().IsDir() || !isWorkspaceBinary(file.Name) {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, 0, err
		}
		body, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, 0, readErr
		}
		if closeErr != nil {
			return nil, 0, closeErr
		}
		return body, file.FileInfo().Mode().Perm(), nil
	}
	return nil, 0, fmt.Errorf("workspace binary not found in archive")
}

func isWorkspaceBinary(name string) bool {
	base := filepath.Base(name)
	return base == "workspace" || base == "workspace.exe"
}

func replaceExecutable(target string, body []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o755
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".workspace-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode|0o700); err != nil {
		return err
	}
	return os.Rename(tmpPath, target)
}

func compareSemver(a, b string) (int, bool) {
	left, ok := parseSemver(a)
	if !ok {
		return 0, false
	}
	right, ok := parseSemver(b)
	if !ok {
		return 0, false
	}
	for i := range left {
		if left[i] < right[i] {
			return -1, true
		}
		if left[i] > right[i] {
			return 1, true
		}
	}
	return 0, true
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		part = strings.Split(part, "-")[0]
		n, err := strconv.Atoi(part)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
