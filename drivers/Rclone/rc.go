package rclone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// rcClient 是 rclone rcd 的 rc HTTP API 最小客户端。
// rc 协议：POST {endpoint}/{method}，body 为参数 JSON；响应 200 且 body 形如
// {"result": ...}，失败时为 {"error": "...", "input": ...}（HTTP 4xx/5xx）。
type rcClient struct {
	endpoint string
	user     string
	password string
	httpCli  *http.Client
}

func newRCClient(endpoint, user, password string) *rcClient {
	// 用户手填尾斜杠会让 URL 变 //operations/...，rclone mux 直接 404
	endpoint = strings.TrimSuffix(endpoint, "/")
	return &rcClient{
		endpoint: endpoint,
		user:     user,
		password: password,
		httpCli: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// call 调用一个 rc 端点，params 序列化为 JSON body，result 反序列化到 out（可为 nil）。
func (c *rcClient) call(ctx context.Context, method string, params map[string]any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("rc 参数序列化失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("rc 请求构造失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.password != "" {
		req.SetBasicAuth(c.user, c.password)
	}
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("rc 请求失败（%s）: %w", method, err)
	}
	defer resp.Body.Close()
	// operations/list 无分页，响应体一次全量返回；给个宽松上限并在超限时显式报错，
	// 而不是让静默截断把 JSON 撕坏报出莫名其妙的解析错误
	const maxBody = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("rc 响应读取失败: %w", err)
	}
	if len(raw) > maxBody {
		return fmt.Errorf("rc %s 响应超过 %dMB 上限（目录过大）", method, maxBody>>20)
	}
	if resp.StatusCode != http.StatusOK {
		env := struct {
			Error string `json:"error"`
		}{}
		_ = json.Unmarshal(raw, &env)
		if env.Error == "" {
			env.Error = string(raw)
		}
		return fmt.Errorf("rc %s 失败（HTTP %d）: %s", method, resp.StatusCode, env.Error)
	}
	// 注意：rc 成功响应没有统一信封——body 即各端点自己的返回值
	// （operations/list 是 {"list":[...]}；backend/command 的 {"result":...}
	// 是该 handler 自行包裹的一层）
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("rc %s 响应解析失败: %w", method, err)
		}
	}
	return nil
}
