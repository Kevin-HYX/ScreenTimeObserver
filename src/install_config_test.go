package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallConfigSeparatePaths(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "程序 空格")
	data := filepath.Join(root, "数据 空格")
	if err := os.MkdirAll(app, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeInstallConfig(app, data); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(app, filepath.Join(app, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != data || cfg.LogDir != filepath.Join(data, "logs") {
		t.Fatalf("路径错误: %+v", cfg)
	}
	if cfg.IconStateDir != filepath.Join(app, "assets", "states") {
		t.Fatal("图标路径未随安装位置迁移")
	}
	before, _ := os.ReadFile(filepath.Join(app, "config.json"))
	if err := writeInstallConfig(app, filepath.Join(root, "other")); err == nil {
		t.Fatal("不能覆盖已有配置")
	}
	after, _ := os.ReadFile(filepath.Join(app, "config.json"))
	if string(before) != string(after) {
		t.Fatal("已有配置被改写")
	}
}

func TestInstallConfigRejectsRelativePath(t *testing.T) {
	for _, path := range []string{"", "data"} {
		if err := writeInstallConfig(t.TempDir(), path); err == nil {
			t.Fatal("应拒绝相对路径")
		}
	}
}
