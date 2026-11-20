# osdiag

Linux 主机诊断证据采集工具。**只采集事实，不做研判**；证据包交给 AI Agent 分析。

- 设计稿：`docs/osdiag/2026-11-20-osdiag-design-v0.4.md`
- 包内 schema：`docs/osdiag/schema/`（1.0，13 个 JSON Schema）
- 配套 Skill：`devops-skills/luanbird-os-diagnose/`

## 状态

M0–M6 已完成，工具版本 **0.2.0**。已在一台 openEuler 20.03 / 内核 4.19.90 / VMware / K8s 节点
（x86_64）真机验证：`standard` + `--allow-heavy --allow-external` 产出 53 artifacts / 55 文件，
`inspect` 哈希一致，`validate-package.py` 全量 PASS；中断包（exit 4）同样 PASS。

包内容：meta、CPU/内存/PSI/磁盘/文件系统/网络/conntrack 七条指标流、派生统计与 series、
kmsg + 文本日志 + 22 类字面模式、进程表与 top、12 项主动探测、DMI/EDAC/hwmon/AER/pstore/
PCI/SMART/RAID、Agent 阅读文档与一页纸 overview。

## 构建

```sh
export GOCACHE=$PWD/.gocache GOTMPDIR=$PWD/.tmp   # 沙箱内若 ~/Library/Caches 不可写
make linux-amd64     # bin/osdiag_linux_amd64
make linux-arm64     # bin/osdiag_linux_arm64
make build           # 本机二进制
make test vet
```

要求 Go ≥ 1.22；`CGO_ENABLED=0`，纯标准库，不依赖目标机 glibc、shell、python 或 coreutils。

## 使用

```sh
# 环境检查（不采集）
./osdiag preflight --workdir /var/lib/osdiag

# 默认全被动：只跑 P0 探测，不写盘、不压 CPU、不发外网包
./osdiag run --profile standard

# 允许 P1：沙箱写/CPU 压/内存申请/网关 ICMP/本地 TCP；允许 P2 外网需再加 --allow-external
./osdiag run --profile standard --allow-heavy --allow-external

# 纯取证、只读根、journald-only
./osdiag run --profile quick --no-probe --workdir /dev/shm/osdiag
./osdiag run --profile quick --journal-file /tmp/journal.export

# 列表、校验
./osdiag list --workdir /var/lib/osdiag
./osdiag inspect /var/lib/osdiag/out/osdiag_<host>_<runid>.tar.gz
```

stdout 末行为 `ARCHIVE=<绝对路径>`；人读进度在 stderr。

退出码：0 成功；2 部分成功（有采集错误）；3 preflight 失败；4 中断；5 用法/并发；6 打包失败。

## 采样窗口与探测

`--duration` 是**指标采样窗口**；探测在采样结束后执行，总预算 `max(duration/4, 5s)`，
因此 `run.duration_s` 可能比 `--duration` 多出 5s 或 25%（取大者）。探测默认只跑 P0，P1 需 `--allow-heavy`，
P2 需 `--allow-heavy --allow-external`；`--no-probe` 连 P0 也关闭。

## 产物

```
manifest.json  catalog.json  overview.md  README.md
agent/reading-guide.md  glossary.json  schema-manifest.json  field-dictionary.json
meta/       os-release.txt host.json kernel.json cpu.json meminfo.json sysctl.json ...
hardware/   dmi.json edac.json hwmon.json aer.json pci.json pstore.json smart.json storage.json
metrics/    streams.json cpu.jsonl memory.jsonl pressure.jsonl disk.jsonl
            filesystem.jsonl network.jsonl conntrack.jsonl
derived/    field-stats.json deltas.json formulas.json series.jsonl
logs/       kmsg.log messages.log ... index.json
probes/     time.clock.json ... runlog.jsonl
top/        processes.jsonl summary.json
integrity/  SHA256SUMS
```

包内所有 JSON/JSONL 受 schema 1.0 约束；校验：

```sh
python3 docs/osdiag/tools/validate-package.py <解包目录>
```

## 安全边界

- 只读 `/proc`、`/sys`、`/var/log`、`/dev/kmsg`；不读 `/proc/<pid>/environ`（除非 `--with-environ`）；
- 写入仅 `--workdir`（默认 `/var/lib/osdiag`）；P1 沙箱写在 `probe/<runid>/` 内并清理；
- 不执行外部命令、不加载内核模块、不改 sysctl；
- 探测有单项 5s、总预算、load/PSI/内存水位保护；不满足时跳过并写 `skip_reason`。

## 已知限制

- 4.19 无 PSI：`pressure` 流为 0 行并标 `quality=unsupported`，不是“正常”。
- journald 二进制不解析；只统计目录，文本靠 `--journal-file` 导入 `journalctl -o export`。
- SMART/NVMe 仅交叉编译 + 单测，未在物理盘真机验证；VMware 虚拟盘会进 `unsupported`。
- 仅 x86_64 真机验证；arm64 已交叉编译，未上真机。
- `--interval/--max-log-bytes/--no-smart/--label/--root` 等设计稿中的 flag 尚未实现。

## 目录

```
cmd/osdiag/            入口
internal/app/          run/preflight/inspect/list、catalog/manifest、打包
internal/collect/      Env、meta
internal/collect/metrics/  七条指标流
internal/collect/proc/     进程与 top
internal/collect/logs/     kmsg/文本/journal-export/模式
internal/collect/probes/   12 项探测
internal/collect/hw/       深硬件
internal/derive/       field-stats/deltas/formulas/series
internal/agentdoc/     reading-guide/glossary/schema-manifest/overview
internal/sysinfo/      /proc、/sys 解析
internal/pack/         扫描、SHA256SUMS、tar.gz、inspect
internal/model/        schema 1.0 结构体
internal/runid/        运行 ID
internal/version/      构建期版本
scripts/e2e.sh         真机端到端脚本
```
