package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"claudeprofiles/desktopdriver"
	"claudeprofiles/desktopui"
	"claudeprofiles/desktopusage"
	"claudeprofiles/profiles"
)

const applicationVersion = "0.7.0"

func main() {
	refreshOnce := flag.Bool("refresh-once", false, "窗口就绪后执行一次在线刷新，沿用账号冷却限制")
	flag.Parse()
	desktopusage.InstallPrivateHTTPDiagnostics()
	if runtime.GOOS != "darwin" {
		log.Fatal("Claude Desktop 账号管理目前仅支持 macOS")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("无法找到用户目录")
	}
	paths := profiles.Paths{
		LiveDir:        filepath.Join(homeDir, "Library", "Application Support", "Claude"),
		StoreDir:       filepath.Join(homeDir, "Library", "Application Support", "Claude Profiles"),
		TranscriptRoot: filepath.Join(homeDir, ".claude", "projects"),
		RequestLogDir:  filepath.Join(homeDir, "Library", "Logs", "Claude Profiles", "HTTP"),
	}
	driver := &desktopdriver.Driver{LiveDir: paths.LiveDir}
	store := profiles.New(paths, driver)
	store.SetUsageQuerier(&desktopusage.Client{
		LiveDir: paths.LiveDir, AppVersion: applicationVersion,
		EgressPolicy:     desktopusage.ClaudeSupportedEgressPolicy(),
		HTTPLogDirectory: paths.RequestLogDir,
	})
	desktopui.Version = applicationVersion
	desktopui.Run(store, driver.Start, *refreshOnce)
}
