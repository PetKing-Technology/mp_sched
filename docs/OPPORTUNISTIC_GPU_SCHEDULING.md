# 机会式 GPU 调度升级方案

日期：2026-07-29
分支：opportunistic-gpu-scheduling-20260729

## 1. 目标

保留数据库统一排队，但避免严格队首阻塞。GPU 任务不声明、也不获得固定显存配额；mp-worker 持续扫描宿主机真实显存，只要某张允许使用的 GPU 在扣除公共安全余量后仍有至少 20 GiB 空闲，就立即从队列启动一个任务。

本版本不引入 MIG、vGPU、TKE/qGPU 或 CUDA MPS，也不实现运行中 CUDA 进程的暂停与显存释放。

## 2. 已确认策略

| 参数 | 默认值 | 含义 |
|---|---:|---|
| worker.not_admitted_retry_ms | 2000 ms | 准入失败任务的再次扫描间隔 |
| docker.gpu_admission.min_start_free_memory_mb | 20480 MiB | 扣除安全余量后，启动新任务至少需要的空闲显存 |
| docker.gpu_admission.reserve_memory_mb | 10240 MiB | 每张 GPU 不用于新任务准入的公共安全余量 |
| docker.gpu_admission.launch_guard_seconds | 120 s | 一个任务刚启动后，同卡不再启动第二个任务的观察期 |
| docker.gpu_admission.query_timeout_seconds | 3 s | 单次 nvidia-smi 查询超时 |

默认启动条件：

    nvidia-smi 当前真实空闲显存 >= 20480 + 10240 = 30720 MiB

20 GiB 是启动阈值，不是任务配额；10 GiB 是每卡公共安全余量。容器内程序仍可按实际需要申请显存。

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
4. 排除仍为 admitted 的 GPU，以及进入 running 后尚处于120秒观察期的 GPU。
5. 在满足30720 MiB原始空闲显存的设备中，选择空闲显存最多的一张。
6. 将设备 ID 写入任务 extra.docker_gpu_id，Docker只挂载该设备。

宿主机上未由 mp-sched 启动的进程也会反映在 memory.free 中，因此会自动计入准入判断。

## 5. 并发安全

单次 nvidia-smi 快照不能防止两个 Worker 同时选择同一张卡。本实现使用 PostgreSQL事务级 advisory lock，将“读取 GPU 快照、选卡、写入 admitted 和 device ID”串行化。

新任务进入 admitted 后，该 GPU 一直受启动保护；进入 running 后保护持续120秒。这避免镜像启动或 CUDA 初始化尚未占用显存时连续塞入多个任务。

## 6. 故障行为

- nvidia-smi 查询失败、超时或输出无法解析：fail-closed，任务保持 pending，2秒后重试。
- 没有 GPU 达到启动阈值：保持 pending，并在API中显示实际原因。
- 运行中显存增长导致 CUDA OOM：由容器正常进入 failed；本版本不伪造“暂停等待”。
- Agent只有在原调度任务明确进入终态后才可创建新 attempt；pending/running超时不能作为重复提交依据。
- OOM后的“空卡重试”和通用幂等键属于调用方或下一阶段能力，不在本次数据库协议中自动推断。

## 7. 可观测性

任务查询新增 pending_reason、next_schedule_at 和 schedule_attempts。日志保留 task_id 和具体 GPU 拒绝原因，包括所需原始空闲显存、扫描到的最大空闲显存、启动观察期内的 GPU 数量和 nvidia-smi 查询错误。

## 8. 回滚

将 docker.gpu_admission.enable 设置为 false，即可恢复原有 gpu_ids 多重集槽位调度，不需要回滚数据库字段。新增任务字段均允许空值。

## 9. 验收条件

1. 原始空闲显存为30720 MiB时允许启动；低于该值1 MiB时拒绝。
2. 多张卡满足条件时选择真实空闲显存最多的卡。
3. gpu_ids重复不会在机会式模式中制造固定任务槽。
4. 同卡存在 admitted任务时不再启动第二个任务。
5. running任务开始后120秒内不再向同卡启动新任务。
6. 队首任务拒绝后进入2秒冷却，后续 pending任务能够被扫描。
7. 机会式模式关闭后，原有固定槽位测试全部通过。
8. GPU查询失败不会误启动任务。
