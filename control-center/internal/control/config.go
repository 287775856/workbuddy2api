// Package control implements the independent WorkBuddy local control center.
package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is deliberately limited to panel-owned state and the shared auths
// directory. It never reads or writes workbuddy2api's config.json.
type Config struct {
	Listen         string `json:"listen"`
	AuthDir        string `json:"auth_dir"`
	DataDir        string `json:"data_dir"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	ReadOnly       bool   `json:"read_only"`
	Timezone       string `json:"timezone"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func Default() Config {
	return Config{
		Listen:         "127.0.0.1:8787",
		AuthDir:        "../workbuddy2api/auths",
		DataDir:        "./data",
		Username:       "admin",
		Password:       "",
		ReadOnly:       false,
		Timezone:       "Asia/Shanghai",
		TimeoutSeconds: 30,
	}
}

func LoadConfig(path string) (Config, error) {
	c := Default()
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, fmt.Errorf("解析控制台配置: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return c, fmt.Errorf("读取控制台配置: %w", err)
	}
	if v := strings.TrimSpace(os.Getenv("WBCC_LISTEN")); v != "" {
		c.Listen = v
	}
	if v := strings.TrimSpace(os.Getenv("WBCC_AUTH_DIR")); v != "" {
		c.AuthDir = v
	}
	if v := strings.TrimSpace(os.Getenv("WBCC_DATA_DIR")); v != "" {
		c.DataDir = v
	}
	if v := strings.TrimSpace(os.Getenv("WBCC_USERNAME")); v != "" {
		c.Username = v
	}
	if v := os.Getenv("WBCC_PASSWORD"); v != "" {
		c.Password = v
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.AuthDir == "" {
		return c, fmt.Errorf("auth_dir 不能为空")
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if c.Username == "" {
		c.Username = "admin"
	}
	if c.Password == "" {
		return c, fmt.Errorf("请通过 WBCC_PASSWORD 或配置文件设置面板密码")
	}
	if c.TimeoutSeconds < 5 {
		c.TimeoutSeconds = 30
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return c, fmt.Errorf("timezone 无效: %w", err)
	}
	var err error
	if c.AuthDir, err = filepath.Abs(c.AuthDir); err != nil {
		return c, err
	}
	if c.DataDir, err = filepath.Abs(c.DataDir); err != nil {
		return c, err
	}
	return c, nil
}

func (c Config) Timeout() time.Duration { return time.Duration(c.TimeoutSeconds) * time.Second }
