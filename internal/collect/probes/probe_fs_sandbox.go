package probes

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"luanbird/osdiag/internal/collect"
)

const fsSandboxBytes = 8 << 20

func sanitizeRunID(id string) string {
	if id == "" {
		return "unknown"
	}
	r := strings.NewReplacer("/", "_", "\\", "_", "..", "_", ":", "_")
	return r.Replace(id)
}

// probeFSSandbox 只在 workdir/probe/<runid>/ 内做 8MiB 写读、rename 与删除，
// 每一步的耗时和字节数写入 samples，结束时清理整个沙箱目录。
func probeFSSandbox(ctx context.Context, env *collect.Env) (out outcome) {
	r := newResult()
	out.res = r

	dir := filepath.Join(env.Workdir, "probe", sanitizeRunID(env.RunID))
	if env.Workdir == "" {
		dir = env.Path("probe/" + sanitizeRunID(env.RunID))
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			out.cleanup = "清理沙箱目录失败: " + err.Error()
			return
		}
		// probe/ 是本探测独占的父目录；空目录一并移除，失败（例如被并发占用）忽略。
		_ = os.Remove(filepath.Dir(dir))
		out.cleanup = "已清理沙箱目录 " + dir
	}()

	record := func(op string, start time.Time, n int64, err error) {
		r.Samples = append(r.Samples, sample(start, op, time.Since(start), n, errnoOf(err)))
	}
	fail := func(op string, err error) {
		markFail(r, errnoOf(err), op+": "+err.Error())
	}

	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "fs.sandbox 开始前已取消: "+err.Error())
		return
	}

	t0 := time.Now()
	err := os.MkdirAll(dir, 0o700)
	record("mkdir", t0, -1, err)
	if err != nil {
		fail("创建沙箱目录失败", err)
		return
	}

	payload := bytes.Repeat([]byte{'s'}, fsSandboxBytes)
	src := filepath.Join(dir, "osdiag-"+sanitizeRunID(env.RunID)+"-sandbox.bin")
	dst := src + ".renamed"

	t0 = time.Now()
	f, err := os.OpenFile(src, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	record("create", t0, -1, err)
	if err != nil {
		fail("创建沙箱文件失败", err)
		return
	}

	t0 = time.Now()
	n, werr := f.Write(payload)
	record("write", t0, int64(n), werr)
	if werr != nil {
		_ = f.Close()
		fail("写入沙箱文件失败", werr)
		return
	}
	if n != len(payload) {
		_ = f.Close()
		markFail(r, "EIO", fmt.Sprintf("写入长度不足: %d/%d", n, len(payload)))
		return
	}

	t0 = time.Now()
	serr := f.Sync()
	record("fsync", t0, -1, serr)
	cerr := f.Close()
	if serr != nil {
		fail("fsync 沙箱文件失败", serr)
		return
	}
	if cerr != nil {
		fail("关闭沙箱文件失败", cerr)
		return
	}

	if err := ctx.Err(); err != nil {
		markFail(r, errnoOf(err), "fs.sandbox 在 rename 前被取消: "+err.Error())
		return
	}

	t0 = time.Now()
	err = os.Rename(src, dst)
	record("rename", t0, -1, err)
	if err != nil {
		fail("rename 沙箱文件失败", err)
		return
	}

	t0 = time.Now()
	got, err := os.ReadFile(dst)
	record("read", t0, int64(len(got)), err)
	if err != nil {
		fail("读取沙箱文件失败", err)
		return
	}
	if !bytes.Equal(got, payload) {
		markFail(r, "EIO", "读取内容与写入内容不一致")
		return
	}

	t0 = time.Now()
	err = os.Remove(dst)
	record("delete", t0, -1, err)
	if err != nil {
		fail("删除沙箱文件失败", err)
		return
	}

	totals := map[string]float64{}
	totalUS := 0.0
	for _, s := range r.Samples {
		totals[s.Op] += s.DurationUS
		totalUS += s.DurationUS
	}
	r.Metrics = map[string]any{
		"dir":       dir,
		"bytes":     len(got),
		"steps":     len(r.Samples),
		"verified":  true,
		"write_us":  round3(totals["write"]),
		"fsync_us":  round3(totals["fsync"]),
		"read_us":   round3(totals["read"]),
		"rename_us": round3(totals["rename"]),
		"delete_us": round3(totals["delete"]),
		"total_us":  round3(totalUS),
	}
	return
}
