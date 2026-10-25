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

// toRclonePath 把 OpenList 虚拟路径映射为 remote 内的路径
// （remote_name + root_folder_path 是存储根，虚拟 / 对应它）
func (d *Rclone) toRclonePath(p string) string {
	rel := strings.TrimPrefix(p, "/")
	rel = strings.TrimPrefix(rel, ".")
	if rel == "" {
		return d.RootFolderPath
	}
	if d.RootFolderPath == "" {
		return rel
	}
	return stdpath.Join(d.RootFolderPath, rel)
}

func (d *Rclone) Init(ctx context.Context) error {
	if d.Endpoint == "" {
		return errors.New("endpoint 不能为空")
	}
	if d.RemoteName == "" {
		return errors.New("remote_name 不能为空")
	}
	if d.LinkViaExec && d.RcloneBin == "" {
		return errors.New("启用 link_via_exec 时必须配置 rclone_bin")
	}
	d.client = newRCClient(d.Endpoint, d.RCUser, d.RCPassword)
	// 用 about 同时校验 rcd 连通性与 remote 名称（一次调用查两个错）
	if err := d.client.call(ctx, "operations/about", map[string]any{"fs": d.fsName()}, nil); err != nil {
		return fmt.Errorf("rcd 连接或 remote 校验失败: %w", err)
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
	// 调用形状必须是"根 remote + arg 携带 remote 内全路径"——带 path 的目录根 Fs
	// 会让后端的 uriFor 解析出错（服务端报 Path not exist，实测踩过）
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
	u := strings.TrimSpace(stdout.String())
	// Windows 控制台可能带 NOTICE 前缀行，取最后一个 http 开头的 token
	for _, line := range strings.Split(u, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http") {
			u = line
			break
		}
	}
	if !strings.HasPrefix(u, "http") {
		return "", fmt.Errorf("rclone link 输出不是链接: %q", u)
	}
	return u, nil
}

var _ driver.Driver = (*Rclone)(nil)
