package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/ulikunitz/xz/lzma"
)

type File struct {
	LocalFile    string `json:"localfile"`
	PackedHash   string `json:"packedhash"`
	PackedSize   int    `json:"packedsize"`
	URL          string `json:"url"`
	UnpackedHash string `json:"unpackedhash"`
	UnpackedSize int    `json:"unpackedsize"`
}

type AssetsInfo struct {
	Files   []File `json:"files"`
	Version int    `json:"version"`
}

type ClientInfo struct {
	Revision   int    `json:"revision"`
	Version    string `json:"version"`
	Files      []File `json:"files"`
	Executable string `json:"executable"`
	Generation string `json:"generation"`
	Variant    string `json:"variant"`
}

type App struct {
	ctx     context.Context
	logger  *logrus.Logger
	baseURL string
	appName string

	clientInfo ClientInfo
	assetsInfo AssetsInfo

	totalBytes      int64
	totalFiles      int64
	downloadedBytes int64
	downloadedFiles int64

	parallel int

	activeDownloads map[string]struct{}
	mutex           sync.Mutex

	queue  chan File
	cancel chan struct{}
}

func NewApp(logger *logrus.Logger, baseURL string, appName string, parallel int) *App {
	return &App{
		logger:          logger,
		baseURL:         baseURL,
		queue:           make(chan File, 16),
		cancel:          make(chan struct{}),
		activeDownloads: make(map[string]struct{}),
		parallel:        parallel,
		appName:         appName,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) OpenClientLocation() {
	fmt.Println("Opening client location")
	if runtime.GOOS == "darwin" {
		exec.Command("open", a.appDirectory()).Start()
	} else if runtime.GOOS == "windows" {
		exec.Command("explorer", a.appDirectory()).Start()
	} else if runtime.GOOS == "linux" {
		exec.Command("xdg-open", a.appDirectory()).Start()
	}
}

func (a *App) Exit() {
	os.Exit(0)
}

func (a *App) remoteClientJSON() string {
	return "client." + a.OS() + ".json"
}

func (a *App) remoteAssetsJSON() string {
	return "assets." + a.OS() + ".json"
}

func (a *App) refreshManifests() {
	err := a.downloadFile(a.baseURL+a.remoteClientJSON(), "client.json", false)
	if err != nil {
		a.logger.Errorf("Error downloading %s: %v", a.remoteClientJSON(), err)
	}

	clientPath := filepath.Join(a.appDirectory(), "client.json")
	a.logger.Infof("Reading client manifest from: %s", clientPath)
	err = readJSON(clientPath, &a.clientInfo)
	if err != nil {
		a.logger.Errorf("Error reading %s: %v", "client.json", err)
	} else {
		a.logger.Infof("Successfully loaded client.json: version=%s, files=%d", a.clientInfo.Version, len(a.clientInfo.Files))
	}

	err = a.downloadFile(a.baseURL+a.remoteAssetsJSON(), "assets.json", false)
	if err != nil {
		a.logger.Errorf("Error downloading %s: %v", a.remoteAssetsJSON(), err)
	}

	assetsPath := filepath.Join(a.appDirectory(), "assets.json")
	a.logger.Infof("Reading assets manifest from: %s", assetsPath)
	err = readJSON(assetsPath, &a.assetsInfo)
	if err != nil {
		a.logger.Errorf("Error reading %s: %v", "assets.json", err)
	} else {
		a.logger.Infof("Successfully loaded assets.json: version=%s, files=%d", a.assetsInfo.Version, len(a.assetsInfo.Files))
	}
}

func (a *App) Version() string {
	a.refreshManifests()
	return a.clientInfo.Version
}

func (a *App) Revision() int {
	a.refreshManifests()
	return a.clientInfo.Revision
}

func (a *App) DownloadPercent() float64 {
	if a.totalBytes == 0 {
		return 0
	}
	percent := float64(a.downloadedBytes) / float64(a.totalBytes) * 100
	a.logger.Infof("Downloaded %d/%d files |  %d/%d bytes (%.2f%%)", a.downloadedFiles, a.totalFiles, a.downloadedBytes, a.totalBytes, percent)
	return percent
}

func (a *App) TotalFiles() int64 {
	return a.totalFiles
}

func (a *App) TotalBytes() int64 {
	return a.totalBytes
}

func (a *App) DownloadedFiles() int64 {
	return a.downloadedFiles
}

func (a *App) DownloadedBytes() int64 {
	return a.downloadedBytes
}

func (a *App) ToggleLocal(value bool) {
	a.logger.Infof("Setting enableLocal to %v", value)
	viper.Set("enableLocal", value)
	a.saveConfig()
}

func (a *App) saveConfig() {
	if err := viper.WriteConfigAs(filepath.Join(configDirectory(a.appName), "config.toml")); err != nil {
		a.logger.Errorf("Error writing config: %v", err)
	}
}

func (a *App) LocalEnabled() bool {
	return viper.GetBool("enableLocal")
}

func (a *App) ToggleMusic(value bool) {
	a.logger.Infof("Setting musicEnabled to %v", value)
	viper.Set("musicEnabled", value)
	a.saveConfig()
}

func (a *App) MusicEnabled() bool {
	// Default to true if not set
	if !viper.IsSet("musicEnabled") {
		return true
	}
	return viper.GetBool("musicEnabled")
}

func (a *App) OS() string {
	os := runtime.GOOS
	if os == "darwin" {
		return "mac"
	}
	return os
}

func (a *App) ActiveDownload() string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	for url := range a.activeDownloads {
		return url
	}
	return ""
}

func (a *App) Update() {
	// Download full client ZIP from GitHub Releases
	a.logger.Infof("Downloading full client ZIP...")
	a.totalFiles = 1
	a.totalBytes = 0
	a.downloadedFiles = 0
	a.downloadedBytes = 0
	
	// Clean old client folders
	a.logger.Infof("Cleaning old client folders...")
	clientFolders := []string{"OTCLIENT", "OTCLIENT NORDEMON", "OTCLIENTE NORDEMON CRIPT", "client"}
	for _, folder := range clientFolders {
		oldPath := filepath.Join(a.appDirectory(), folder)
		if fileExists(oldPath) {
			a.logger.Infof("Removing old folder: %s", oldPath)
			os.RemoveAll(oldPath)
		}
	}
	
	// Get download URL from GitHub config
	downloadURLPath := a.baseURL + "client_download_url.txt"
	a.logger.Infof("Fetching download URL from: %s", downloadURLPath)
	resp, err := http.Get(downloadURLPath)
	if err != nil {
		a.logger.Errorf("Error fetching download URL: %v", err)
		return
	}
	defer resp.Body.Close()
	
	urlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logger.Errorf("Error reading download URL: %v", err)
		return
	}
	zipURL := strings.TrimSpace(string(urlBytes))
	a.logger.Infof("ZIP URL: %s", zipURL)
	
	err = a.downloadZip(zipURL, "client", true)
	if err != nil {
		a.logger.Errorf("Error downloading client ZIP: %v", err)
		return
	}
	
	a.logger.Infof("Client ZIP downloaded and extracted successfully!")
	
	// Save version locally after successful download
	versionURL := a.baseURL + "client_version.txt"
	resp, err = http.Get(versionURL)
	if err == nil {
		defer resp.Body.Close()
		versionBytes, err := io.ReadAll(resp.Body)
		if err == nil {
			versionPath := filepath.Join(a.appDirectory(), "client_version.txt")
			os.WriteFile(versionPath, versionBytes, 0644)
			a.logger.Infof("Saved client version: %s", strings.TrimSpace(string(versionBytes)))
		}
	}
}

var mapKinds = map[int]string{
	0: "https://tibiamaps.github.io/tibia-map-data/minimap-with-markers.zip",
	1: "https://tibiamaps.github.io/tibia-map-data/minimap-without-markers.zip",
	2: "https://tibiamaps.github.io/tibia-map-data/minimap-with-grid-overlay-and-markers.zip",
	3: "https://tibiamaps.io/downloads/minimap-with-grid-overlay-without-markers",
	4: "https://tibiamaps.github.io/tibia-map-data/minimap-with-grid-overlay-and-poi-markers.zip",
}

var mapLocations = map[string]string{
	"mac":     "Contents/Resources/minimap",
	"windows": "minimap",
	"linux":   "minimap",
}

func (a *App) DownloadMaps(kind int) {
	a.totalBytes = 0
	a.downloadedBytes = 0
	a.totalFiles = 1
	a.downloadedFiles = 0
	a.logger.Infof("Downloading %s", mapKinds[kind])
	err := a.downloadZip(mapKinds[kind], mapLocations[a.OS()], true)
	if err != nil {
		a.logger.Errorf("Error downloading %s: %v", mapKinds[kind], err)
		return
	}
}

func (a *App) NeedsUpdate() bool {
	// Download remote version file
	versionURL := a.baseURL + "client_version.txt"
	resp, err := http.Get(versionURL)
	if err != nil {
		a.logger.Errorf("Error downloading version file: %v", err)
		// Fallback: check if init.lua exists in client folder
		initPath := filepath.Join(a.appDirectory(), "client", "init.lua")
		return !fileExists(initPath)
	}
	defer resp.Body.Close()
	
	remoteVersionBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logger.Errorf("Error reading remote version: %v", err)
		return false
	}
	remoteVersion := strings.TrimSpace(string(remoteVersionBytes))
	a.logger.Infof("Remote client version: %s", remoteVersion)
	
	// Check local version
	localVersionPath := filepath.Join(a.appDirectory(), "client_version.txt")
	if !fileExists(localVersionPath) {
		a.logger.Infof("No local version file - needs update")
		return true
	}
	
	localVersionBytes, err := os.ReadFile(localVersionPath)
	if err != nil {
		a.logger.Errorf("Error reading local version: %v", err)
		return true
	}
	localVersion := strings.TrimSpace(string(localVersionBytes))
	a.logger.Infof("Local client version: %s", localVersion)
	
	needsUpdate := localVersion != remoteVersion
	a.logger.Infof("Needs update: %v", needsUpdate)
	return needsUpdate
}

func (a *App) appDirectory() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		a.logger.Errorf("Error getting config directory: %v", err)
		return ""
	}
	appName := a.appName
	if a.OS() == "mac" {
		appName = a.appName + ".app"
	}
	return filepath.Join(configDir, appName)
}

func (a *App) filesToUpdate() ([]File, error) {
	var files []File
	filesTocheck := append(a.assetsInfo.Files, a.clientInfo.Files...)

	mutex := sync.Mutex{}
	wg := sync.WaitGroup{}
	wg.Add(len(filesTocheck))

	for _, file := range filesTocheck {
		go func(file File) {
			defer wg.Done()

			localFilePath := filepath.Join(a.appDirectory(), file.LocalFile)
			if !fileExists(localFilePath) {
				a.logger.Infof("File %s does not exist", localFilePath)
				mutex.Lock()
				files = append(files, file)
				mutex.Unlock()
			} else {
				localHash, err := sha256Sum(localFilePath)
				if err != nil {
					a.logger.Errorf("Error reading local file: %s\n", err)
					return
				}

				if localHash != file.UnpackedHash {
					a.logger.Infof("File %s has changed (local: %s, remote: %s)", localFilePath, string(localHash), file.UnpackedHash)
					mutex.Lock()
					files = append(files, file)
					mutex.Unlock()
				}
			}
		}(file)
	}

	wg.Wait()

	return files, nil
}

func fileExists(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

func readJSON(s string, d interface{}) error {
	contents, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	err = json.Unmarshal(contents, &d)
	if err != nil {
		return err
	}
	return nil
}

func (a *App) downloadZip(url, targetFolder string, progress bool) error {
	// Create target folder path
	targetPath := filepath.Join(a.appDirectory(), targetFolder)
	err := os.MkdirAll(targetPath, 0755)
	if err != nil {
		return err
	}

	// Download to temp file
	tempFile := filepath.Join(os.TempDir(), "nordemon_download.zip")
	out, err := os.Create(tempFile)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	a.totalBytes = resp.ContentLength

	var reader io.Reader = resp.Body
	if progress {
		reader = io.TeeReader(reader, a)
	}
	_, err = io.Copy(out, reader)
	if err != nil {
		return err
	}
	out.Close()

	// Extract directly to target folder, flattening structure if needed
	err = unzipToFolder(tempFile, targetPath)
	if err != nil {
		return err
	}

	a.downloadedFiles++

	return nil
}

func unzip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			err := os.MkdirAll(filepath.Join(dst, f.Name), 0755)
			if err != nil {
				return err
			}
			continue
		}

		err := os.MkdirAll(filepath.Join(dst, filepath.Dir(f.Name)), 0755)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.Create(filepath.Join(dst, f.Name))
		if err != nil {
			return err
		}

		_, err = io.Copy(out, rc)
		if err != nil {
			return err
		}

		out.Close()
		rc.Close()
	}

	return nil
}

// unzipToFolder extracts ZIP contents, flattening if there's only one root folder
func unzipToFolder(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	// Check if all files are in a single root folder
	var rootFolder string
	for _, f := range r.File {
		parts := strings.Split(f.Name, "/")
		if len(parts) > 0 {
			if rootFolder == "" {
				rootFolder = parts[0]
			} else if rootFolder != parts[0] {
				// Multiple root folders, don't flatten
				rootFolder = ""
				break
			}
		}
	}

	// Extract files
	for _, f := range r.File {
		// Calculate target path
		var targetPath string
		if rootFolder != "" {
			// Flatten: remove root folder from path
			relPath := strings.TrimPrefix(f.Name, rootFolder+"/")
			if relPath == "" {
				continue // Skip root folder itself
			}
			targetPath = filepath.Join(dst, relPath)
		} else {
			// Keep structure as-is
			targetPath = filepath.Join(dst, f.Name)
		}

		if f.FileInfo().IsDir() {
			err := os.MkdirAll(targetPath, 0755)
			if err != nil {
				return err
			}
			continue
		}

		err := os.MkdirAll(filepath.Dir(targetPath), 0755)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.Create(targetPath)
		if err != nil {
			rc.Close()
			return err
		}

		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *App) downloadFile(url, dst string, progress bool) error {
	a.logger.Infof("Downloading %s to %s", url, dst)
	dst = filepath.Join(a.appDirectory(), dst)
	err := os.MkdirAll(filepath.Dir(dst), 0755)
	if err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if progress {
		reader = io.TeeReader(reader, a)
	}

	if filepath.Ext(dst) != ".lzma" && filepath.Ext(url) == ".lzma" {
		lzmaReader, err := lzma.NewReader(reader)
		if err != nil {
			return err
		}
		reader = lzmaReader
	}

	_, err = io.Copy(out, reader)
	if err != nil {
		return err
	}

	atomic.AddInt64(&a.downloadedFiles, 1)

	return nil
}

func (a *App) localExecutable() string {
	name := "Contents/MacOS/client-local"
	if a.OS() == "windows" {
		name = "bin/client-local.exe"
	}
	if a.OS() == "linux" {
		name = "bin/client-local"
	}
	return filepath.Join(a.appDirectory(), name)
}

func (a *App) executable() string {
	// O ZIP extrai para subpasta "client"
	return filepath.Join(a.appDirectory(), "client", a.clientInfo.Executable)
}

func (a *App) PlayDirect() {
	// Caminho direto hardcoded
	executable := filepath.Join(a.appDirectory(), "client", "otclient_gl.exe")
	workingDir := filepath.Dir(executable)
	
	fmt.Printf("===== PLAY DIRECT =====\n")
	fmt.Printf("Executável: %s\n", executable)
	fmt.Printf("Existe: %v\n", fileExists(executable))
	
	if !fileExists(executable) {
		fmt.Printf("ERRO: Executável não encontrado!\n")
		return
	}
	
	cmd := exec.Command(executable)
	cmd.Dir = workingDir
	if err := cmd.Start(); err != nil {
		fmt.Printf("ERRO ao iniciar: %v\n", err)
		return
	}
	
	fmt.Printf("Cliente iniciado! PID: %d\n", cmd.Process.Pid)
}

func (a *App) Play(local bool) {
	executable := a.executable()
	if local {
		executable = a.localExecutable()
	}
	
	a.logger.Infof("===== ATTEMPTING TO LAUNCH OTCLIENT =====")
	a.logger.Infof("Executable path: %s", executable)
	
	// Check if executable exists
	if !fileExists(executable) {
		a.logger.Errorf("EXECUTABLE NOT FOUND: %s", executable)
		fmt.Printf("ERROR: Cliente não encontrado em: %s\n", executable)
		return
	}
	a.logger.Infof("Executable exists: YES")
	
	// Set working directory to client folder
	workingDir := filepath.Dir(executable)
	a.logger.Infof("Working directory: %s", workingDir)
	
	// Try to set permissions (ignore error on Windows)
	os.Chmod(executable, 0755)
	
	// Create command
	a.logger.Infof("Creating command...")
	cmd := exec.Command(executable)
	cmd.Dir = workingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	
	// Start process
	a.logger.Infof("Starting process...")
	if err := cmd.Start(); err != nil {
		a.logger.Errorf("FAILED TO LAUNCH: %s", err)
		fmt.Printf("ERROR ao iniciar cliente: %v\n", err)
		fmt.Printf("Caminho: %s\n", executable)
		fmt.Printf("Diretório de trabalho: %s\n", workingDir)
		return
	}
	
	a.logger.Infof("===== OTCLIENT LAUNCHED SUCCESSFULLY =====")
	a.logger.Infof("Process ID: %d", cmd.Process.Pid)
	fmt.Printf("OTClient iniciado com sucesso! PID: %d\n", cmd.Process.Pid)
}

func (a *App) Write(p []byte) (n int, err error) {
	n = len(p)
	atomic.AddInt64(&a.downloadedBytes, int64(n))
	return
}

func sha256Sum(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
