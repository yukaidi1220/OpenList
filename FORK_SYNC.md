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

## xrgzs 重建 main 的含义

xrgzs 每个版本（如 v26.10.22.0）= 官方某个时点的历史 + 一个包含全部增强的 squash 提交，旧 squash 会被 force-push 掉。因此：

- 本地旧提交（如已被替换的 squash）不受影响，但**不要**基于 `upstream/main` 的旧位置建长期分支。
- 合并新版本 squash 时，之前 squash 的内容与新 squash 大部分相同，git 会自动抵消；真正要处理的是官方新改动与 fork 自有改动的重叠。
