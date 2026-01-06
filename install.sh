#!/bin/bash
set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Config
REPO="iliyian/aliyun-spot-autoopen"
INSTALL_DIR="/opt/aliyun-spot-autoopen"
SERVICE_NAME="aliyun-spot"

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  Aliyun Spot Instance Auto-Start${NC}"
echo -e "${GREEN}  自动安装脚本${NC}"
echo -e "${GREEN}========================================${NC}"
echo

# Check root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}请使用 root 用户运行此脚本${NC}"
    echo "sudo bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh)\""
    exit 1
fi

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case $ARCH in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    *)
        echo -e "${RED}不支持的架构: $ARCH${NC}"
        exit 1
        ;;
esac

case $OS in
    linux)
        BINARY="aliyun-spot-autoopen-linux-$ARCH"
        ;;
    darwin)
        BINARY="aliyun-spot-autoopen-darwin-$ARCH"
        ;;
    *)
        echo -e "${RED}不支持的操作系统: $OS${NC}"
        exit 1
        ;;
esac

echo -e "${YELLOW}检测到系统: $OS-$ARCH${NC}"
echo -e "${YELLOW}将下载: $BINARY${NC}"
echo

# Get latest release
echo -e "${GREEN}[1/5] 获取最新版本...${NC}"
LATEST_VERSION=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$LATEST_VERSION" ]; then
    echo -e "${RED}无法获取最新版本${NC}"
    exit 1
fi
echo "最新版本: $LATEST_VERSION"

# Create directory
echo -e "${GREEN}[2/5] 创建安装目录...${NC}"
mkdir -p $INSTALL_DIR
cd $INSTALL_DIR

# Download binary
echo -e "${GREEN}[3/5] 下载程序...${NC}"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_VERSION/$BINARY"
curl -L -o aliyun-spot-autoopen "$DOWNLOAD_URL"
chmod +x aliyun-spot-autoopen

# Download config template
echo -e "${GREEN}[4/5] 下载配置模板...${NC}"
curl -L -o .env.example "https://raw.githubusercontent.com/$REPO/main/.env.example"

# Create .env if not exists
if [ ! -f .env ]; then
    cp .env.example .env
    echo -e "${YELLOW}已创建配置文件 $INSTALL_DIR/.env${NC}"
    echo -e "${YELLOW}请编辑配置文件填入你的 AccessKey 和 Telegram Token${NC}"
fi

# Install systemd service
echo -e "${GREEN}[5/5] 安装 systemd 服务...${NC}"
cat > /etc/systemd/system/$SERVICE_NAME.service << EOF
[Unit]
Description=Aliyun Spot Instance Auto-Start Monitor
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/aliyun-spot-autoopen
Restart=always
RestartSec=10
EnvironmentFile=$INSTALL_DIR/.env

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload

echo
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  安装完成！${NC}"
echo -e "${GREEN}========================================${NC}"
echo
echo -e "安装目录: ${YELLOW}$INSTALL_DIR${NC}"
echo -e "配置文件: ${YELLOW}$INSTALL_DIR/.env${NC}"
echo
echo -e "${YELLOW}下一步:${NC}"
echo -e "1. 编辑配置文件:"
echo -e "   ${GREEN}vim $INSTALL_DIR/.env${NC}"
echo
echo -e "2. 启动服务:"
echo -e "   ${GREEN}systemctl start $SERVICE_NAME${NC}"
echo
echo -e "3. 设置开机自启:"
echo -e "   ${GREEN}systemctl enable $SERVICE_NAME${NC}"
echo
echo -e "4. 查看日志:"
echo -e "   ${GREEN}journalctl -u $SERVICE_NAME -f${NC}"
echo