// Package logs 采集 kmsg、文本日志与 journal 导出文本，并生成字面模式索引（M2）。
//
// 只读取源日志；所有内容复制到 stage 内。journal 二进制格式不做解析，
// 仅在 index 中登记目录存在性与文件数/字节数（gap=journal_binary）。
package logs

import (
	"context"
	"errors"
	"fmt"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

// Run 执行 M2 日志采集并写 logs/index.json。
// 单个日志源失败只通过 env.AddError 记录，不中断其他来源。
func Run(ctx context.Context, env *collect.Env) error {
	if env == nil {
		return errors.New("logs: nil env")
	}

	idx := model.LogsIndex{
		SchemaVersion: "1.0",
		Files:         []model.LogFile{},
		Patterns:      []model.LogPattern{},
	}
	var collected []string

	if ctx.Err() == nil {
		if rec, ok := collectKmsg(env); ok {
			idx.Files = append(idx.Files, *rec)
			collected = append(collected, rec.Path)
		}
	}

	for _, c := range textCandidates {
		if ctx.Err() != nil {
			break
		}
		if rec, ok := collectTextLog(env, c); ok {
			idx.Files = append(idx.Files, *rec)
			collected = append(collected, rec.Path)
		}
	}

	journal := collectJournalInfo(env)
	idx.Journal = &journal

	if rec, ok := importJournalExports(ctx, env); ok {
		idx.Files = append(idx.Files, *rec)
		collected = append(collected, rec.Path)
	}

	idx.Patterns = scanPatterns(ctx, env, collected, env.Start)

	if err := pack.WriteJSON(env.Path("logs/index.json"), idx); err != nil {
		return fmt.Errorf("写 logs/index.json 失败: %w", err)
	}
	return ctx.Err()
}
