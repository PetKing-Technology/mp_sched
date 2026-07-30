# 机会式 GPU 调度升级方案

日期：2026-07-30
分支：free-memory-only-gpu-scheduling-20260730

## 1. 目标

保留数据库统一排队，但避免严格队首阻塞。GPU 任务不声明、也不获得固定显存配额；mp-worker 持续扫描宿主机真实显存，只要某张允许使用的 GPU 当前至少有 20 GiB 空闲，就立即从队列启动一个任务。容器名义挂载 GPU、调度状态和历史分配记录均不等于显存占用，不参与准入。

本版本不引入 MIG、vGPU、TKE/qGPU 或 CUDA MPS，也不实现运行中 CUDA 进程的暂停与显存释放。

## 2. 已确认策略

| 参数 | 默认值 | 含义 |
|---|---:|---|
| worker.not_admitted_retry_ms | 2000 ms | 准入失败任务的再次扫描间隔 |
| docker.gpu_admission.min_start_free_memory_mb | 20480 MiB | 启动新任务至少需要的实时空闲显存 |
| docker.gpu_admission.reserve_memory_mb | 0 | 兼容旧 YAML；free-memory-only 模式忽略 |
| docker.gpu_admission.launch_guard_seconds | 0 | 兼容旧 YAML；free-memory-only 模式忽略 |
| docker.gpu_admission.query_timeout_seconds | 3 s | 单次 nvidia-smi 查询超时 |

默认启动条件：

    nvidia-smi 当前真实空闲显存 >= 20480 MiB

20 GiB 是启动阈值，不是任务配额。容器内程序仍可按实际需要申请显存；运行中增长导致 OOM 时由任务正常失败并交给上层重试。

## 3. 队列与回填

未尝试任务仍按 created_at ASC 保持原始 FIFO 顺序；重试任务按 next_schedule_at 参与公平扫描。Worker 抢占任务后如果因为全局并发或 GPU 条件不满足而拒绝准入：

1. 任务恢复为 pending。
2. 写入 pending_reason。
3. 写入 next_schedule_at = now + 2s。
4. Worker 不停在该任务上，立即继续扫描后续 pending 任务。
5. 到达 next_schedule_at 后，该任务按到期时间再次参与调度；尚未尝试的后续任务会先完成首轮扫描。

这使暂时不满足条件的队首任务不会阻塞后面的可运行任务，同时不会修改它的原始排队时间。

## 4. GPU 选卡

docker.host_resources.gpu_ids 在机会式模式下表示允许调度的物理 GPU 集合，重复项会去重，不再表示固定并发槽数。关闭机会式模式后，保留原来的多重集槽位语义，便于回滚。

每次 GPU 准入执行：

1. 通过 PostgreSQL transaction advisory lock 串行化 Docker GPU 选卡。
2. 执行 nvidia-smi 获取 index、uuid、memory.total、memory.free。
3. 只考虑 gpu_ids 中列出的设备。
4. 不读取 admitted/running、容器挂载或历史分配来推断显存占用。
5. 在满足 20480 MiB 实时空闲显存的设备中，选择空闲显存最多的一张。
6. 将设备 ID 写入任务 extra.docker_gpu_id，Docker只挂载该设备。

宿主机上未由 mp-sched 启动的进程也会反映在 memory.free 中，因此会自动计入准入判断。

## 5. 并发安全

本实现使用 PostgreSQL 事务级 advisory lock，将“读取 GPU 快照、选卡、写入 admitted 和 device ID”串行化，避免多个 Worker 在同一时刻并发选卡。串行化不制造名义显存预留；下一次准入仍重新读取真实 free memory。

## 6. 故障行为

- nvidia-smi 查询失败、超时或输出无法解析：fail-closed，任务保持 pending，2秒后重试。
- 没有 GPU 达到启动阈值：保持 pending，并在API中显示实际原因。
- 运行中显存增长导致 CUDA OOM：由容器正常进入 failed；本版本不伪造“暂停等待”。
- Agent只有在原调度任务明确进入终态后才可创建新 attempt；pending/running超时不能作为重复提交依据。
- OOM后的“空卡重试”和通用幂等键属于调用方或下一阶段能力，不在本次数据库协议中自动推断。

## 7. 可观测性

任务查询新增 pending_reason、next_schedule_at 和 schedule_attempts。日志保留 task_id 和具体 GPU 拒绝原因，包括所需实时空闲显存、扫描到的最大空闲显存和 nvidia-smi 查询错误。

## 8. 回滚

将 docker.gpu_admission.enable 设置为 false，即可恢复原有 gpu_ids 多重集槽位调度，不需要回滚数据库字段。新增任务字段均允许空值。

## 9. 验收条件

1. 实时空闲显存为 20480 MiB 时允许启动；低于该值 1 MiB 时拒绝。
2. 多张卡满足条件时选择真实空闲显存最多的卡。
3. gpu_ids重复不会在机会式模式中制造固定任务槽。
4. admitted/running 或遗留无单卡字段任务不会覆盖实时空闲显存判断。
5. 当前真实空闲显存仍达到阈值时允许继续回填，否则保持 pending。
6. 队首任务拒绝后进入2秒冷却，后续 pending任务能够被扫描。
7. 机会式模式关闭后，原有固定槽位测试全部通过。
8. GPU查询失败不会误启动任务。
