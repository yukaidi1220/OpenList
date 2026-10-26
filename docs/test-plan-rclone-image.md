# OpenList + rclone 同容器：新服务器部署测试计划

> 配套文档：[deploy-rclone-image.md](deploy-rclone-image.md)（部署步骤）。
> 本计划的目标：在全新服务器上跑通部署全流程，**验证文档是否过于理想化**——
> 每一个与文档不符的点都记录下来并回写文档。

## 测试范围

部署 OpenList+rclone 集成镜像（`ghcr.io/yukaidi1220/openlist-rclone`），挂载
Rclone 驱动的四个 remote（**yun139 / cloud189 / wopan / cloudreve**），验证：

1. 目录列表 + **目录缓存**生效
2. 文件下载 302 直链 + **直链缓存**生效
3. **hide_zero_size**：0 字节文件不列出（开关双向）
4. 其他：容器重启持久性、rcd 崩溃自愈、400m 内存下的表现、四 remote 并发

明确**不在范围**：上传测试（drivers/Rclone 是 `NoUpload: true` 只读驱动——
文档需写明）；构建产物归档（构建由他人负责）。

## 阶段 0：本地预准备（开始前完成）

| # | 事项 | 产物 |
|---|---|---|
| 0.1 | 镜像经 xrsys-maker `build-openlist-rclone-image.yml` 推到 GHCR | `ghcr.io/yukaidi1220/openlist-rclone:latest` |
| 0.2 | 从机密仓本地副本提取四段 remote 配置（**只在本机操作，不打印内容**） | 合并后的 rclone.conf |
| 0.3 | netcatty 连上服务器，`get_environment` 确认可达 | 会话句柄 |

## 阶段 1：系统初始化（坑点高发区）

| # | 步骤 | 预判的坑 |
|---|---|---|
| 1.1 | 识别发行版（`cat /etc/os-release`） | 可能不是 Debian/Ubuntu（CentOS/OpenEuler/Alpine？） |
| 1.2 | 系统更新（apt/dnf/yum 按发行版） | 国内源慢 → 换镜像源；证书过期 |
| 1.3 | 装 Docker + compose 插件 | 官方源被墙 → 阿里云镜像；`sudo` 缺失；systemd 不在 |
| 1.4 | 网络体检：curl github.com / ghcr.io / goproxy.cn 各测连通与速度 | 决定阶段 2 走哪条路线 |

**记录**：每个坑的实际解法（回写本文档第 9 节）。

## 阶段 2：镜像获取（双路线）

- **主线（0.1 的 GHCR 镜像）**：`docker pull ghcr.io/yukaidi1220/openlist-rclone:latest`
  - 坑预判：ghcr.io 国内直连可能慢/超时 → 配 registry mirror 或 docker proxy
- **备线（实测文档理想化程度）**：服务器上 `git clone + docker build -f Dockerfile.rclone`
  - 预判坑（已部分缓解）：
    - `go mod download` 挂 proxy.golang.org → 已加 `GOPROXY=goproxy.cn`（PR #20）
    - **前端 tarball 从 github.com 下载（FetchWebRolling）→ 无法从 build.sh 外部代理，
      大概率失败**——若失败，记录后放弃服务器构建，走主线（结论回写文档：国内服务器
      构建不可行，必须 GHCR）

## 阶段 3：配置与启动

| # | 步骤 | 预判的坑 |
|---|---|---|
| 3.1 | `/opt/openlist-rclone/rclone-config/rclone.conf`（四 remote，600 权限） | 凭据传输方式（netcatty sftp vs heredoc）；换行符 CRLF 污染 |
| 3.2 | compose（400m、127.0.0.1:5244、RUN_RCLONE=true） | 5244 被占；`mem_limit` 需要 compose v2 |
| 3.3 | 启动 → `docker ps` 双进程 → ping → rcd.log 无 CRITICAL | 400m 下 openlist 初始化 OOM？ |

## 阶段 4：OpenList 初始化 + 挂载四驱动

| # | 步骤 | 预判的坑 |
|---|---|---|
| 4.1 | 取初始 admin 密码（容器日志），API 登录拿 token | 密码随机串含特殊字符 |
| 4.2 | API 创建 4 个 Rclone 存储（POST /api/admin/storage/create） | addition JSON 字段名与 meta.go 对齐；驱动名必须 `Rclone` |
| 4.3 | 每块存储的 root_folder_path：cloudreve=`File`；yun139/189/wopan=按 rclone conf 实际目录结构（先 `rclone lsd` 探） | 远端根目录结构与预想不同 |
| 4.4 | 逐存储 `POST /api/fs/list` 冒烟 | rc 认证/凭据错误的报错样式 |

## 阶段 5：功能验证矩阵

> 每项都记录：请求方式、响应证据（状态码/头/耗时/日志行）。

### 5.1 目录列表 + 目录缓存（×4 remote）

1. `POST /api/fs/list`（refresh=false）首请求 → 200 + 条目
2. 同参数第二次 → **命中缓存**的判据：响应时间显著下降（>50%）或 openlist 日志无上游 rc 调用
3. `refresh=true` 强刷 → 重新拉上游
- **坑预判**：rcd 首次列目录可能因凭据/token 慢（冷启动 5-10s）

### 5.2 文件 302 直链 + 直链缓存（×4 remote）

1. `GET /d/<mount>/<file>`（HEAD 或 -r 0-100）→ **302** + `Location` 头
2. 用 Location 真实下载（Range 1KB）→ 206/200 + 内容哈希与源一致（抽 1 个 remote 全量比对）
3. **直链缓存**：TTL 内第二次拿链接 → 签名参数/对象路径一致（缓存命中）；不同 remote 的
   链接时效不同（139/189 短时效，cloudreve 1h）——记录各自 TTL 表现
- **坑预判**：139/189 的直链可能需要特定 UA/Referer（OpenList 的 Link.Header 应带）；
  302 目标若被墙/失效，验证"重新取链"而不是直接判失败

### 5.3 hide_zero_size（重点：cloudreve + 任意一个有 0 字节文件的 remote）

1. 先在远端造一个 0 字节文件（rclone touch / openlist API）
2. `hide_zero_size=false` → list 中**出现**
3. `hide_zero_size=true` → list 中**不出现**
4. 开关切换不需要重建存储（改 addition 保存即可）——验证是否要重启
- **坑预判**：0 字节**目录**不能被过滤（isDir 排除逻辑已写，实测确认）

### 5.4 稳定性与自愈

| 项 | 操作 | 通过标准 |
|---|---|---|
| 容器重启持久性 | `docker restart` | 存储配置保留、自动重连、ping 恢复 |
| rcd 崩溃自愈 | 容器内 kill rclone 进程 | 守护 3s 内重启，5572 恢复 |
| runsv/aria2 无回归 | RUN_ARIA2=false 下 aria2 目录干净 | 不误启 |
| 400m 内存 | 大目录列表 + `docker stats` | 不 OOM；记录峰值 |
| 四 remote 并发 | 并行 list 四存储 ×3 轮 | 全部成功，无串扰 |

### 5.5 额外（时间允许）

- WebDAV 挂载冒烟（OpenList 自带 /dav）
- 400m vs 600m 对比（若 5.4 出现 OOM）
- 多轮 list 后 rcd.log 的错误率统计

## 阶段 6：回写与收尾

1. 所有与 `deploy-rclone-image.md` 不符的点 → 更新文档（版本化记录）
2. 四 remote 的 TTL/缓存行为表 → 追加到部署文档
3. 服务器上的临时凭据文件按需清理（或保留供长期运行）
4. 本文档标记最终状态（通过项/失败项/豁免项）

## 坑位日志（执行时实时填写）

| # | 阶段 | 现象 | 根因 | 解法 | 文档是否回写 |
|---|---|---|---|---|---|
| - | - | （待填） | | | |
