package agentdoc

// glossary 是 agent/glossary.json 的内容：术语 → 中文释义。
// 只解释怎么读，不含本次运行的结论。
var glossary = map[string]string{
	"PSI": "Pressure Stall Information（/proc/pressure/{cpu,memory,io}）：任务因资源不足而停顿的时间占比；" +
		"some=至少一个任务受阻，full=所有任务受阻；avg10/60/300 是内核滑动均值，total 是累计微秒（counter）。",
	"D-state": "进程状态 D（TASK_UNINTERRUPTIBLE），不可中断睡眠：等待内核 IO/锁/驱动，通常与块设备、NFS、" +
		"驱动相关；持续 D 状态需结合 wchan 与 IO 延迟判断，不能直接等同于 hang。",
	"counter": "单调递增的累计值（CPU 时间片、网卡字节数、错误数等）；只有两次采样的差值才有意义，" +
		"绝对值与直接相减都可能失真（复位、热插拔、时钟跳变）。求差以 derived/deltas.json 为准。",
	"gauge": "瞬时值（load1、可用内存、温度、队列深度等）；可直接采信，不能跨采样累加或求差。",
	"rate": "派生速率（字段后缀 _per_s 或 _rate），由 counter 增量 / interval 得到，公式见 derived/formulas.json；" +
		"不要对 rate 再求差。",
	"counter_reset": "计数器归零或回退（设备重置、驱动 reload、热插拔、32 位回绕）；deltas 中 " +
		"counter_reset=true 时该区间 delta 无效，需换窗口或改用 derived/series.jsonl。",
	"deltas": "derived/deltas.json：T0→T1 的 counter 增量与速率，附 interval_s 与 counter_reset；" +
		"跨采样求差只用它，不要手算。",
	"stream": "一条指标流 = metrics/ 下一个 jsonl 文件；间隔、行数、时间范围、字段与 quality 在 " +
		"metrics/streams.json，文件名以该文件的 path 为准。",
	"scope": "指标维度：host/cpu/disk/fs/iface/net/cgroup/proc/device；同名字段在不同 scope 下不可混算。",
	"field-dictionary": "agent/field-dictionary.json：每个流每个字段的 type/unit/kind/scope/formula，" +
		"是判断能否求差的唯一依据。",
	"formulas": "derived/formulas.json：每个 L1 派生字段的字符串公式与单位，用于复算与引用。",
	"series":   "derived/series.jsonl：关键派生序列（按 name 过滤、按时间窗抽样），name 必须能在 formulas.json 中找到。",
	"L0":       "原始层：meta/、hardware/、metrics/*.jsonl、logs/*.log、probes/*.json；保留原值与来源，不做加工。",
	"L1":       "派生层：derived/ 下的 field-stats、deltas、series、formulas；每个值必须能按 formulas.json 复算。",
	"L2": "字面命中层：logs/index.json.patterns 中按类别匹配到的字符串，带 file/line/text；" +
		"只证明日志里出现过该串，不证明故障成立。",
	"L3": "研判层：根因、严重性、健康分。本工具不产出 L3，由 Agent 基于 L0/L1/L2 证据自行推断并标注置信度。",
	"quality": "数据质量标记数组：ok/gap/counter_reset/clock_step/topology_change/truncated/denied/" +
		"unsupported/budget_exceeded/partial；它解释数据为何可用或不可用，不是健康度。",
	"gap": "采样缺口：错过 tick 或采集中断（manifest.coverage.gaps 另给 expected/actual/reason）；" +
		"缺口不得插值补齐，只能报“该窗口无数据”。",
	"unsupported": "内核/虚拟化/容器不支持该能力（容器内部分 /proc、virtio 盘无 SMART、旧内核无 PSI 等）；" +
		"表示“读不到”，不表示目标异常。",
	"truncated": "按预算被截断（日志单文件/总量上限、包体 128 MiB）；命中数偏少不能反证“未发生”。",
	"null":      "未知/不可读/不适用；禁止当 0 参与计算，缺失程度看 field-stats 的 nulls。",
	"probe P0/P1/P2": "合成探测风险级，决定默认是否执行：P0 无副作用、默认开启（time.clock、syscall.basic、" +
		"limits.self、net.loopback、dns.localhost）；P1 需 --allow-heavy（fs.sandbox、cpu.burn、mem.alloc、" +
		"net.icmp_gateway）；P2 需 --allow-heavy 且 --allow-external（dns.external、net.tcp_external、" +
		"net.icmp_external）。未执行时 skipped=true 并给 skip_reason。",
	"ok:false": "probes/*.json 中仅表示该合成动作失败（errno 见 error.errno，如 EPERM/EACCES/EROFS/" +
		"ENOSPC/ETIMEDOUT）；可能是权限、只读 FS、容器限制，不是系统故障结论。",
	"skipped": "按策略未执行（skip_reason 必填：未开 --allow-heavy、压力超阈值、依赖缺失等）；" +
		"不是失败，不计入错误率。",
	"runlog": "probes/runlog.jsonl：探测生命周期 start/end/skip/error/cleanup；" +
		"用它解释 probes/ 下为何缺少某个探测文件。",
	"SMART": "磁盘自监控（ATA SMART / NVMe SMART/Health Log）：属性值、阈值、错误计数与健康位；" +
		"属性语义随厂商与介质变化，看增量、阈值与健康位，不要套单一数值标准。",
	"EDAC": "内核 EDAC 子系统（/sys/devices/system/edac）：内存 CE（可纠正）/UE（不可纠正）计数；" +
		"CE 增长表示在纠错，UE 更严重，但结论仍需结合日志与硬件证据。",
	"AER": "PCIe Advanced Error Reporting：corrected/uncorrected 错误计数与日志（AER:）；" +
		"corrected 通常已被硬件恢复，uncorrected 需结合设备链路状态。",
	"conntrack": "netfilter 连接跟踪表：count/max 与插入失败计数；接近上限会导致新连接丢弃，" +
		"但需结合速率与上限判断。",
	"journald-only": "只有 systemd-journald、没有 /var/log/messages 等文本日志的主机；本包不解析 journal 二进制，" +
		"logs/index.json 记 journal.present=true、parsed=false、gap；需要内容时用 --journal-file 导入 " +
		"journalctl -o export 文本重新采集。",
	"kmsg":   "内核日志环（/dev/kmsg 的只读拷贝）；dmesg_restrict 或容器内不可读时记 denied/unsupported。",
	"pstore": "持久化崩溃存储（/sys/fs/pstore）：oops/panic 后的内核日志与 ramoops 片段，重启后仍存在。",
	"tainted": "内核污染标记（/proc/sys/kernel/tainted 位掩码）：专有模块、强制卸载、MCE 等；" +
		"是事实线索，不是故障判定。",
	"coverage": "覆盖率：manifest.coverage 的 expected_samples/actual_samples/ratio；" +
		"ratio<1 或有 gaps 时，rate/占比的时间代表性下降。",
	"capability": "manifest.capabilities[]：能力 id 的 present/readable/errno；present=false 表示环境不提供，" +
		"readable=false 表示有但读不到（附 errno）。",
	"iowait": "CPU 空闲但有未完成 IO 的时间占比（counter）；升高说明存在 IO 等待，不能单独归因到某块磁盘。",
	"steal":  "虚拟化环境下被 hypervisor 抢占的 CPU 时间占比；KVM 客户机中升高说明宿主争抢。",
}
