# OpenList + rclone rcd 同容器部署指南

一个容器跑两个服务：OpenList（网盘前端）+ rclone rcd（驱动经 rc API 连它挂载任意 remote）。
镜像基于本仓库 `Dockerfile.rclone`，rclone 为 yukaidi1220 fork 构建（cloudreve 后端 +
`backend downloadurl` 取链）。

## 前置条件

- Docker（含 compose v2）
- Cloudreve 的凭据（本指南以 qzy-hk 为例：site、username、password 或 access_token）

## 1. 准备目录与 rclone 配置

```bash
mkdir -p /opt/openlist-rclone/{openlist-data,rclone-config}
cat > /opt/openlist-rclone/rclone-config/rclone.conf <<'EOF'
[cloudreve]
type = cloudreve
url = https://pan-qzyun-net.yukaidi.top
# 二选一：refresh_token（推荐，不会过期）
refresh_token = <从 OpenList 旧实例 data.db 的 x_storages.addition 里取，或重新授权>
# 或 username/password
EOF
chown -R 1001:1001 /opt/openlist-rclone/rclone-config
chmod 600 /opt/openlist-rclone/rclone-config/rclone.conf
```

> **属主要点（实测坑）**：容器以 openlist(1001) 运行。bind mount 的两个宿主目录
> **必须 chown 1001:1001**（含 rclone-config 目录本身）——compose 建目录默认 root:root，
> openlist 无写权限会启动即退出（`./data 无写权限`）；rclone-config 目录不可写会导致
> cloudreve 后端刷新 token 后无法回写配置（`permission denied` 疯狂刷屏）。

> 凭据来源：旧 OpenList 实例的 SQLite（`openlist/data/data.db`）表 `x_storages`，
> `addition` 列 JSON 里有 `refresh_token`/`access_token`。qzy-hk/qzy-cn/qzy-hk-mupan
> 三块都是 Cloudreve V4。

## 1.5 rclone 来源（重要）

镜像里的 rclone **必须是你 fork 的构建**（`yukaidi1220/rclone`，v1.76.0-cdn.23+）——
官方 rclone 没有 cloudreve 后端，也没有 `backend downloadurl` 取链命令。

Dockerfile 默认参数：
```
ARG RCLONE_DOWNLOAD_URL=https://github.com/yukaidi1220/rclone/releases/download/v1.76.0-cdn.23/rclone-cdn-linux-amd64
```
换版本：`docker build --build-arg RCLONE_DOWNLOAD_URL=<新地址> -f Dockerfile.rclone -t openlist-rclone .`

构建时会打印 `rclone version`，构建失败即报错。

## 2. 构建镜像（或用 CI 产物）

```bash
git clone https://github.com/yukaidi1220/OpenList.git && cd OpenList
git checkout main   # PR #4/#5 已合并
docker build -f Dockerfile.rclone -t openlist-rclone:latest .
```

## 3. compose 文件（内存限制 400m）

`docker-compose.yml`：

```yaml
services:
  openlist-rclone:
    image: openlist-rclone:latest
    restart: always
    container_name: openlist-rclone
    ports:
      # 只绑宿主 127.0.0.1：OpenList 面板（如需外网访问，前面加反代）
      # 对公网直接开放：改 '5244:5244' 并务必先改掉默认 admin 密码
      - '5244:5244'
    volumes:
      - './openlist-data:/opt/openlist/data'
      - './rclone-config:/config/rclone'
    environment:
      - UMASK=022
      - TZ=Asia/Shanghai
      - RUN_ARIA2=false
      - RUN_RCLONE=true
      # rcd 认证（与第 5 步挂载时填的一致；请改掉默认值）
      - RCLONE_RC_PORT=5572
      - RCLONE_RC_USER=openlist
      - RCLONE_RC_PASS=改成你自己的密码
    # ===== 内存限制 400m（cgroup 层，镜像层做不到）=====
    mem_limit: 400m
    memswap_limit: 400m      # 禁用 swap 超额，超限即 OOM kill 而非拖慢
    healthcheck:
      test: ['CMD', 'wget', '-qO-', 'http://127.0.0.1:5244/ping']
      interval: 30s
      timeout: 5s
      retries: 3
```

> **400m 的现实预期**：OpenList 空载 ~100-150MB，rcd 空载 ~50-80MB，日常合计
> ~200MB。但 rclone 在大文件上传/下载（几 GB 的 ESD）时传输缓冲会明显增长，
> 400m 偏紧。如果 OOM 频发（`dmesg | grep -i oom`），优先调 rcd 缓冲：
> 在 rclone.conf 加 `[client]` 段不可行——rcd 的缓冲受 OpenList 侧并发控制，
> 实际策略是**减少 OpenList 里 Rclone 存储的并发**（挂载时把 `--transfers`
> 相关的多线程下载关掉/降低），或放宽到 600m。

## 4. 启动与验证

```bash
cd /opt/openlist-rclone
docker compose up -d
sleep 10

# 两个进程都活着
docker exec openlist-rclone ps aux | grep -E "openlist server|rclone rcd"

# rcd 认证连通
docker exec openlist-rclone /opt/rclone/rclone rc \
  --url http://127.0.0.1:5572 --rc-user openlist --rc-pass 改成你自己的密码 \
  operations/about fs=cloudreve:

# OpenList 面板
curl -s http://127.0.0.1:5244/ping
```

内存验证：

```bash
docker stats openlist-rclone --no-stream --format "{{.MemUsage}} ({{.MemPerc}})"
```

## 4.5 连接拓扑（OpenList ↔ rclone）

```
┌────────────── 容器 openlist-rclone ──────────────┐
│  OpenList (PID 1)           rclone rcd (守护)    │
│  监听 :5244                 监听 0.0.0.0:5572    │
│      │                          ↑                │
│      └──── rc HTTP API ─────────┘                │
│         endpoint=http://127.0.0.1:5572           │
└──────────────────────────────────────────────────┘
        ↓ 只有 5244 映射到宿主 127.0.0.1
```
- rcd **只监听容器内**，不对宿主暴露 5572（凭据面最小）
- OpenList 驱动填 `endpoint=http://127.0.0.1:5572` + `rc_user`/`rc_password`
- rcd 由 entrypoint 守护（单实例锁 + 崩溃 3 秒重试），日志在容器内 `/var/log/rcd.log`
- 查看：`docker exec openlist-rclone tail -f /var/log/rcd.log`

## 4.7 初始化 admin（重要，实测坑）

此 OpenList 构建在未初始化时**不会自动创建 admin 用户**（等 Web 安装向导），
CLI 的 `admin set/random` 只对已存在的用户生效。两种初始化方式：

```bash
# 方式 A：调 init API（推荐，脚本化）
curl -s -X POST http://127.0.0.1:5244/api/public/init/setup   -H "Content-Type: application/json"   -d '{"username":"admin","password":"你的密码","site_title":"EEEOS"}'

# 方式 B：设 OPENLIST_ADMIN_PASSWORD 环境变量后重建容器
```

## 5. 面板挂载 Rclone 存储

打开 `http://127.0.0.1:5244/@manage`（首次进面板的 admin 密码在
`docker logs openlist-rclone 2>&1 | grep -i password`），添加存储：

| 配置项 | 值 |
|---|---|
| 驱动 | Rclone |
| 挂载路径 | `/cr/qzy-hk`（同名挂载可复刻旧布局；root_folder_path 对应网盘内目录） |
| endpoint | `http://127.0.0.1:5572` |
| rc_user / rc_password | 同第 3 步环境变量 |
| remote_name | `cloudreve` |
| root_folder_path | `File`（制品）；另建一块填 `mupan`（母盘） |
| link_via_exec | 关（rcd 是 fork 构建时走 rc 主路径即可） |
| hide_zero_size | 按需 |

> **API/脚本化挂载注意**：`/api/admin/storage/update` 的 payload **必须带
> `driver` 字段**（否则报 `driver cannot be changed`）；创建时 init 失败的存储
> 会留在库里，rcd 修好后用 update 触发重新初始化即可，不必删除重建。

需要复刻旧实例的 `/cr/qzy-hk`、`/cr/qzy-cn`、`/cr/qzy-hk-mupan` 三个挂载：
同一 rclone.conf 里加 `[cloudreve-cn]`（qzy-cn 凭据）段，remote_name 分别填
`cloudreve` / `cloudreve-cn`，root_folder_path 均为 `File`；qzy-hk-mupan 的
root_folder_path 填 `mupan`。

## 6. 数据迁移（可选，从旧实例）

旧实例的挂载配置、用户、索引在旧 `openlist/data/data.db`。最省事的迁移：
停旧实例 → 把整个 `openlist/data/` 拷进新卷 → 但要把 Rclone 存储补进
`x_storages`（旧库没有这个驱动）——SQL 示例：

```sql
INSERT INTO x_storages (mount_path, driver, addition, modified, disabled, ... )
VALUES ('/cr/qzy-hk', 'Rclone',
  '{"endpoint":"http://127.0.0.1:5572","rc_user":"openlist","rc_password":"...",
    "remote_name":"cloudreve","root_folder_path":"File","link_via_exec":false,
    "hide_zero_size":true}', ...);
```

更推荐直接在新面板手动加三块存储（第 5 步），旧数据只迁用户和设置。

## 7. 故障排查

| 症状 | 检查 |
|---|---|
| 面板 502/ping 不通 | `docker logs openlist-rclone` 看 openlist 是否被 OOM kill（400m 偏紧时） |
| 存储报 rc 连接失败 | `docker exec openlist-rclone ps aux`——rcd 没起来看 `docker logs` 里 runsv 段；认证与环境变量是否一致 |
| 取链失败 command not found | rcd 用的二进制不是 fork 版（`docker exec openlist-rclone /opt/rclone/rclone version` 应为 v1.76.0-cdn.23+） |
| 容器反复重启 | `dmesg | grep oom`——400m 被 xfer 缓冲顶爆，按第 3 步说明调并发或放宽 |
| rcd 没起来 | `docker exec openlist-rclone cat /var/log/rcd.log`；常见是 rclone.conf 路径/凭据错 |
| 端口冲突 | rcd 绑 `0.0.0.0:5572`（容器内），与宿主无冲突；若容器内 5572 被占改 `RCLONE_RC_PORT` |
| 列目录 404/500 | root_folder_path 与 remote 实际布局不匹配（qzy-hk 网盘根下是 File/ 和 mupan/） |


## 8. 部署实测坑位汇总（2026-10-26 洛杉矶 Ubuntu 24.04 全流程验证）

| # | 现象 | 根因 | 解法 |
|---|---|---|---|
| 1 | 容器启动即退出，`./data 无写权限` | bind mount 宿主目录 root:root，容器内 openlist(1001) 无写权限 | `chown -R 1001:1001` 两个挂载目录（rclone-config 目录本身也要） |
| 2 | 登录 401，admin 用户不存在 | 未初始化时不自动建 admin（等 Web 向导），CLI admin set/random 只改密码不建号 | 调 `POST /api/public/init/setup` 或设 `OPENLIST_ADMIN_PASSWORD` |
| 3 | 容器 restart 后 rcd 永远起不来 | entrypoint 的 /tmp 锁跨 restart 残留，守护被误跳过 | 已在镜像内修复（移除锁）；自定义 entrypoint 时勿加跨重启锁 |
| 4 | cloudreve token 刷新后疯狂报 permission denied | rclone-config 目录不可写，rclone 无法回写配置 | 同 #1，目录 chown 1001 |
| 5 | storage/update 报 driver cannot be changed | update payload 没带 driver 字段 | update payload 带 `driver` 字段 |
| 6 | 面板正常但脚本访问 API 返回 HTML | storage/list 是 GET 不是 POST | GET + Authorization 头 |

已验证通过的完整矩阵见 [test-plan-rclone-image.md](test-plan-rclone-image.md)。
