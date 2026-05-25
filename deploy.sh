#!/bin/bash
# Frpc WebUI 管理器 - Docker 部署脚本
# 用法: 将此脚本和 docker-compose.yml 放在同一目录下执行

set -e

echo "============================================"
echo "  Frpc WebUI 管理器 - Docker 部署"
echo "============================================"
echo ""

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# 检查 docker-compose.yml
if [ ! -f "docker-compose.yml" ]; then
    echo "[错误] 未找到 docker-compose.yml 文件！"
    exit 1
fi

# 创建数据目录
echo "[1/2] 创建数据目录..."
mkdir -p ./data
echo "      创建成功 ✓"

# 启动容器
echo "[2/2] 启动容器..."
docker compose up -d 2>/dev/null || docker-compose up -d
echo "      启动成功 ✓"

echo ""
echo "============================================"
echo "  部署完成！"
echo ""

# 获取服务器 IP
IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "SERVER-IP")
PORT=$(grep WEB_PORT docker-compose.yml 2>/dev/null | grep -o '[0-9]*' | head -1)
PORT=${PORT:-7500}

echo "  访问地址: http://$IP:$PORT"
echo ""
echo "  常用命令:"
echo "    查看日志:  docker compose logs -f"
echo "    重启服务:  docker compose restart"
echo "    停止服务:  docker compose down"
echo "============================================"
