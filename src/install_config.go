package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// 安装器只为首次安装生成配置，不迁移或覆盖已有采集数据。
func writeInstallConfig(exeDir, dataDir string) error {
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return fmt.Errorf("数据目录必须是绝对路径")
	}
	configPath := filepath.Join(exeDir, "config.json")
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("配置已存在，拒绝覆盖；升级将继续使用原配置")
	} else if !os.IsNotExist(err) {
		return err
	}
	cfg := defaultConfig(exeDir)
	cfg.DataDir = filepath.Clean(dataDir)
	cfg.LogDir = filepath.Join(cfg.DataDir, "logs")
	for _, dir := range []string{cfg.DataDir, cfg.LogDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		f, err := os.CreateTemp(dir, ".install-write-check-*")
		if err != nil {
			return err
		}
		name := f.Name()
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Remove(name); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
