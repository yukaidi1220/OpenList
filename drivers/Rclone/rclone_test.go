package rclone

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
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

func (o *testObj) GetSize() int64          { return o.size }
func (o *testObj) GetName() string         { return o.name }
func (o *testObj) ModTime() time.Time      { return o.modTime }
func (o *testObj) CreateTime() time.Time   { return o.modTime }
func (o *testObj) IsDir() bool             { return o.isDir }
func (o *testObj) GetHash() utils.HashInfo { return utils.HashInfo{} }
func (o *testObj) GetID() string           { return "" }
func (o *testObj) GetPath() string         { return o.path }

// TestToRclonePath 校验路径映射。关键约定：op 层传入的 obj.Path 已含
// root_folder_path 前缀（根目录 obj 的 Path 就是 RootFolderPath 本身，
// 见 internal/op/fs.go 的 IRootPath 分支），这里只清洗直通不 Join。
func TestToRclonePath(t *testing.T) {
	d := &Rclone{Addition: Addition{
		RootPath:   driver.RootPath{RootFolderPath: "File/sub"},
		RemoteName: "crtest",
	}}
	cases := []struct{ in, want string }{
		// 非空根：根目录 = RootFolderPath 本身（op 层根 obj 的 Path）
		{"File/sub", "File/sub"},
		{"File/sub/dir", "File/sub/dir"},
		{"File/sub/dir/file.txt", "File/sub/dir/file.txt"},
		{"/File/sub", "File/sub"},
		// 空根：Path 即 remote 相对路径
		{"", ""},
		{"/", ""},
		{"dir/file.txt", "dir/file.txt"},
		// 反斜杠兜底（用户在 root_folder_path 里手输的情况）
		{"File\\sub\\dir", "File/sub/dir"},
	}
	for _, c := range cases {
		if got := d.toRclonePath(c.in); got != c.want {
			t.Errorf("toRclonePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	d2 := &Rclone{}
	for _, c := range []struct{ in, want string }{{"", ""}, {"/", ""}, {"a/b", "a/b"}, {".hidden", ".hidden"}} {
		if got := d2.toRclonePath(c.in); got != c.want {
			t.Errorf("empty-root toRclonePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRCClient 用 httptest 验证 rc 客户端的响应处理（无需真实 rcd）
func TestRCClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/operations/about" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"authentication must be set up","input":{}}`))
			return
		}
		if r.URL.Path == "/operations/list" {
			_, _ = w.Write([]byte(`{"list":[{"ID":"x","Name":"f.txt","Size":3,"IsDir":false,"ModTime":"2026-10-26T03:20:39+08:00"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// 故意带尾斜杠构造，验证 endpoint 清洗
	c := newRCClient(srv.URL+"/", "u", "p")
	err := c.call(context.Background(), "operations/about", map[string]any{"fs": "r:"}, nil)
	if err == nil || !strings.Contains(err.Error(), "authentication must be set up") {
		t.Fatalf("非 200 信封处理错误: %v", err)
	}

	var out struct {
		List []rcListObj `json:"list"`
	}
	if err := c.call(context.Background(), "operations/list", map[string]any{"fs": "r:", "remote": ""}, &out); err != nil {
		t.Fatalf("list 调用失败: %v", err)
	}
	if len(out.List) != 1 || out.List[0].Name != "f.txt" || out.List[0].Size != 3 {
		t.Fatalf("list 解析错误: %+v", out.List)
	}
}

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
// 注意：根目录 obj 的 Path 按 op 层 IRootPath 约定传 RootFolderPath 本身。
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

	// 按 op 层约定：根 obj 的 Path = RootFolderPath 本身
	objs, err := d.List(ctx, &testObj{path: d.RootFolderPath, isDir: true}, model.ListArgs{})
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(objs) == 0 {
		t.Fatal("List 返回空列表")
	}
	idx := -1
	for i, o := range objs {
		t.Logf("list: %-20s size=%d dir=%v path=%s", o.GetName(), o.GetSize(), o.IsDir(), o.GetPath())
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
