package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/imyousuf/CodeEagle/internal/config"
)

const (
	// githubRepo is the GitHub repository for CodeEagle.
	githubRepo = "imyousuf/CodeEagle"
	// devCheckInterval is how often to check GitHub for updates (6 hours).
	devCheckInterval = 6 * time.Hour
	// devReleaseDateFile stores the published date of our installed dev release.
	devReleaseDateFile = "dev-release-date"
	// devLastCheckFile stores when we last checked GitHub.
	devLastCheckFile = "dev-last-check"
)

var (
	checkOnlyFlag bool
	forceFlag     bool
)

// updateConfig holds update-related configuration loaded from ~/.CodeEagle/update.yaml.
type updateConfig struct {
	AutoUpdateDev bool `mapstructure:"auto_update_dev"`
	Disabled      bool `mapstructure:"disabled"`
}

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for and install updates",
		Long: `Check for new versions of CodeEagle and optionally install them.

By default, this command will download and install the latest version.
For dev versions, CodeEagle checks for updates every 6 hours on startup
and only downloads when GitHub has a newer release.`,
		Example: `  # Check for updates without installing
  codeeagle update --check

  # Update to latest version
  codeeagle update

  # Force update even if already up to date
  codeeagle update --force`,
		RunE: runUpdate,
	}

	cmd.Flags().BoolVar(&checkOnlyFlag, "check", false, "Only check for updates, don't install")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Force update even if already on latest version")

	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	osName, arch := detectPlatform()
	fmt.Printf("Current version: %s\n", Version)
	fmt.Printf("Platform: %s/%s\n", osName, arch)

	if isWSL() {
		fmt.Println("(Running in WSL)")
	}
	fmt.Println()

	// Determine release tag to download
	var releaseTag string
	if isDevVersion(Version) {
		releaseTag = "dev"
		fmt.Println("Dev version detected - checking for latest dev release...")
	} else {
		fmt.Println("Checking for latest stable release...")
		tag, err := getLatestReleaseTag()
		if err != nil {
			return fmt.Errorf("failed to check for updates: %w", err)
		}
		releaseTag = tag
		fmt.Printf("Latest version: %s\n", releaseTag)
	}

	if checkOnlyFlag {
		downloadURL := buildDownloadURL(releaseTag, osName, arch)
		fmt.Printf("\nDownload URL: %s\n", downloadURL)
		fmt.Println("\nRun 'codeeagle update' to install.")
		return nil
	}

	// Check if we should skip (for dev versions without force)
	var devRelease *githubRelease
	if isDevVersion(Version) && !forceFlag {
		shouldUpdate, release := shouldAutoUpdateDev()
		if !shouldUpdate {
			localDate, _ := getLocalReleaseDate()
			lastCheck, _ := readDateFile(devLastCheckFile)
			nextCheck := lastCheck.Add(devCheckInterval)
			fmt.Printf("Installed release date: %s\n", localDate.Format(time.RFC3339))
			if !lastCheck.IsZero() {
				fmt.Printf("Last check: %s\n", lastCheck.Format(time.RFC3339))
				fmt.Printf("Next check: %s\n", nextCheck.Format(time.RFC3339))
			}
			fmt.Println("\nYou already have the latest dev release.")
			fmt.Println("Use --force to re-download anyway.")
			// Record that we checked (even though no update was needed)
			_ = recordLastCheck()
			return nil
		}
		devRelease = release
	}

	// Download and install
	if err := downloadAndInstall(releaseTag, osName, arch); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	// Record update time for dev versions
	if isDevVersion(Version) {
		var releaseDate time.Time
		if devRelease != nil {
			releaseDate = devRelease.PublishedAt
		} else {
			// Force update - fetch the release info for the date
			if release, err := getDevReleaseInfo(); err == nil {
				releaseDate = release.PublishedAt
			} else {
				releaseDate = time.Now()
			}
		}
		if err := recordDevUpdate(releaseDate); err != nil {
			fmt.Printf("Warning: failed to record update time: %v\n", err)
		}
		_ = recordLastCheck()
	}

	fmt.Println("\nUpdate complete! Please restart CodeEagle to use the new version.")
	return nil
}

// detectPlatform returns the OS and architecture for the current system.
func detectPlatform() (string, string) {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	// WSL should use Linux binaries
	if osName == "linux" && isWSL() {
		osName = "linux"
	}

	return osName, arch
}

// isWSL detects if running in Windows Subsystem for Linux.
func isWSL() bool {
	// Check for WSL-specific file
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
		return true
	}

	// Check /proc/version for WSL indicators
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}

	content := strings.ToLower(string(data))
	return strings.Contains(content, "microsoft") || strings.Contains(content, "wsl")
}

// isDevVersion checks if the given version is a dev version.
func isDevVersion(version string) bool {
	return version == "dev" || strings.HasPrefix(version, "dev-")
}

// buildDownloadURL constructs the GitHub release download URL for the given version and platform.
func buildDownloadURL(tag, osName, arch string) string {
	assetName := fmt.Sprintf("codeeagle-%s-%s.tar.gz", osName, arch)
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, tag, assetName)
}

// githubRelease represents a GitHub release response.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	PublishedAt time.Time `json:"published_at"`
}

// getLatestReleaseTag fetches the latest release tag from GitHub.
func getLatestReleaseTag() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", fmt.Sprintf("codeeagle/%s", Version))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	return release.TagName, nil
}

// getDevReleaseInfo fetches the dev release info from GitHub.
func getDevReleaseInfo() (*githubRelease, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/dev", githubRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", fmt.Sprintf("codeeagle/%s", Version))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

// getBinaryModTime returns the modification time of the current executable.
func getBinaryModTime() (time.Time, error) {
	execPath, err := os.Executable()
	if err != nil {
		return time.Time{}, err
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(execPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// readDateFile reads a date from a file in the home directory.
func readDateFile(filename string) (time.Time, error) {
	homeDir, err := config.HomeDir()
	if err != nil {
		return time.Time{}, err
	}
	data, err := os.ReadFile(filepath.Join(homeDir, filename))
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
}

// writeDateFile writes a date to a file in the home directory.
func writeDateFile(filename string, t time.Time) error {
	homeDir, err := config.HomeDir()
	if err != nil {
		return err
	}
	if err := config.EnsureHomeDir(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(homeDir, filename), []byte(t.Format(time.RFC3339)), 0644)
}

// getLocalReleaseDate returns the date of our installed dev release.
// Priority: 1. release-date file, 2. executable modification time.
func getLocalReleaseDate() (time.Time, error) {
	if date, err := readDateFile(devReleaseDateFile); err == nil {
		return date, nil
	}
	return getBinaryModTime()
}

// shouldCheckNow returns true if enough time has passed since the last check.
func shouldCheckNow() bool {
	lastCheck, err := readDateFile(devLastCheckFile)
	if err != nil {
		return true
	}
	return time.Since(lastCheck) >= devCheckInterval
}

// shouldAutoUpdateDev checks if we should update the dev version.
// Returns true if an update is needed, along with the release info.
func shouldAutoUpdateDev() (bool, *githubRelease) {
	if !shouldCheckNow() {
		return false, nil
	}

	release, err := getDevReleaseInfo()
	if err != nil {
		return false, nil
	}

	localDate, _ := getLocalReleaseDate()

	if release.PublishedAt.After(localDate) {
		return true, release
	}

	return false, nil
}

// recordDevUpdate stores the release's published date after a successful update.
func recordDevUpdate(releaseDate time.Time) error {
	return writeDateFile(devReleaseDateFile, releaseDate)
}

// recordLastCheck stores the current time as when we last checked GitHub.
func recordLastCheck() error {
	return writeDateFile(devLastCheckFile, time.Now())
}

// downloadAndInstall downloads and installs the update.
func downloadAndInstall(version, osName, arch string) error {
	downloadURL := buildDownloadURL(version, osName, arch)
	fmt.Printf("Downloading from: %s\n", downloadURL)

	tmpDir, err := os.MkdirTemp("", "codeeagle-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, "codeeagle.tar.gz")

	if err := downloadFile(downloadURL, archivePath); err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}

	fmt.Println("Download complete. Extracting...")

	binaryName := "codeeagle"
	extractedPath := filepath.Join(tmpDir, binaryName)

	if err := extractTarGz(archivePath, tmpDir, binaryName); err != nil {
		return fmt.Errorf("failed to extract tar.gz: %w", err)
	}

	if _, err := os.Stat(extractedPath); err != nil {
		return fmt.Errorf("extracted binary not found: %w", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	fmt.Printf("Installing to: %s\n", execPath)

	if err := replaceBinary(extractedPath, execPath); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	return nil
}

// downloadFile downloads a file from the given URL.
func downloadFile(url, destPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("codeeagle/%s", Version))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// extractTarGz extracts a specific file from a tar.gz archive.
func extractTarGz(archivePath, destDir, targetFile string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == targetFile {
			destPath := filepath.Join(destDir, targetFile)
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			return nil
		}
	}

	return fmt.Errorf("file %s not found in archive", targetFile)
}

// replaceBinary replaces the current binary with the new one.
func replaceBinary(newBinary, currentBinary string) error {
	oldPath := currentBinary + ".old"
	os.Remove(oldPath)

	if err := os.Rename(currentBinary, oldPath); err != nil {
		return fmt.Errorf("failed to rename old binary: %w", err)
	}

	if err := copyFile(newBinary, currentBinary); err != nil {
		_ = os.Rename(oldPath, currentBinary)
		return fmt.Errorf("failed to copy new binary: %w", err)
	}

	os.Remove(oldPath)
	return nil
}

// copyFile copies a file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// loadUpdateConfig reads update configuration from ~/.CodeEagle/update.yaml.
// Returns defaults if the file doesn't exist.
func loadUpdateConfig() updateConfig {
	cfg := updateConfig{
		AutoUpdateDev: true,
		Disabled:      false,
	}

	homeDir, err := config.HomeDir()
	if err != nil {
		return cfg
	}

	configPath := filepath.Join(homeDir, "update.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg
	}

	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetDefault("auto_update_dev", true)
	v.SetDefault("disabled", false)

	if err := v.ReadInConfig(); err != nil {
		return cfg
	}

	_ = v.Unmarshal(&cfg)
	return cfg
}

// CheckAndAutoUpdate checks if CodeEagle should be auto-updated on startup.
func CheckAndAutoUpdate() bool {
	homeDir, err := config.HomeDir()
	if err != nil {
		return false
	}
	if _, err := os.Stat(homeDir); os.IsNotExist(err) {
		return false
	}

	cfg := loadUpdateConfig()
	if cfg.Disabled {
		return false
	}

	osName, arch := detectPlatform()

	if isDevVersion(Version) {
		if !cfg.AutoUpdateDev {
			return false
		}

		shouldUpdate, release := shouldAutoUpdateDev()
		_ = recordLastCheck()

		if !shouldUpdate {
			return false
		}

		fmt.Println("[New dev version available, downloading...]")
		if err := downloadAndInstall("dev", osName, arch); err != nil {
			return false
		}

		if release != nil {
			_ = recordDevUpdate(release.PublishedAt)
		}

		fmt.Println("[Dev version auto-updated. Restart to use new version.]")
		return true
	}

	// Stable version: check for new release
	fmt.Println("[Checking for updates...]")
	latestTag, err := getLatestReleaseTag()
	if err != nil {
		return false
	}

	if Version == latestTag || "v"+Version == latestTag {
		return false
	}

	fmt.Printf("[New version available: %s]\n", latestTag)
	if err := downloadAndInstall(latestTag, osName, arch); err != nil {
		return false
	}

	fmt.Println("[Updated. Restart to use new version.]")
	return true
}
