package pack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"luanbird/osdiag/internal/model"
)

func TestTarGzRoundTripAndDeterminism(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, "stage")
	if err := os.MkdirAll(filepath.Join(stage, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "README.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "meta", "host.json"), []byte("{\"hostname\":\"h\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	arts, err := Scan(stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 2 {
		t.Fatalf("artifacts=%d", len(arts))
	}
	m := &model.Manifest{SchemaVersion: model.SchemaVersion, Artifacts: arts}
	if err := WriteJSON(filepath.Join(stage, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := WriteSums(stage); err != nil {
		t.Fatal(err)
	}
	a1 := filepath.Join(dir, "a.tar.gz")
	a2 := filepath.Join(dir, "b.tar.gz")
	if err := TarGz(stage, a1); err != nil {
		t.Fatal(err)
	}
	if err := TarGz(stage, a2); err != nil {
		t.Fatal(err)
	}
	b1, _ := os.ReadFile(a1)
	b2, _ := os.ReadFile(a2)
	if !bytes.Equal(b1, b2) {
		t.Fatal("两次打包结果不一致")
	}
	got, n, err := Inspect(a1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != 2 || n != 4 { // manifest + SHA256SUMS + 2 文件
		t.Fatalf("artifacts=%d files=%d", len(got.Artifacts), n)
	}

	// 篡改后必须校验失败。
	if err := os.WriteFile(filepath.Join(stage, "README.md"), []byte("# tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a3 := filepath.Join(dir, "c.tar.gz")
	if err := TarGz(stage, a3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Inspect(a3); err == nil {
		t.Fatal("篡改后仍通过校验")
	}
}

func TestScanExcludesManifestAndSums(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"README.md", "manifest.json", "integrity/SHA256SUMS", "metrics/x.jsonl"} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("a\nb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	arts, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 2 {
		t.Fatalf("want 2 artifacts, got %d", len(arts))
	}
	for _, a := range arts {
		if a.Path == "manifest.json" || a.Path == "integrity/SHA256SUMS" {
			t.Fatalf("不应包含 %s", a.Path)
		}
		if a.Path == "metrics/x.jsonl" && (a.Rows == nil || *a.Rows != 2) {
			t.Fatalf("jsonl 行数错误: %+v", a)
		}
	}
}
