# 文档目录

| 文件 | 内容 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 进程划分、状态机、Pipeline、Provider、回调、对账、运行超时扫描、GPU 槽位与机会式显存准入 |
| [CONFIG.md](CONFIG.md) | 配置文件字段、默认值、`controller.http` 与 `server` 的关系、Docker / 回调 / Worker / Telemetry 子段 |
| [OPPORTUNISTIC_GPU_SCHEDULING.md](OPPORTUNISTIC_GPU_SCHEDULING.md) | 2026-07-29 机会式 GPU 显存扫描、队列回填、并发保护与回滚方案 |
| [API.md](API.md) | HTTP 端点、请求体字段、状态与事件、错误码、ClickHouse 遥测查询 |
| [openapi.yaml](openapi.yaml) | OpenAPI 3，可直接导入 Postman / Swagger UI |

仓库根目录的 [../README.md](../README.md) 介绍如何安装、运行与容器部署。
