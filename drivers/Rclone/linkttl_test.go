package rclone

import (
	"testing"
	"time"
)

// TestLinkTTLFor 覆盖用户定义的缓存时长规则（顺序：wo.cn → 按有效期分档 → 未知）
func TestLinkTTLFor(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s3 := func(signedAt string, expiresSec int) string {
		return "https://bucket.example.com/a.bin?X-Amz-Date=" + signedAt +
			"&X-Amz-Expires=" + itoa(expiresSec) + "&X-Amz-Signature=deadbeef"
	}

	cases := []struct {
		name string
		url  string
		want time.Duration
	}{
		// 1) 联通盘 wo.cn：固定 30 分钟（链接不带有效期参数）
		{"wopan wo.cn", "https://hydownload.pan.wo.cn/openapi/download?fid=abc", 30 * time.Minute},
		{"wopan wo.cn 带无关参数", "https://x.wo.cn/openapi/download?fid=abc&t=1", 30 * time.Minute},

		// 2) 有效期 < 60 分钟：最多 20%
		// yun139 个人盘 15 分钟 -> 3 分钟
		{"15min -> 20%", s3("20261027T120000Z", 15*60), 3 * time.Minute},
		// cloudreve 3599s (~59.98min) -> 约 12 分钟
		{"3599s -> 20%", s3("20261027T120000Z", 3599), time.Duration(float64(3599*time.Second) * 0.2)},

		// 3) [60, 180) 分钟：有效期 - 40 分钟
		// 60 分钟整 -> 20 分钟
		{"60min -> -40", s3("20261027T120000Z", 3600), 20 * time.Minute},
		// 120 分钟（wopan 若带参数）-> 80 分钟
		{"120min -> -40", s3("20261027T120000Z", 7200), 80 * time.Minute},
		// 179 分钟 -> 139 分钟
		{"179min -> -40", s3("20261027T120000Z", 179*60), 139 * time.Minute},

		// 4) >= 180 分钟：最多 70%
		// 180 分钟 -> 126 分钟
		{"180min -> 70%", s3("20261027T120000Z", 180*60), 126 * time.Minute},
		// 24 小时（yun139 家庭盘）-> 1008 分钟
		{"24h -> 70%", s3("20261027T120000Z", 86400), time.Duration(float64(24*time.Hour) * 0.7)},

		// 5) cloud189：expired 毫秒绝对时间戳
		// 90 分钟后过期 -> [60,180) 档 -> 50 分钟
		{"189 90min 剩余", "https://d.cloud.189.cn/f?a=1&expired=" + itoa64(now.Add(90*time.Minute).UnixMilli()), 50 * time.Minute},
		// 30 分钟后过期 -> <60 档 -> 6 分钟
		{"189 30min 剩余", "https://d.cloud.189.cn/f?a=1&expired=" + itoa64(now.Add(30*time.Minute).UnixMilli()), 6 * time.Minute},

		// 6) 解析不出有效期 -> 2 分钟
		{"无参数", "https://example.com/a.bin", 2 * time.Minute},
		{"参数不可解析", "https://example.com/a.bin?X-Amz-Expires=abc", 2 * time.Minute},
	}

	for _, c := range cases {
		got := linkTTLFor(c.url, now)
		if got != c.want {
			t.Errorf("%s: linkTTLFor = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestLinkTTLForSignedInPast 签名时刻已过：剩余有效期可能为负，应回落到未知档
func TestLinkTTLForSignedInPast(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	// 1 小时前签发、有效期 30 分钟 -> 已过期
	u := "https://b.example.com/a?X-Amz-Date=20261027T110000Z&X-Amz-Expires=1800"
	if got := linkTTLFor(u, now); got != linkTTLUnknown {
		t.Errorf("过期链接应回落 2 分钟，得到 %v", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func itoa64(n int64) string { return itoa(int(n)) }
