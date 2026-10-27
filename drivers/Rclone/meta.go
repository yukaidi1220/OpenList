package rclone

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	driver.RootPath
	// rcd 是 rclone 的远程控制守护进程（rclone rcd），本驱动所有操作都走它的 rc HTTP API
	Endpoint string `json:"endpoint" required:"true" type:"text" help:"rclone rcd 地址，例如 http://127.0.0.1:5572（末尾斜杠可省略）"`
	// rcd 未配置认证（未设 --rc-user）时留空即可
	RCUser     string `json:"rc_user" required:"false" type:"text" help:"rcd 的 --rc-user，未配置认证则留空"`
	RCPassword string `json:"rc_password" required:"false" type:"text" secret:"true" help:"rcd 的 --rc-pass，与 rc_user 成对出现"`
	// rclone remote 名称（rclone.conf 里的段名），不含冒号和路径
	RemoteName string `json:"remote_name" required:"true" type:"text" help:"rclone remote 名称（rclone.conf 中的段名），例如 crtest；不能以 - 开头"`
	// 根目录下 0 字节文件通常是云盘的占位/目录映射产物，列出会刷屏
	HideZeroSize bool `json:"hide_zero_size" required:"false" type:"bool" default:"false" help:"不列出 0 字节文件"`
}

var config = driver.Config{
	Name:        "Rclone",
	DefaultRoot: "/",
	NoUpload:    true,
	LocalSort:   true,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Rclone{}
	})
}
