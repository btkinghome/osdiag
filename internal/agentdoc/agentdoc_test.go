package agentdoc

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"luanbird/osdiag/internal/collect"
	"luanbird/osdiag/internal/model"
	"luanbird/osdiag/internal/pack"
)

func TestRunWritesAgentDocs(t *testing.T) {
	dir := t.TempDir()
	env := collect.NewEnv(dir, "20261120T000000Z-abc123", dir, "standard", time.Now().UTC(), time.Minute)
	if err := Run(env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	guide, err := os.ReadFile(env.Path("agent/reading-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"manifest.json", "catalog.json", "metrics/streams.json", "agent/field-dictionary.json",
		"derived/field-stats.json", "derived/deltas.json", "logs/index.json", "overview.md",
		"metrics/<stream>.jsonl", "probes/runlog.jsonl", "probes/*.json", "hardware/", "top/summary.json",
		"2 MiB", "path:line", "counter_reset", "unsupported", "journald-only", "不做研判",
	} {
		if !strings.Contains(string(guide), want) {
			t.Errorf("reading-guide.md 缺少 %q", want)
		}
	}

	var gl map[string]string
	raw, err := os.ReadFile(env.Path("agent/glossary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &gl); err != nil {
		t.Fatalf("glossary.json 不是合法的术语对象: %v", err)
	}
	for _, term := range []string{
		"PSI", "D-state", "counter", "gauge", "L1", "L2", "counter_reset", "quality",
		"probe P0/P1/P2", "SMART", "EDAC", "AER", "conntrack", "journald-only",
	} {
		if strings.TrimSpace(gl[term]) == "" {
			t.Errorf("glossary.json 缺少术语 %q", term)
		}
	}

	var sm struct {
		SchemaVersion string `json:"schema_version"`
		Schemas       []struct {
			File      string   `json:"file"`
			Version   string   `json:"version"`
			AppliesTo []string `json:"applies_to"`
		} `json:"schemas"`
	}
	raw, err = os.ReadFile(env.Path("agent/schema-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &sm); err != nil {
		t.Fatalf("schema-manifest.json 解析失败: %v", err)
	}
	if sm.SchemaVersion != model.SchemaVersion {
		t.Errorf("schema_version = %q, 期望 %q", sm.SchemaVersion, model.SchemaVersion)
	}
	if len(sm.Schemas) != 13 {
		t.Fatalf("schemas 项数 = %d, 期望 13", len(sm.Schemas))
	}
	seen := map[string]bool{}
	for _, s := range sm.Schemas {
		if seen[s.File] {
			t.Errorf("schema 重复: %s", s.File)
		}
		seen[s.File] = true
		if s.Version != model.SchemaVersion {
			t.Errorf("%s version = %q, 期望 %q", s.File, s.Version, model.SchemaVersion)
		}
		if len(s.AppliesTo) == 0 {
			t.Errorf("%s 缺少 applies_to", s.File)
		}
		for _, p := range s.AppliesTo {
			if strings.Contains(p, "*") {
				continue
			}
			if got := pack.SchemaFor(p); got != s.File {
				t.Errorf("pack.SchemaFor(%q) = %q, 清单写的是 %q", p, got, s.File)
			}
		}
	}
	for _, want := range []string{"probes/*.json", "derived/series.jsonl", "top/processes.jsonl"} {
		found := false
		for _, s := range sm.Schemas {
			for _, p := range s.AppliesTo {
				if p == want {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("schema 清单缺少 %q", want)
		}
	}
}
