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

  # rclone rcd：直接后台拉起（不走 runsv）。
  # 实测 runsv 会把 rcd 拖进 finish/restart 循环（finish 每 2s 触发、端口 TIME_WAIT
  # 抢占 → bind address already in use），直接后台起最稳。保留 runsv 目录供
  # RUN_RCLONE_USE_RUNSV=true 的替代路径。
  if [ "$RUN_RCLONE" = "true" ] && [ -x /opt/rclone/rclone ]; then
    # 不经过 run 脚本：直接起 rclone，参数显式传（避免子 shell/env 继承问题，
    # CI 实测 run 脚本里的 ${RCLONE_RC_ADDR} 没生效、永远绑 5572）
    /opt/rclone/rclone rcd       --config /config/rclone/rclone.conf       --rc-addr "${RCLONE_RC_ADDR:-127.0.0.1:5572}"       --rc-user "${RCLONE_RC_USER}"       --rc-pass "${RCLONE_RC_PASS}"       --log-level INFO &
    echo "rcd 已后台启动（pid $!，addr=${RCLONE_RC_ADDR:-127.0.0.1:5572}）"
  fi

  # aria2 仍走 runsv（上游行为不变）
  if [ -d /opt/service/start/aria2 ]; then
    runsvdir /opt/service/start &
  fi
  exec ./openlist server --no-prefix
fi
