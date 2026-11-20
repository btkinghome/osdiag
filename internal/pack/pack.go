// Package pack 负责证据包落盘、校验和打包。
package pack

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"luanbird/osdiag/internal/model"
)

// schemaFor 把包内路径映射到 schema 文件名。
var schemaFor = map[string]string{
	"manifest.json":               "manifest.schema.json",
	"catalog.json":                "catalog.schema.json",
	"metrics/streams.json":        "streams.schema.json",
	"agent/field-dictionary.json": "field-dictionary.schema.json",
	"derived/field-stats.json":    "field-stats.schema.json",
	"derived/formulas.json":       "formulas.schema.json",
	"derived/deltas.json":         "deltas.schema.json",
	"derived/series.jsonl":        "series.schema.json",
	"logs/index.json":             "logs-index.schema.json",
	"top/summary.json":            "top-summary.schema.json",
	"top/processes.jsonl":         "process-record.schema.json",
	"probes/runlog.jsonl":         "runlog.schema.json",
}

func SchemaFor(rel string) string { return schemaFor[rel] }

func KindOf(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".json":
		return "json"
	case ".jsonl":
		return "jsonl"
	case ".md":
		return "md"
	case ".log":
		return "log"
	default:
		return "text"
	}
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func CountRows(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	n := 0
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n, sc.Err()
}

// Scan 遍历包目录，生成 manifest.artifacts 条目。
// 排除 manifest.json 自身与 integrity/SHA256SUMS（后者校验前者）。
func Scan(root string) ([]model.Artifact, error) {
	var out []model.Artifact
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" || rel == "integrity/SHA256SUMS" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := HashFile(path)
		if err != nil {
			return err
		}
		a := model.Artifact{
			Path:   rel,
			Kind:   KindOf(rel),
			Bytes:  info.Size(),
			SHA256: sum,
			Schema: schemaFor[rel],
		}
		if strings.HasSuffix(rel, ".jsonl") {
			n, err := CountRows(path)
			if err != nil {
				return err
			}
			a.Rows = &n
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func WriteText(path string, s string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return os.WriteFile(path, []byte(s), 0o644)
}

// WriteSums 生成 integrity/SHA256SUMS，覆盖除自身以外的全部文件。
func WriteSums(root string) error {
	type row struct{ sum, rel string }
	var rows []row
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "integrity/SHA256SUMS" {
			return nil
		}
		sum, err := HashFile(path)
		if err != nil {
			return err
		}
		rows = append(rows, row{sum, rel})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].rel < rows[j].rel })
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s  %s\n", r.sum, r.rel)
	}
	if err := os.MkdirAll(filepath.Join(root, "integrity"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "integrity", "SHA256SUMS"), []byte(b.String()), 0o644)
}

// TarGz 以确定性顺序打包 root 到 out。
func TarGz(root, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:     rel,
			Mode:     0o644,
			Size:     info.Size(),
			ModTime:  time.Unix(0, 0),
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, in); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	return !strings.Contains(rel, "/../")
}

// Inspect 读取 tar.gz，校验 SHA256SUMS 与 manifest.artifacts，返回 manifest 与文件数。
func Inspect(archive string) (*model.Manifest, int, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, 0, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !safeRel(hdr.Name) {
			return nil, 0, fmt.Errorf("不安全的路径: %q", hdr.Name)
		}
		b, err := io.ReadAll(io.LimitReader(tr, 64*1024*1024))
		if err != nil {
			return nil, 0, err
		}
		files[hdr.Name] = b
	}

	var manifest model.Manifest
	mb, ok := files["manifest.json"]
	if !ok {
		return nil, 0, fmt.Errorf("缺少 manifest.json")
	}
	if err := json.Unmarshal(mb, &manifest); err != nil {
		return nil, 0, fmt.Errorf("manifest.json 非法: %w", err)
	}

	sumsRaw, ok := files["integrity/SHA256SUMS"]
	if !ok {
		return nil, 0, fmt.Errorf("缺少 integrity/SHA256SUMS")
	}
	listed := map[string]string{}
	for _, line := range strings.Split(string(sumsRaw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			return nil, 0, fmt.Errorf("SHA256SUMS 行格式错误: %q", line)
		}
		listed[parts[1]] = parts[0]
	}
	for rel, want := range listed {
		b, ok := files[rel]
		if !ok {
			return nil, 0, fmt.Errorf("SHA256SUMS 列出的文件缺失: %s", rel)
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want {
			return nil, 0, fmt.Errorf("%s 哈希不匹配: got %s want %s", rel, got, want)
		}
	}
	for rel := range files {
		if rel == "integrity/SHA256SUMS" {
			continue
		}
		if _, ok := listed[rel]; !ok {
			return nil, 0, fmt.Errorf("文件未出现在 SHA256SUMS: %s", rel)
		}
	}
	for _, a := range manifest.Artifacts {
		b, ok := files[a.Path]
		if !ok {
			return nil, 0, fmt.Errorf("manifest.artifacts 指向缺失文件: %s", a.Path)
		}
		if int64(len(b)) != a.Bytes {
			return nil, 0, fmt.Errorf("%s 字节数不匹配: got %d want %d", a.Path, len(b), a.Bytes)
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != a.SHA256 {
			return nil, 0, fmt.Errorf("%s manifest 哈希不匹配", a.Path)
		}
	}
	return &manifest, len(files), nil
}

// ReadManifestBytes 便于测试与调试。
func ReadManifestBytes(b []byte) (*model.Manifest, error) {
	var m model.Manifest
	if err := json.Unmarshal(bytes.TrimSpace(b), &m); err != nil {
		return nil, err
	}
	return &m, nil
}
