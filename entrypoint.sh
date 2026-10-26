#!/bin/sh

umask ${UMASK}

if [ "$1" = "version" ]; then
  ./openlist version
else
  # Check file of /opt/openlist/data permissions for current user
  # 检查当前用户是否有当前目录的写和执行权限
  if [ -d ./data ]; then
    if ! [ -w ./data ] || ! [ -x ./data ]; then
  cat <<EOF
Error: Current user does not have write and/or execute permissions for the ./data directory: $(pwd)/data
Please visit https://doc.oplist.org/guide/installation/docker#for-version-after-v4-1-0 for more information.
错误：当前用户没有 ./data 目录（$(pwd)/data）的写和/或执行权限。
请访问 https://doc.oplist.org/guide/installation/docker#v4-1-0-%E4%BB%A5%E5%90%8E%E7%89%88%E6%9C%AC 获取更多信息。
Exiting...
EOF
      exit 1
    fi
  fi

  # Define the target directory path for aria2 service
  ARIA2_DIR="/opt/service/start/aria2"
  if [ "$RUN_ARIA2" = "true" ]; then
    # If aria2 should run and target directory doesn't exist, copy it
    if [ ! -d "$ARIA2_DIR" ]; then
      mkdir -p "$ARIA2_DIR"
      cp -r /opt/service/stop/aria2/* "$ARIA2_DIR" 2>/dev/null
    fi
  else
    # If aria2 should NOT run and target directory exists, remove it
    if [ -d "$ARIA2_DIR" ]; then
      rm -rf "$ARIA2_DIR"
    fi
  fi

  # rclone 服务目录必须清掉：base 镜像的 runsvdir 会扫描 /opt/service/start，
  # 若这里放了 rclone 服务，runsvdir 和下文直接执行会同时拉起 rcd → 端口抢占
  # （CI 实测 address already in use）。rcd 统一由下面直接后台启动。
  rm -rf /opt/service/start/rclone 2>/dev/null || true

  # rclone rcd 守护：单实例锁 + 简单重试循环。
  # 历史教训（全部 CI 实测）：runsv→端口抢占；普通&→僵尸；setsid&→双实例抢端口。
  # 方案：用 mkdir 原子锁保证只有一个守护进程，守护自己 while 重启 rcd。
  if [ "$RUN_RCLONE" = "true" ] && [ -x /opt/rclone/rclone ]; then
    mkdir -p /var/log
    if mkdir /tmp/.rcd-guard 2>/dev/null; then
      (
        while true; do
          /opt/rclone/rclone rcd             --config /config/rclone/rclone.conf             --rc-addr "$RCLONE_RC_ADDR"             --rc-user "$RCLONE_RC_USER"             --rc-pass "$RCLONE_RC_PASS"             --log-level INFO >> /var/log/rcd.log 2>&1
          echo "[$(date)] rcd exited code=$?, restart in 3s" >> /var/log/rcd.log
          sleep 3
        done
      ) &
      echo "rcd 守护已启动（addr=$RCLONE_RC_ADDR）"
    else
      echo "rcd 守护已存在（锁目录被占），跳过"
    fi
  fi

  # aria2 仍走 runsv（上游行为不变）
  if [ -d /opt/service/start/aria2 ]; then
    runsvdir /opt/service/start &
  fi
  exec ./openlist server --no-prefix
fi
