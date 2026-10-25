package rclone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	stdpath "path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	log "github.com/sirupsen/logrus"
)

type Rclone struct {
	model.Storage
	Addition
	client *rcClient
}

func (d *Rclone) Config() driver.Config {
	return config
}

func (d *Rclone) GetAddition() driver.Additional {
	return &d.Addition
}

// fsName 组出 rclone 的 fs 参数（remote 名 + 冒号）
func (d *Rclone) fsName() string {
	return d.RemoteName + ":"
}

// toRclonePath 把 OpenList 传来的路径映射为 remote 内路径。
// 注意 IRootPath 约定（对照 webdav/s3 驱动）：op 层传入和驱动产出的 obj.Path
// 都已含 root_folder_path 前缀（根目录 obj 的 Path 就是 RootFolderPath 本身），
// 所以这里只做清洗直通，绝不能再 Join 一次根——否则双拼，存储直接打不开。
func (d *Rclone) toRclonePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.Trim(p, "/")
}

func (d *Rclone) Init(ctx context.Context) error {
	if d.Endpoint == "" {
		return errors.New("endpoint 不能为空")
	}
	if d.RemoteName == "" {
		return errors.New("remote_name 不能为空")
	}
	if strings.HasPrefix(d.RemoteName, "-") {
		return errors.New("remote_name 不能以 - 开头（会被 rclone 当作 flag）")
	}
	if d.LinkViaExec {
		if d.RcloneBin == "" {
			return errors.New("启用 link_via_exec 时必须配置 rclone_bin")
		}
		if d.RcloneConf == "" {
			return errors.New("启用 link_via_exec 时必须配置 rclone_conf（子进程的 remote 必须与 rcd 一致）")
		}
	}
	d.client = newRCClient(d.Endpoint, d.RCUser, d.RCPassword)
	// 用 about 校验 rcd 连通性与 remote 名称；再用根路径 list 校验 root_folder_path
	// （about 不触碰路径，root 配错要到首次 List 才暴露，提前在这里拦）
	if err := d.client.call(ctx, "operations/about", map[string]any{"fs": d.fsName()}, nil); err != nil {
		return fmt.Errorf("rcd 连接或 remote 校验失败: %w", err)
	}
	var out struct {
		List []rcListObj `json:"list"`
	}
	if err := d.client.call(ctx, "operations/list", map[string]any{
		"fs": d.fsName(), "remote": d.toRclonePath("/"),
	}, &out); err != nil {
		return fmt.Errorf("根目录 list 失败（检查 remote_name 与 root_folder_path）: %w", err)
	}
	return nil
}

func (d *Rclone) Drop(ctx context.Context) error {
	return nil
}

type rcListObj struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	Path     string `json:"Path"`
	Size     int64  `json:"Size"`
	ModTime  string `json:"ModTime"`
	IsDir    bool   `json:"IsDir"`
	MimeType string `json:"MimeType"`
}

func (d *Rclone) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	var out struct {
		List []rcListObj `json:"list"`
	}
	params := map[string]any{"fs": d.fsName(), "remote": d.toRclonePath(dir.GetPath())}
	if err := d.client.call(ctx, "operations/list", params, &out); err != nil {
		return nil, err
	}
	res := make([]model.Obj, 0, len(out.List))
	for _, it := range out.List {
		var mod time.Time
		if it.ModTime != "" {
			if t, err := time.Parse(time.RFC3339, it.ModTime); err == nil {
				mod = t
			} else {
				log.Debugf("[rclone] ModTime 解析失败（置零）: %q", it.ModTime)
			}
		}
		res = append(res, &model.Object{
			Path:     stdpath.Join(dir.GetPath(), it.Name),
			Name:     it.Name,
			Size:     it.Size,
			Modified: mod,
			IsFolder: it.IsDir,
			ID:       it.ID,
		})
	}
	return res, nil
}

func (d *Rclone) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	rpath := d.toRclonePath(file.GetPath())
	// 主路径：fork 给 backend/command 加了 publiclink 分发。
	// 调用形状：根 remote + arg 携带 remote 内全路径（backend/command 的 handler
	// 只读 fs/command/opt/arg，没有 path 参数；带目录根的 fs 会让 uriFor 解析错位）
	var out struct {
		Result string `json:"result"`
	}
	err := d.client.call(ctx, "backend/command", map[string]any{
		"command": "publiclink",
		"fs":      d.fsName(),
		"arg":     []string{rpath},
	}, &out)
	if err == nil && out.Result != "" {
		return &model.Link{URL: out.Result}, nil
	}
	if err == nil {
		err = errors.New("rcd publiclink 返回了空结果")
	}
	// 回退：本地 rclone 子进程执行 link
	if !d.LinkViaExec {
		return nil, fmt.Errorf("rcd 取链失败（未启用 link_via_exec 回退）: %w", err)
	}
	log.Debugf("[rclone] rc 取链失败，走 exec 回退: %v", err)
	u, err := d.linkViaExec(ctx, rpath)
	if err != nil {
		return nil, err
	}
	return &model.Link{URL: u}, nil
}

// linkViaExec 通过本地 rclone 子进程执行 link 命令（PublicLinker 接口在 CLI 侧始终可用）
func (d *Rclone) linkViaExec(ctx context.Context, rpath string) (string, error) {
	execCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(execCtx, d.RcloneBin, "--config", d.RcloneConf, "link", d.fsName()+rpath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("rclone link 执行失败: %w（stderr: %s）", err, strings.TrimSpace(stderr.String()))
	}
	// NOTICE 行可能混在输出里，取第一个 http(s) 开头的行
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			return line, nil
		}
	}
	return "", fmt.Errorf("rclone link 输出不是链接: %q", strings.TrimSpace(stdout.String()))
}

var _ driver.Driver = (*Rclone)(nil)
