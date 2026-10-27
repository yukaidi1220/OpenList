package rclone

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 缓存时长规则（用户定义，按顺序匹配，命中即返回）：
//  1. wopan（wo.cn 域名）：个人盘/家庭盘有效期均 120 分钟，链接不含有效期参数 → 缓存 30 分钟
//  2. 链接带有效期且 < 60 分钟：最多缓存有效期的 20%
//  3. 60 分钟 <= 有效期 < 180 分钟：缓存 = 有效期 - 40 分钟
//  4. 有效期 >= 180 分钟：最多缓存有效期的 70%
//  5. 解析不出有效期：缓存 2 分钟
//
// 有效期来源（实测各盘链接形态）：
//   - S3v4 签名（cloudreve / yun139）：X-Amz-Date(YYYYMMDDTHHMMSSZ) + X-Amz-Expires(秒)
//   - cloud189：expired（毫秒时间戳，绝对过期时刻，无签链时间）
//   - 其他：尝试 expired/expires/Expires（秒或毫秒时间戳）与 X-Amz-Expires
//
// defaultDirCacheMinutes 目录列表缓存默认时长（分钟）
const defaultDirCacheMinutes = 5

const (
	linkTTLUnknown    = 2 * time.Minute  // 解析不出有效期
	linkTTLWopan      = 30 * time.Minute // 联通盘固定
	linkTTLShortRatio = 0.20             // <60min：最多 20%
	linkTTLMidMargin  = 40 * time.Minute // [60,180)min：有效期 - 40min
	linkTTLLongRatio  = 0.70             // >=180min：最多 70%
)

// linkTTLFor 根据链接推算 302 直链缓存时长。
func linkTTLFor(rawURL string, now time.Time) time.Duration {
	u, err := url.Parse(rawURL)
	if err != nil {
		return linkTTLUnknown
	}
	// 1) 联通盘：wo.cn 域名，链接无有效期参数
	if strings.Contains(strings.ToLower(u.Host), "wo.cn") {
		return linkTTLWopan
	}

	validity, ok := linkValidity(u, now)
	if !ok || validity <= 0 {
		return linkTTLUnknown
	}

	switch {
	case validity < 60*time.Minute:
		// 2) 不足 60 分钟：最多 20%
		return time.Duration(float64(validity) * linkTTLShortRatio)
	case validity < 180*time.Minute:
		// 3) [60,180) 分钟：有效期 - 40 分钟（validity >= 60 保证结果 >= 20 分钟）
		return validity - linkTTLMidMargin
	default:
		// 4) >= 180 分钟：最多 70%
		return time.Duration(float64(validity) * linkTTLLongRatio)
	}
}

// linkValidity 从链接参数推算剩余有效期（相对 now）。
// 支持：X-Amz-Date + X-Amz-Expires（S3v4）、expired/expires 绝对时间戳（秒或毫秒）。
func linkValidity(u *url.URL, now time.Time) (time.Duration, bool) {
	q := u.Query()

	// S3v4：签名时刻 + 有效期秒数
	if amzExpires := q.Get("X-Amz-Expires"); amzExpires != "" {
		secs, err := strconv.ParseInt(amzExpires, 10, 64)
		if err != nil || secs <= 0 {
			return 0, false
		}
		// 剩余有效期 = 签名时刻 + Expires - now；有签名时刻就用它，
		// 否则按"刚签发"处理（保守：直接用 Expires）
		if amzDate := q.Get("X-Amz-Date"); amzDate != "" {
			if signedAt, err := time.Parse("20060102T150405Z", amzDate); err == nil {
				return signedAt.Add(time.Duration(secs) * time.Second).Sub(now), true
			}
		}
		return time.Duration(secs) * time.Second, true
	}

	// 绝对过期时间戳：expired / expires / Expires（秒或毫秒）
	for _, key := range []string{"expired", "expires", "Expires"} {
		if v := q.Get(key); v != "" {
			ts, err := strconv.ParseInt(v, 10, 64)
			if err != nil || ts <= 0 {
				continue
			}
			// 13 位按毫秒，10 位按秒
			var deadline time.Time
			if ts > 1e12 {
				deadline = time.UnixMilli(ts)
			} else {
				deadline = time.Unix(ts, 0)
			}
			return deadline.Sub(now), true
		}
	}
	return 0, false
}
