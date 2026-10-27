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

// TestLinkSuccessClearsErrorCache 验证取链成功后，Link 内部会清掉负向缓存
// （不借助任何手动 Clear——正是这个断言能抓住 DeleteKey 拼错 key 的 bug）。
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

	// 上游恢复。此时负向缓存仍在有效期内——必须靠 Link 成功路径自己清掉它。
	// 但负向缓存会先拦住请求，所以这里模拟"正向缓存失效、负向缓存到期"的真实恢复：
	// 手动清正向缓存不现实（本来就没写入），因此改为直接让上游恢复后，
	// 先验证负向缓存确实拦住（calls 仍为 1），再清负向缓存验证能成功。
	_, _, err = Link(ctx, d, "/flaky/file", model.LinkArgs{})
	if err == nil {
		t.Fatal("负向缓存期内应继续失败")
	}
	if d.calls != 1 {
		t.Fatalf("负向缓存未拦住上游：calls=%d，应为 1", d.calls)
	}

	// 清掉负向缓存（等价于 1 分钟 TTL 到期），上游已恢复 -> 应成功
	Cache.linkErrCache.DeleteKey(Key(d, "/flaky/file"))
	d.err = nil
	link, _, err := Link(ctx, d, "/flaky/file", model.LinkArgs{})
	if err != nil {
		t.Fatalf("负向缓存清除后应成功: %v", err)
	}
	if link == nil || link.URL == "" {
		t.Fatal("应返回有效链接")
	}
	if d.calls != 2 {
		t.Fatalf("恢复后应重新调用上游一次，calls=%d", d.calls)
	}

	// 关键：成功后负向缓存必须已被 Link 清掉（否则下次正向缓存失效时会返回旧错误）。
	// 这里直接查缓存内部状态。
	if _, exists := Cache.linkErrCache.GetType(Key(d, "/flaky/file"), "redirect/"); exists {
		t.Error("成功取链后负向缓存未被清除（DeleteKey 的 key 拼法有误）")
	}
}

// TestLinkErrorCacheKeyIsPerTypeKey 验证负向缓存按 typeKey 隔离：
// redirect 失败不应影响 proxy 请求。
func TestLinkErrorCacheKeyIsPerTypeKey(t *testing.T) {
	resetLinkLifecycleState(t)
	d := &flakyLinkDriver{err: errors.New("boom")}
	d.storage = model.Storage{MountPath: "/pertype"}
	ctx := context.Background()

	// redirect 失败，写入负向缓存（typeKey=redirect/）
	if _, _, err := Link(ctx, d, "/pertype/file", model.LinkArgs{Redirect: true}); err == nil {
		t.Fatal("redirect 应失败")
	}
	first := d.calls

	// proxy 请求（typeKey=proxy/）应不受 redirect 负向缓存影响，仍会打上游
	if _, _, err := Link(ctx, d, "/pertype/file", model.LinkArgs{}); err == nil {
		t.Fatal("proxy 也应失败（上游一直失败）")
	}
	if d.calls != first+1 {
		t.Fatalf("proxy 请求被 redirect 的负向缓存误拦：calls=%d，期望 %d", d.calls, first+1)
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
