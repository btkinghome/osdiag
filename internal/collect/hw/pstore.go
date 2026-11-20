package hw

import (
	"os"
	"path/filepath"
	"time"
)

// pstoreFile 对应 hardware/pstore.json。只索引条目名、大小与 mtime，
// 绝不复制内容（pstore 可能含内核内存片段等敏感信息）。
type pstoreFile struct {
	Present    bool         `json:"present"`
	Entries    []pstoreItem `json:"entries"`
	TotalBytes int64        `json:"total_bytes"`
	Truncated  bool         `json:"truncated,omitempty"`
}

type pstoreItem struct {
	Name  string `json:"name"`
	Size  int64  `json:"size_bytes"`
	Mtime string `json:"mtime"`
}

// collectPstore 索引 /sys/fs/pstore/ 下的条目（不读取内容）。
func collectPstore(root string) pstoreFile {
	out := pstoreFile{
		Entries: []pstoreItem{},
	}
	dir := filepath.Join(root, "fs/pstore")
	if !dirExists(dir) {
		return out
	}
	out.Present = true
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if len(out.Entries) >= maxPstoreEntries {
			out.Truncated = true
			break
		}
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		out.Entries = append(out.Entries, pstoreItem{
			Name:  e.Name(),
			Size:  info.Size(),
			Mtime: info.ModTime().UTC().Format(time.RFC3339),
		})
		out.TotalBytes += info.Size()
	}
	return out
}
