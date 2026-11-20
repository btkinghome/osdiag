// Package agentdoc 生成证据包内给 AI Agent 的阅读材料：
// agent/reading-guide.md、agent/glossary.json、agent/schema-manifest.json。
// 内容只描述“怎么读这个包”，不写本次运行的结论。
package agentdoc

import (
	_ "embed"
	"fmt"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/pack"
)

// readingGuide 是嵌入的阅读指南原文，保证包内指南与代码同一版本。
//
//go:embed reading-guide.md
var readingGuide string

// Run 生成 agent/ 下的三份阅读材料。
func Run(env *collect.Env) error {
	if env == nil {
		return fmt.Errorf("agentdoc: env 为空")
	}
	sm, err := buildSchemaManifest()
	if err != nil {
		return err
	}
	if err := pack.WriteText(env.Path("agent/reading-guide.md"), readingGuide); err != nil {
		return fmt.Errorf("agentdoc: 写 agent/reading-guide.md: %w", err)
	}
	if err := pack.WriteJSON(env.Path("agent/glossary.json"), glossary); err != nil {
		return fmt.Errorf("agentdoc: 写 agent/glossary.json: %w", err)
	}
	if err := pack.WriteJSON(env.Path("agent/schema-manifest.json"), sm); err != nil {
		return fmt.Errorf("agentdoc: 写 agent/schema-manifest.json: %w", err)
	}
	if err := writeOverview(env); err != nil {
		return fmt.Errorf("agentdoc: 写 overview.md: %w", err)
	}
	return nil
}
