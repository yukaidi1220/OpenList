# OpenList + rclone 镜像更新方法

两种场景：**更新的内容在哪**决定了要重建还是只重启。

## 场景 A：只改了 rclone（fork）

rclone 是构建期从 GitHub Release 下载并烧进镜像的，所以 fork 更新后必须：

```bash
# 1) 在 rclone fork 打新 tag 触发 CI 出 release（CI 自动构建 4 平台资产）
cd <rclone-fork>
git tag v1.76.0-cdn.NN origin/main && git push origin v1.76.0-cdn.NN
# 等 "Build & Release" workflow 绿，确认 release 有 rclone-cdn-linux-amd64

# 2) 把镜像 Dockerfile 里的版本号指向新 tag
#    D:\...\OpenList\Dockerfile.rclone
ARG RCLONE_DOWNLOAD_URL=.../download/v1.76.0-cdn.NN/rclone-cdn-linux-amd64

# 3) 提 PR 合并到 OpenList fork 的 main

# 4) 触发镜像构建（xrsys-maker 仓的 workflow，构建完自动推 GHCR）
gh workflow run build-openlist-rclone-image.yml -R yukaidi1220/xrsys-maker --ref main
# 等它绿（约 7-8 分钟，含冒烟校验）

# 5) 服务器更新
ssh root@<服务器> 'cd /opt/openlist-rclone && docker compose pull && docker compose up -d'
```

## 场景 B：只改了 OpenList（驱动代码等）

镜像里的 openlist 二进制是构建期从 OpenList fork 的 main 编译的，所以：

```bash
# 1) 改动 PR 合并到 OpenList fork 的 main
# 2) 触发镜像构建（同上第 4 步）——无需动 Dockerfile
# 3) 服务器更新（同上第 5 步）
```

## 场景 C：只改配置（rclone.conf / compose）

**不用重建镜像**，直接改文件重启：

```bash
cd /opt/openlist-rclone
# 改 rclone-config/rclone.conf 或 docker-compose.yml
docker compose up -d          # 配置类改动只需重启容器
docker exec openlist-rclone cat /var/log/rcd.log | tail   # 确认 rcd 起来了
```

⚠️ **rcd 的 token 不会自动重载**：改了 rclone.conf 里的凭据后必须重启容器（或 `docker exec openlist-rclone pkill -f "rclone rcd"` 让守护循环重拉），否则 rcd 内还是旧 token，取链会报 `Login required` / 40007 之类的假故障。

## 验证更新是否生效

```bash
cd /opt/openlist-rclone

# 1) 镜像构建时间（应是刚才的时间）
docker image inspect ghcr.io/yukaidi1220/openlist-rclone:latest --format 'BuiltAt={{.Created}}'

# 2) 容器用的镜像与启动时间
docker inspect openlist-rclone --format 'Image={{.Config.Image}} Started={{.State.StartedAt}}'

# 3) 镜像内 rclone 版本（确认是新的）
docker exec openlist-rclone /opt/rclone/rclone version | head -1

# 4) 双进程 + 健康
docker ps --format '{{.Names}} {{.Status}}'
docker exec openlist-rclone sh -c 'ps -ef | grep -c "[r]clone rcd"; wget -qO- http://127.0.0.1:5244/ping'

# 5) 取链冒烟（四 remote）
docker exec openlist-rclone /opt/rclone/rclone rc \
  --url http://127.0.0.1:5572 --rc-user <user> --rc-pass <pass> \
  operations/publiclink fs=cloudreve: remote=<某文件路径>
```

## 回滚

```bash
cd /opt/openlist-rclone
# 用带版本的 tag
docker compose down
sed -i 's|openlist-rclone:latest|openlist-rclone:<旧run_number>|' docker-compose.yml
docker compose up -d
```
GHCR 上每次构建留三个 tag：`latest`、`<run_number>`、`sha-<短sha>`。前两个按构建序号，
好念但看不出对应哪次提交；`sha-` 那个能直接反查源码，事后追根因用它。
