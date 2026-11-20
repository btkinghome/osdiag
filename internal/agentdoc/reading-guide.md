# 阅读顺序（Agent）

本文件说明**怎么读这个包**，不含任何本次运行的结论。包格式为 schema `1.0`；术语见
`agent/glossary.json`，字段语义见 `agent/field-dictionary.json`，每个文件的 schema 见
`agent/schema-manifest.json`。

## 0. 通用规则

- 先统计、后原始；先时间窗、后全量；任何 `*.jsonl` 都不整文件读。
- 总预算约 **2 MiB**（UTF-8 字节；粗估 40–70 万 tokens，随中英文比例变化）。各步上限见标题；超限就缩小时间窗或改读统计，
  不要用"大概看过"代替引用。
- 所有结论必须能落到具体引用：`path:line`、`path#字段` 或 `probes/<name>.json#metrics.<key>`（见末节）。
- 工具只出事实（L0 原始 / L1 派生 / L2 字面命中），不出研判（L3）；包里**没有**健康分、严重性、根因。
- `null` = 未知/不可读，不能当 0；缺口不插值、不补齐。

## 1. `manifest.json` —— 先确认这次运行是否可信（≤64 KiB）

- 看：`run.status`（`ok`/`partial`/`interrupted`）、`run.exit_code`、`run.profile`、
  `run.start`/`run.end`/`run.duration_s`；`coverage.ratio` 与 `coverage.gaps[]`；
  `capabilities[]` 的 `present`/`readable`/`errno`；`probe_policy` 的
  `allow_heavy`/`allow_external`/`enabled`/`skipped_due_to_pressure`；`errors[]` 的
  `collector`/`msg`；`artifacts[]` 的 `path`/`kind`/`bytes`/`rows`（判断文件是否存在、多大）；
  `host` 与 `overhead`。
- 不看：不逐个读 `artifacts[].sha256`（校验用 `integrity/SHA256SUMS` 或 `osdiag inspect`）；
  `status=partial`、`errors` 非空只说明某个采集器失败，不要直接外推为主机故障，先看
  `capabilities.errno` 与 `errors[].collector`；`interrupted` 时先接受"周期偏短"再读后续。

## 2. `catalog.json` —— 决定读哪些文件（≤64 KiB）

- 看：`entries[]` 的 `path`/`kind`/`priority`/`bytes`/`rows`/`schema`/`description`/`notes`；
  按 `priority` 1→2 选（只有这两级），`bytes` 大的先估算预算。
- 不看：`description` 是用途说明，不是诊断结论；`priority` 只表示阅读顺序，不表示异常程度；
  某文件缺失不等于采集失败（以 `manifest.artifacts` 为准）。

## 3. `metrics/streams.json` 与 `agent/field-dictionary.json` —— 流清单与字段语义（≤320 KiB）

- 看：`streams[]` 的 `id`/`path`/`interval_ms`/`rows`/`start`/`end`/`scope`/`fields`/`quality`/`notes`；
  后续读 `metrics/<stream>.jsonl` 时**用 `path` 里的真实文件名**，不要用 `id` 拼。
  `field-dictionary.json` 的 `fields[]`：`name`/`stream`/`type`/`unit`/`kind`/`scope`/`formula`/`description`；
  `kind=counter` 才能求差，`gauge`/`rate` 直接读。
- 不看：不读流正文（第 7 步再按窗口读）；不假设某条流一定存在；`quality` 非 `ok` 的流先看
  `notes`，再决定是否可用于求速率/占比。

## 4. `derived/field-stats.json`、`derived/deltas.json`、`derived/formulas.json` —— 先统计（≤544 KiB）

- 看：`field-stats.streams[]` 的 `stream`/`field`/`count`/`nulls`/`min`/`max`/`mean`/`p50`/`p95`/`p99`/`quality`，
  用 `nulls` 判断字段可信度，用极值与分位定位候选窗口；`deltas.fields[]` 的
  `stream`/`field`/`scope`/`start`/`end`/`delta`/`rate_per_s`/`counter_reset`，这是 counter 增量的唯一合法来源；
  `formulas.json` 用于复算 L1 字段。
- 不看：不把 `max`/`p95` 高低直接当异常（包内没有基线与阈值）；`mean`/`p50` 对 counter 无意义；
  `counter_reset=true` 的 `delta` 不能用；不要自己手算 T0→T1 差值。

## 5. `logs/index.json` —— 命中了什么、在哪一行（≤256 KiB）

- 看：`files[]` 的 `path`/`source`/`bytes`/`lines`/`truncated`/`rotated`/`first_ts`/`last_ts`/`sha256`；
  `patterns[]` 的 `category`/`count`/`first_ts`/`last_ts`/`samples[]`（`file`/`line`/`text`）；
  `journal` 的 `present`/`parsed`/`bytes`/`files`/`earliest`/`latest`/`gap`。
- 不看：不 grep 整个包（先看索引，再定点读原文）；`count` 只表示字面出现次数，不是严重性；
  `journal.parsed=false` 表示二进制 journal 未解析（journald-only 主机属预期，需要内容时用
  `--journal-file` 重新采集）。

## 6. `overview.md` —— 人读速览（≤32 KiB）

- 看：主机/内核/环境身份、周期与覆盖率、目录说明；本地时区只在这里出现，与 JSON 的 UTC 时间
  对齐时要换算。
- 不看：overview 不能作为唯一证据（它是摘要，不含 `file:line`）；不要从措辞推断严重性。

## 7. 目标指标流 `metrics/<stream>.jsonl` —— 只读异常窗口（≤384 KiB）

- 看：按 `streams[].path` 打开，只取异常时间窗前后各 30 s、相关字段与相关 scope（单 CPU/单网卡等）；
  关注 `null` 比例、时间戳单调性、`quality`；速率一律取 `deltas` 或按 `formulas` 复算。
- 不看：不整文件读；不跨 `counter_reset`/`clock_step`/`topology_change` 区间相减；
  单点尖峰不是结论，需要相邻窗口与其它层证据。

## 8. 目标日志原文 `logs/*.log` —— 命中行 ±20 行（≤128 KiB）

- 看：用 `patterns[].samples[].file` 与 `line` 定点打开，读该行前后各 20 行；核对 `first_ts`/`last_ts`
  是否与目标时间窗重叠；`truncated=true` 时命中数只会偏少。
- 不看：不读未命中日志；不跨文件拼接行号（`file` 是包内相对路径，如 `logs/kmsg.log`）；
  历史 boot 的日志不能当本次周期证据（先看时间戳）。

## 9. `probes/runlog.jsonl` 与 `probes/*.json` —— 基础动作是否可用（≤64 KiB）

- 看：`runlog` 每行的 `ts`/`probe`/`event`（`start`/`end`/`skip`/`error`/`cleanup`）/`level`/`duration_ms`/`detail`，
  用来解释 `probes/` 下为何缺文件；`probes/*.json` 的 `probe`/`level`/`started_at`/`duration_ms`/`ok`/`skipped`/
  `skip_reason`/`error{errno,msg}`/`metrics`/`samples[]`（`ts`/`op`/`duration_us`）。
- 不看：`ok=false` 只表示该动作失败（看 `error.errno`），可能是权限/只读 FS/容器限制；
  `skipped=true` 是按策略未执行（看 `skip_reason` 与 `manifest.probe_policy`），不是失败；
  P1/P2 默认关闭，没有结果属正常。

## 10. `hardware/*` 与 `top/*` —— 硬件计数与资源占用（≤128 KiB）

- 看：`top/summary.json` 的 `counts`（`processes`/`threads`/`kernel_threads`/`zombies`/`dstate`/`listening_sockets`）、
  `top_cpu`/`top_rss`/`top_io` 前列、`dstate[]`（`pid`/`comm`/`state`/`wchan`/`delayacct_blkio_ticks`）；
  `hardware/` 下的 `dmi.json`、SMART 类文件（属性/阈值/健康位/错误计数）、`edac.json`（CE/UE）、
  AER 文件（corrected/uncorrected）、thermal/hwmon/raid/pstore（以 `catalog.json` 条目为准）。
- 不看：不把 SMART 原始属性值套统一阈值（介质/厂商语义不同，看增量、阈值与健康位）；
  不遍历 `top/processes.jsonl` 全部行（每行含完整 `cmdline`，按 `pid` 抽读）；单次快照不能说明趋势。

## 11. `meta/*` —— 背景资料，读一次（≤64 KiB）

- 看：`meta/host.json`、`meta/kernel.json`（含 `tainted`）、`meta/os-release.txt`、`meta/cpu.json`、
  `meta/meminfo.json`、`meta/sysctl.json`、`meta/cgroup.json`、`meta/modules.txt`、`meta/selinux.json`。
- 不看：不重复读；sysctl 取值本身不是异常（需结合业务与基线）。

## 12. 判读边界（硬约束）

- **不做研判**：包里没有健康分/严重性/根因；工具只给 L0 原始、L1 派生、L2 字面命中。
  结论由 Agent 给出，并区分"事实 / 推断 / 待确认"。
- **`ok:false`** 只表示该合成动作返回错误，原因看 `error.errno`；`skipped=true` 表示按策略未执行。
  二者都不等于系统故障。
- **L2 命中**只代表日志里出现过该字面串，可能是历史记录、无关模块或已恢复；需结合时间戳、
  上下文与其它层证据。
- **counter 必须看增量**：只用 `derived/deltas.json`（或按 `formulas.json` 复算）；
  `counter_reset`/`clock_step`/`topology_change` 区间内的差值无效。
- **quality 语义**：`ok` 可用；`gap` 采样缺口；`counter_reset` 计数器复位；`clock_step` 时钟跳变
  （时间窗不可直接对齐）；`topology_change` 设备/CPU 增减导致序列断裂；`truncated` 内容被截断；
  `denied` 权限不足；`unsupported` 内核/虚拟化不支持；`budget_exceeded` 超预算停采；`partial` 部分字段缺失。
- **`unsupported` / `gap`**：前者是"环境不提供或读不到"，后者是"该有但没有"，都表示**没有证据**，
  不能写成"正常/无异常"，也不得用 0 或插值补。
- **`null` ≠ 0**：未知值不得参与求和/均值；用 `field-stats.nulls` 量化缺失。
- 时间统一 UTC（RFC3339）；`overview.md` 的本地时间换算后再与 JSON 对齐；日志行时间是写入时间，
  不等于采集时间。

## 13. 证据引用

每条事实至少带一个可复核的引用，禁止引用包外数据或编造行号：

| 证据类型 | 引用格式 | 示例 |
|---|---|---|
| 日志命中 | `logs/<file>:<line>`（行号从 1 开始） | `logs/kmsg.log:812` |
| 指标/派生 | `derived/deltas.json#<stream>.<field>` | `derived/deltas.json#disk.io_ticks_ms` |
| 字段统计 | `derived/field-stats.json#<stream>.<field>` | `derived/field-stats.json#cpu.iowait_ms` |
| 探测 | `probes/<domain>.<name>.json#metrics.<key>` | `probes/fs.sandbox.json#metrics.<key>` |
| 进程 | `top/processes.jsonl:<line>` 或 `top/summary.json#dstate[i]` | `top/processes.jsonl:37` |
| 运行事实 | `manifest.json#<path>` | `manifest.json#coverage.gaps[0]` |

引用时同时给出时间窗（`start`/`end`）或 `interval_s`，否则速率类数值无法复核。
