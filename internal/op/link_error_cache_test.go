package op

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// failLinkDriver 的 Link 永远失败，用于验证负向缓存。
type failLinkDriver struct {
	driver.Driver
	storage model.Storage
	calls   int
	err     error
}

func (d *failLinkDriver) Config() driver.Config      { return driver.Config{} }
func (d *failLinkDriver) GetStorage() *model.Storage { return &d.storage }
func (d *failLinkDriver) Get(context.Context, string) (model.Obj, error) {
	return &model.Object{Name: "file"}, nil
}
func (d *failLinkDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	d.calls++
	return nil, d.err
}

// TestLinkErrorIsCachedForOneMinute 验证取链失败后 1 分钟内不再打上游。
func TestLinkErrorIsCachedForOneMinute(t *testing.T) {
	resetLinkLifecycleState(t)
	upstream := errors.New("upstream boom")
	d := &failLinkDriver{err: upstream}
	d.storage = model.Storage{MountPath: "/fail"}
	ctx := context.Background()

	_, _, err := Link(ctx, d, "/fail/file", model.LinkArgs{})
	if !errors.Is(err, upstream) && err == nil {
		t.Fatalf("首次应失败，得到 %v", err)
	}
	if d.calls != 1 {
		t.Fatalf("首次应调用上游 1 次，实际 %d", d.calls)
	}

	// 第二次：命中负向缓存，不再调用上游
	_, _, err2 := Link(ctx, d, "/fail/file", model.LinkArgs{})
	if err2 == nil {
		t.Fatal("第二次应返回缓存错误")
	}
	if d.calls != 1 {
		t.Fatalf("负向缓存未生效：上游被调用 %d 次（应为 1）", d.calls)
	}
}

// TestLinkSuccessClearsErrorCache 验证成功后清掉负向缓存。
func TestLinkSuccessClearsErrorCache(t *testing.T) {
	resetLinkLifecycleState(t)
	upstream := errors.New("temporary failure")
	d := &flakyLinkDriver{err: upstream}
	d.storage = model.Storage{MountPath: "/flaky"}
	ctx := context.Background()

	// 第一次失败 -> 写入负向缓存
	_, _, err := Link(ctx, d, "/flaky/file", model.LinkArgs{})
	if err == nil {
		t.Fatal("首次应失败")
	}
	if d.calls != 1 {
		t.Fatalf("上游调用 %d 次，应为 1", d.calls)
	}

	// 恢复正常后，清掉负向缓存应能立即成功（模拟缓存过期后的重试）
	Cache.linkErrCache.Clear()
	d.err = nil
	link, _, err := Link(ctx, d, "/flaky/file", model.LinkArgs{})
	if err != nil {
		t.Fatalf("清除负向缓存后应成功: %v", err)
	}
	if link == nil || link.URL == "" {
		t.Fatal("应返回有效链接")
	}
}

// flakyLinkDriver 的 Link 可按需成功或失败。
type flakyLinkDriver struct {
	driver.Driver
	storage model.Storage
	calls   int
	err     error
}

func (d *flakyLinkDriver) Config() driver.Config      { return driver.Config{} }
func (d *flakyLinkDriver) GetStorage() *model.Storage { return &d.storage }
func (d *flakyLinkDriver) Get(context.Context, string) (model.Obj, error) {
	return &model.Object{Name: "file"}, nil
}
func (d *flakyLinkDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return &model.Link{URL: "https://example.com/file"}, nil
}
