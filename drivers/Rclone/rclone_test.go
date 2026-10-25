package rclone

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// testObj 实现 model.Obj 的最小测试桩
type testObj struct {
	path    string
	name    string
	size    int64
	isDir   bool
	modTime time.Time
}

func (o *testObj) GetSize() int64              { return o.size }
func (o *testObj) GetName() string             { return o.name }
func (o *testObj) ModTime() time.Time          { return o.modTime }
func (o *testObj) CreateTime() time.Time       { return o.modTime }
func (o *testObj) IsDir() bool                 { return o.isDir }
func (o *testObj) GetHash() utils.HashInfo     { return utils.HashInfo{} }
func (o *testObj) GetID() string               { return "" }
func (o *testObj) GetPath() string             { return o.path }

// 环境依赖的端到端验证：需要一个真实运行的 rclone rcd（建议带认证）和一个
// 已有测试文件 probe.txt 的 remote。用法（本机已验证的场景）：
//
//	EEEOS_RCD_PROBE=1 \
//	EEEOS_RCD_URL=http://127.0.0.1:5572 \
//	EEEOS_RCD_USER=probe EEEOS_RCD_PASS=probe123 \
//	EEEOS_REMOTE=crtest EEEOS_ROOT=File/eeeos-probe \
//	EEEOS_RCLONE_BIN=/path/to/rclone EEEOS_RCLONE_CONF=/path/to/rclone.conf \
//	EEEOS_PROBE_LOCAL=/path/to/probe.txt \
//	go test ./drivers/Rclone/ -run TestRcloneDriverE2E -v
//
// 验证三件事：List 出真实文件、Link 取到 http 链接（rc 优先/exec 回退）、
// 该链接能真正下载到与本地文件一致的内容。
func TestRcloneDriverE2E(t *testing.T) {
	if os.Getenv("EEEOS_RCD_PROBE") == "" {
		t.Skip("未设置 EEEOS_RCD_PROBE，跳过端到端验证")
	}
	d := &Rclone{}
	d.Endpoint = os.Getenv("EEEOS_RCD_URL")
	d.RCUser = os.Getenv("EEEOS_RCD_USER")
	d.RCPassword = os.Getenv("EEEOS_RCD_PASS")
	d.RemoteName = os.Getenv("EEEOS_REMOTE")
	d.RootFolderPath = os.Getenv("EEEOS_ROOT")
	d.LinkViaExec = os.Getenv("EEEOS_RCLONE_BIN") != ""
	d.RcloneBin = os.Getenv("EEEOS_RCLONE_BIN")
	d.RcloneConf = os.Getenv("EEEOS_RCLONE_CONF")

	ctx := context.Background()
	if err := d.Init(ctx); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}

	objs, err := d.List(ctx, &testObj{path: "/", isDir: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(objs) == 0 {
		t.Fatal("List 返回空列表")
	}
	idx := -1
	for i, o := range objs {
		t.Logf("list: %-20s size=%d dir=%v", o.GetName(), o.GetSize(), o.IsDir())
		if !o.IsDir() && o.GetName() == "probe.txt" {
			idx = i
		}
	}
	if idx < 0 {
		t.Skip("远程目录里没有 probe.txt，先跑探针准备数据")
	}

	link, err := d.Link(ctx, objs[idx], model.LinkArgs{})
	if err != nil {
		t.Fatalf("Link 失败: %v", err)
	}
	n := min(45, len(link.URL))
	if !strings.HasPrefix(link.URL, "http") {
		t.Fatalf("Link 返回的不是 http 链接: %q", link.URL[:n])
	}
	t.Logf("取链成功（前 %d 字符）: %s", n, link.URL[:n])

	if local := os.Getenv("EEEOS_PROBE_LOCAL"); local != "" {
		want, err := os.ReadFile(local)
		if err != nil {
			t.Fatalf("读本地对照文件失败: %v", err)
		}
		cli := &http.Client{Timeout: 60 * time.Second}
		resp, err := cli.Get(link.URL)
		if err != nil {
			t.Fatalf("通过取到的链接下载失败: %v", err)
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("下载读取失败: %v", err)
		}
		if string(got) != string(want) {
			t.Fatalf("内容不一致: want %d bytes, got %d bytes", len(want), len(got))
		}
		t.Logf("✅ 链接下载内容与本地一致（%d bytes）", len(got))
	}
}
