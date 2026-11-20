package derive

import "luanbird/osdiag/internal/model"

// buildFormulas 落盘 L1 公式。每条公式的 inputs 使用 stream.field，
// 保证 Agent 能沿 series.name -> formula -> 原始流复算。
func buildFormulas() model.FormulasDoc {
	return model.FormulasDoc{
		SchemaVersion: model.SchemaVersion,
		Formulas: []model.Formula{
			{
				Name:        "cpu.util_pct",
				Formula:     "100 - cpu.idle_pct",
				Unit:        "%",
				Inputs:      []string{"cpu.idle_pct"},
				Description: "CPU 非空闲占比（百分比）。仅算术派生：100 减去 idle_pct。",
			},
			{
				Name:        "memory.available_ratio",
				Formula:     "memory.mem_available_bytes / memory.mem_total_bytes",
				Unit:        "1",
				Inputs:      []string{"memory.mem_available_bytes", "memory.mem_total_bytes"},
				Description: "可用内存比例，0–1 无量纲；mem_total_bytes 为 0 时该点跳过。",
			},
			{
				Name:        "network.rx_mbps",
				Formula:     "delta(network.rx_bytes) * 8 / interval_s / 1e6",
				Unit:        "Mbps",
				Inputs:      []string{"network.rx_bytes"},
				Description: "接收速率：相邻采样点 rx_bytes 增量按 8 bit/Byte 换算、除以 1e6 得 Mbps（兆比特每秒，不是 MB/s）；计数器回退的点跳过。",
			},
		},
	}
}
