#!/bin/bash
set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Config
INSTALL_DIR="/opt/aliyun-spot-autoopen"
SERVICE_NAME="aliyun-spot"

echo -e "${RED}========================================${NC}"
echo -e "${RED}  Aliyun Spot Instance Auto-Start${NC}"
echo -e "${RED}  卸载脚本${NC}"
echo -e "${RED}========================================${NC}"
echo

# Check root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}请使用 root 用户运行此脚本${NC}"
    echo "sudo bash uninstall.sh"
    exit 1
fi

# Confirm
echo -e "${YELLOW}此操作将:${NC}"
echo -e "  1. 停止并禁用 $SERVICE_NAME 服务"
echo -e "  2. 删除 systemd 服务文件"
echo -e "  3. 删除安装目录 $INSTALL_DIR"
echo
read -p "确定要卸载吗? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "已取消"
    exit 0
fi

echo

# Stop service
echo -e "${GREEN}[1/3] 停止服务...${NC}"
if systemctl is-active --quiet $SERVICE_NAME 2>/dev/null; then
    systemctl stop $SERVICE_NAME
    echo "服务已停止"
else
    echo "服务未运行"
fi

# Disable service
if systemctl is-enabled --quiet $SERVICE_NAME 2>/dev/null; then
    systemctl disable $SERVICE_NAME
    echo "服务已禁用"
fi

# Remove service file
echo -e "${GREEN}[2/3] 删除服务文件...${NC}"
if [ -f /etc/systemd/system/$SERVICE_NAME.service ]; then
    rm /etc/systemd/system/$SERVICE_NAME.service
    systemctl daemon-reload
    echo "服务文件已删除"
else
    echo "服务文件不存在"
fi

# Remove install directory
echo -e "${GREEN}[3/3] 删除安装目录...${NC}"
if [ -d $INSTALL_DIR ]; then
    rm -rf $INSTALL_DIR
    echo "安装目录已删除"
else
    echo "安装目录不存在"
fi

echo
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  卸载完成！${NC}"
echo -e "${GREEN}========================================${NC}"