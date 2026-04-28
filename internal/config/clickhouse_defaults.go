package config

// 与仓库根目录 docker-compose 中 clickhouse 服务环境变量一致（仅本地/示例用，生产请修改）。
const (
	DefaultClickHouseUser     = "mp_sched"
	DefaultClickHousePassword = "mp_sched_dev"
)
