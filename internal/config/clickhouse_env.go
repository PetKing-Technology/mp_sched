package config

import (
	"os"
	"strings"
)

// ClickHouseFromE2EEnv 从 E2E_CH_* 组装连接信息；未设 E2E_CH_ADDR 时 ok 为 false。
// 未单独设置 E2E_CH_USER / E2E_CH_PASSWORD 时，与 DefaultClickHouseUser / DefaultClickHousePassword 及 compose 一致。
func ClickHouseFromE2EEnv() (c ClickHouse, ok bool) {
	addr := strings.TrimSpace(os.Getenv("E2E_CH_ADDR"))
	if addr == "" {
		return ClickHouse{}, false
	}
	c = ClickHouse{
		Enable:   true,
		Address:  addr,
		Database: stringOrDefault(strings.TrimSpace(os.Getenv("E2E_CH_DATABASE")), "default"),
		User:     stringOrDefault(strings.TrimSpace(os.Getenv("E2E_CH_USER")), DefaultClickHouseUser),
		Password: stringOrDefault(os.Getenv("E2E_CH_PASSWORD"), DefaultClickHousePassword),
		TLS:      isTruthy(os.Getenv("E2E_CH_TLS")),
	}
	return c, true
}

func stringOrDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func isTruthy(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes"
}
