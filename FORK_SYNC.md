# Fork 同步流程

本仓库（yukaidi1220/OpenList）同时跟踪两个上游：

| remote | 仓库 | 说明 |
|---|---|---|
| `upstream` | xrgzs/OpenList | 增强版，**每次发版会用单个 squash 提交重建 main（force-push）** |
| `openlistteam` | OpenListTeam/OpenList | 官方原版 |

自己的工作以少量主题化提交直接放在 main 上（见 2026-10-25 的历史重建）。

## 日常同步步骤

```bash
git fetch upstream openlistteam

# 同步 xrgzs（通常每次只有 1 个新的版本 squash 提交）
git merge upstream/main

# 同步官方（正常合并即可）
git merge openlistteam/main

# 解决冲突后验证
go build ./...
go test ./internal/... ./server/webdav/... ./server/s3/...

git push origin main
```

## 冲突注意事项

- `drivers/139`、`drivers/189pc`、`server/webdav`、`internal/db/db.go` 是本 fork 与上游改动重叠的高发区。
- `internal/db/db.go`：合并时确认 AutoMigrate 列表同时保留 `model.CronJob`（xrgzs）和官方的 `model.Media*` 系列，且 `Init` 末尾保留 `initIPDB()`。
- `.github/workflows/build.yml`：xrgzs 基底删除了它（fork 使用自己的 beta_release 工作流），官方每次更新它都会产生 modify/delete 冲突，保持删除即可。
- `go.mod`/`go.sum` 冲突时不要手工合并，改完代码后跑 `go mod tidy` 重新生成。

## 前端渠道

构建时从 `$FRONTEND_REPO`（默认 xrgzs/OpenList-Frontend）下载 dist 并 `//go:embed` 进二进制。注意 xrgzs 前端的发布渠道：

- `edge`：现行渠道（跟随其 main 更新，多语言完整）
- `rolling`：旧渠道，2026-09 起停更且只含英语

`build.sh` 的 FetchWebRolling 已改为优先 `edge`、失败回退 `rolling`；合并上游 build.sh 时注意保留该改动。

## Docker 镜像

- `ghcr.io/yukaidi1220/openlist`（`beta`/`main`/`latest` tag）：由 test_docker.yml 每次 push 构建，另推一个 alist 兼容镜像。
- `ghcr.io/yukaidi1220/aria2`：由 aria2 仓库的 docker-image.yml 构建的运行时镜像（全静态 aria2c，amd64 + arm64 各用原生 runner 编译后合并 manifest，并带容器级 RPC 冒烟测试）。
- 部署示例见 docker-compose.yml：两容器以相同路径挂载 /downloads，OpenList 后台 aria2 RPC 指向 http://aria2:6800/jsonrpc。

## Release 策略

每次 push 到 main 自动发一个北京时间时间戳标签的 release（`YYYYMMDDHHmm`，北京时间），标记 Latest、非 prerelease；工作流自动只保留最近 3 个，更早的连同 tag 一起清理。

## xrgzs 重建 main 的含义

xrgzs 每个版本（如 v26.10.22.0）= 官方某个时点的历史 + 一个包含全部增强的 squash 提交，旧 squash 会被 force-push 掉。因此：

- 本地旧提交（如已被替换的 squash）不受影响，但**不要**基于 `upstream/main` 的旧位置建长期分支。
- 合并新版本 squash 时，之前 squash 的内容与新 squash 大部分相同，git 会自动抵消；真正要处理的是官方新改动与 fork 自有改动的重叠。
