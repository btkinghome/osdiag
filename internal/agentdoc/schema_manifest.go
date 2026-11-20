package agentdoc

import (
	"fmt"
	"strings"

	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

// schemaBinding 是一个 schema 文件与包内产物的对应关系。
type schemaBinding struct {
	File      string   `json:"file"`
	Version   string   `json:"version"`
	AppliesTo []string `json:"applies_to"`
}

// freeform 是没有被 1.0 schema 强制的产物。
type freeform struct {
	Path string `json:"path"`
	Note string `json:"note"`
}

// schemaManifest 对应 agent/schema-manifest.json（自由结构，见 schema/README.md §4）。
type schemaManifest struct {
	SchemaVersion string          `json:"schema_version"`
	Schemas       []schemaBinding `json:"schemas"`
	Unconstrained []freeform      `json:"unconstrained"`
	Notes         []string        `json:"notes"`
}

// schemaOrder 与 docs/osdiag/schema/README.md §1 的清单一一对应，共 13 项。
// 固定路径的 File 由 pack.SchemaFor 校正，避免清单与实际产物漂移；
// probes/*.json 是唯一的 glob 适用项。
var schemaOrder = []schemaBinding{
	{File: "manifest.schema.json", AppliesTo: []string{"manifest.json"}},
	{File: "catalog.schema.json", AppliesTo: []string{"catalog.json"}},
	{File: "streams.schema.json", AppliesTo: []string{"metrics/streams.json"}},
	{File: "field-dictionary.schema.json", AppliesTo: []string{"agent/field-dictionary.json"}},
	{File: "field-stats.schema.json", AppliesTo: []string{"derived/field-stats.json"}},
	{File: "formulas.schema.json", AppliesTo: []string{"derived/formulas.json"}},
	{File: "deltas.schema.json", AppliesTo: []string{"derived/deltas.json"}},
	{File: "series.schema.json", AppliesTo: []string{"derived/series.jsonl"}},
	{File: "logs-index.schema.json", AppliesTo: []string{"logs/index.json"}},
	{File: "probe.schema.json", AppliesTo: []string{"probes/*.json"}},
	{File: "runlog.schema.json", AppliesTo: []string{"probes/runlog.jsonl"}},
	{File: "process-record.schema.json", AppliesTo: []string{"top/processes.jsonl"}},
	{File: "top-summary.schema.json", AppliesTo: []string{"top/summary.json"}},
}

var unconstrained = []freeform{
	{Path: "meta/*", Note: "自由结构（1.0）；字段命名仍遵守 schema/README.md §3 与附录 C"},
	{Path: "hardware/*", Note: "自由结构（1.0）；不同采集器的可选字段不一致，以 catalog.json 条目为准"},
	{Path: "overview.md", Note: "人读摘要，本地时区只在这里出现；仅作入口，不能作为唯一证据"},
	{Path: "README.md", Note: "人读说明与安全声明，非数据"},
	{Path: "agent/reading-guide.md", Note: "阅读顺序与判读边界，非数据"},
	{Path: "agent/glossary.json", Note: "术语表，非数据"},
	{Path: "agent/schema-manifest.json", Note: "本文件，非数据"},
	{Path: "logs/*.log", Note: "原始日志逐行文本；行号即 file:line 引用的行号"},
	{Path: "integrity/SHA256SUMS", Note: "校验清单，覆盖除自身外的全部文件"},
}

const schemaNotes = "schema_version 固定为 \"1.0\"；1.x 只允许新增可选字段、新增枚举值、新增产物，" +
	"删除字段/改字段含义/改必填集合/改路径布局一律升 2.0。Agent 先看 schema_version，再看 manifest.tool.version"

func buildSchemaManifest() (*schemaManifest, error) {
	schemas := make([]schemaBinding, 0, len(schemaOrder))
	for _, b := range schemaOrder {
		b.Version = model.SchemaVersion
		for _, p := range b.AppliesTo {
			if strings.Contains(p, "*") {
				continue // glob 项不参与 SchemaFor 校验
			}
			got := pack.SchemaFor(p)
			if got == "" {
				return nil, fmt.Errorf("agentdoc: pack.SchemaFor 未覆盖 %q", p)
			}
			b.File = got // 以 pack.SchemaFor 为准
		}
		schemas = append(schemas, b)
	}
	return &schemaManifest{
		SchemaVersion: model.SchemaVersion,
		Schemas:       schemas,
		Unconstrained: unconstrained,
		Notes: []string{
			schemaNotes,
			"manifest.artifacts[].schema 只对固定路径生效；probes/*.json 按 glob 适用，" +
				"因此具体探测文件不进入该字段",
			"agent/ 下除 field-dictionary.json 外均为阅读材料，不承载本次运行的结论",
		},
	}, nil
}
