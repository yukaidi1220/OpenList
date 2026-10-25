package rclone

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	driver.RootPath
	// rcd 是 rclone 的远程控制守护进程（rclone rcd），本驱动所有操作都走它的 rc HTTP API
	Endpoint string `json:"endpoint" required:"true" help:"rclone rcd 地址，例如 http://127.0.0.1:5572"`
	// rcd 未配置认证（未设 --rc-user）时留空即可
	RCUser     string `json:"rc_user" required:"false" help:"rcd 的 --rc-user，未配置认证则留空"`
	RCPassword string `json:"rc_password" required:"false" confidential:"true" help:"rcd 的 --rc-pass"`
	// rclone remote 名称（rclone.conf 里的段名），不含冒号和路径
	RemoteName string `json:"remote_name" required:"true" help:"rclone remote 名称（rclone.conf 中的段名），例如 crtest"`
	// 取链回退：rclone 官方 rc 没有 publiclink 端点（backend/command 只分发后端自己的
	// Command 方法）。fork 已把 publiclink 加进 Command 分发时走主路径；否则用本地
	// rclone 子进程执行 link 命令兜底。
	LinkViaExec bool   `json:"link_via_exec" required:"false" default:"false" help:"rcd 取链失败时用本地 rclone 子进程执行 link 兜底"`
	RcloneBin   string `json:"rclone_bin" required:"false" help:"rclone 可执行文件路径（link_via_exec 时必填）"`
	RcloneConf  string `json:"rclone_conf" required:"false" help:"rclone 配置文件路径（link_via_exec 时必填）"`
}

var config = driver.Config{
	Name:        "Rclone",
	DefaultRoot: "/",
	NoUpload:    true,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Rclone{}
	})
}
