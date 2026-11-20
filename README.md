# osdiag（M0）

Linux 主机诊断证据采集工具。**只采集事实，不做研判**；证据包交给 Agent 分析。

设计稿：`docs/osdiag/2026-11-20-osdiag-design-v0.3.md`（schema 1.0 已冻结）。

## 当前状态

M0：骨架 + run 生命周期 + 基础信息（meta/DMI）+ 打包/校验。

- 已完成：单文件静态二进制、`run/inspect/version`、flock 防并发、信号收尾、
  manifest/catalog、SHA256SUMS、确定性 tar.gz、基础信息采集。
- 未包含：指标流（M1）、日志（M2）、主动探测（M3）、硬件深采（M4）、Agent 文档（M6）。
  因此 M0 包还不能通过 `docs/osdiag/tools/validate-package.py` 的全量 schema 校验。

## 构建

```sh
make linux-amd64     # bin/osdiag_linux_amd64
make linux-arm64
make test
```

要求 Go ≥ 1.22；`CGO_ENABLED=0`，不依赖目标机 glibc、shell、python 或 coreutils。

## 使用

```sh
# 在目标机以 root 运行；默认只读、不探测
./osdiag run --profile quick

# 指定工作目录与输出目录（只读根场景）
./osdiag run --profile quick --workdir /dev/shm/osdiag --out /dev/shm/osdiag/out

# 校验并查看摘要
./osdiag inspect /var/lib/osdiag/out/osdiag_<host>_<runid>.tar.gz
```

stdout 末行为 `ARCHIVE=<绝对路径>`。

退出码：0 成功；2 部分成功（有采集错误）；3 环境/采集失败；4 中断；5 用法或并发；6 打包失败。

## 已实现产物（M0）

```
README.md
overview.md
catalog.json
manifest.json
integrity/SHA256SUMS
meta/os-release.txt  host.json  kernel.json  cpu.json  meminfo.json
     sysctl.json  selinux.json  cgroup.json  modules.txt
hardware/dmi.json
```

manifest 与 catalog 已按 schema 1.0 校验通过（真实 openEuler 20.03 / 4.19 主机）。

## 安全边界

- 只读 `/proc`、`/sys`、`/etc/os-release`；
- 写入仅工作目录（默认 `/var/lib/osdiag`）；
- 不执行外部命令，不加载内核模块，不修改 sysctl；
- `--allow-heavy` / `--allow-external` 当前只记录到 manifest，不触发探测。

## 目录

```
cmd/osdiag/            入口
internal/app/          run/inspect/version、打包编排
internal/collect/      基础信息采集
internal/sysinfo/      /proc、/sys 解析
internal/pack/         扫描、SHA256SUMS、tar.gz、inspect
internal/model/        schema 1.0 结构体
internal/runid/        运行 ID
internal/version/      构建期版本
```
# osdiag
