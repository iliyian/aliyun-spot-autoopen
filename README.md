# Aliyun Spot Instance Manager

阿里云抢占式实例 & GCP 抢占式实例自动检测和开机工具。自动监控所有区域的抢占式实例，当实例被回收（停止）时自动重新启动，并通过 Telegram 发送通知。

## 🚀 一键安装

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/iliyian/aliyun-spot-manager/main/install.sh)"
```

## 🔄 一键升级

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/iliyian/aliyun-spot-manager/main/install.sh)" -- upgrade
```

或者在已安装的服务器上：
```bash
sudo /opt/aliyun-spot-manager/install.sh upgrade
```

安装完成后，编辑配置文件并启动服务：
```bash
# 编辑配置
sudo vim /opt/aliyun-spot-manager/.env

# 启动服务
sudo systemctl start aliyun-spot

# 设置开机自启
sudo systemctl enable aliyun-spot

# 查看日志
sudo journalctl -u aliyun-spot -f
```

## 🗑️ 一键卸载

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/iliyian/aliyun-spot-manager/main/uninstall.sh)"
```

## 功能特性

- 🔍 **自动发现** - 自动扫描所有区域，找出所有抢占式实例
- ⏰ **定时监控** - 每分钟检测实例状态（可配置）
- 🚀 **自动启动** - 检测到 Stopped 状态自动启动，失败重试 3 次
- 📱 **Telegram 通知** - 实例回收、启动成功、启动失败都会通知
- 🔇 **通知限流** - 同一实例 5 分钟内只通知一次，避免刷屏
- 💰 **扣费查询** - 通过 Bot 命令查询扣费汇总和月度估算
- 📶 **流量统计** - 查询本月流量使用情况，区分中国大陆和非中国大陆
- 🤖 **Bot 交互命令** - 通过 Telegram 命令随时查询扣费、流量和实例状态
- 🌐 **共享带宽管理** - 通过 Telegram 按钮交互，将实例 EIP 加入或移出共享带宽包
- 🚨 **流量超额自动关机** - 中国大陆/非中国大陆流量分别设置阈值，超额自动停机并通知
- ☁️ **GCP 抢占式实例** - 支持 GCP Preemptible/Spot VM 自动发现和重启

## 快速开始

### 1. 获取阿里云 AccessKey

1. 登录 [阿里云控制台](https://console.aliyun.com/)
2. 点击右上角头像 → **AccessKey 管理**
3. 创建 AccessKey（建议使用 RAM 子账号）
4. 记录 AccessKey ID 和 AccessKey Secret

**所需权限：**
- `ecs:DescribeRegions`
- `ecs:DescribeInstances`
- `ecs:DescribeInstanceStatus`
- `ecs:StartInstance`
- `ecs:StopInstance`
- `vpc:DescribeEipAddresses`
- `vpc:DescribeCommonBandwidthPackages`
- `vpc:AddCommonBandwidthPackageIp`
- `vpc:RemoveCommonBandwidthPackageIp`

### 2. 创建 Telegram Bot

1. 在 Telegram 中搜索 [@BotFather](https://t.me/BotFather)
2. 发送 `/newbot` 创建新机器人
3. 按提示设置机器人名称
4. 获取 Bot Token（格式：`123456789:ABCdefGHIjklMNOpqrsTUVwxyz`）

**获取 Chat ID（三种方法）：**

**方法 1：使用 @userinfobot（推荐，最简单）**
1. 在 Telegram 中搜索 `@userinfobot`
2. 点击 Start 或发送任意消息
3. 机器人会回复你的 Chat ID（Id: 后面的数字）

**方法 2：使用 @getmyid_bot**
1. 在 Telegram 中搜索 `@getmyid_bot`
2. 点击 Start
3. 机器人会回复 Your user ID

**方法 3：通过 API 获取（适用于群组通知）**
1. 先把你创建的 Bot 添加到目标群组
2. 在群组中 @你的机器人 发送一条消息
3. 在浏览器访问：
   ```
   https://api.telegram.org/bot<你的BOT_TOKEN>/getUpdates
   ```
4. 在返回的 JSON 中找到 `"chat":{"id":-123456789}`
   - 个人聊天 ID 是正数（如 `815609952`）
   - 群组 ID 是负数（如 `-123456789`）

### 3. 配置环境变量

```bash
# 编辑配置
vim .env
```

必填配置：
```bash
ALIYUN_ACCESS_KEY_ID=your-access-key-id
ALIYUN_ACCESS_KEY_SECRET=your-access-key-secret
TELEGRAM_BOT_TOKEN=your-bot-token
TELEGRAM_CHAT_ID=your-chat-id
```

### 4. 编译和运行

**本地编译：**
```bash
# 安装依赖
go mod tidy

# 编译
go build -o aliyun-spot-manager

# 运行
./aliyun-spot-manager
```

**交叉编译（Windows 编译 Linux 版本）：**
```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o aliyun-spot-manager-linux-amd64

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -o aliyun-spot-manager-linux-arm64
```

## 部署到服务器

### 使用 systemd（推荐）

```bash
# 1. 创建目录
sudo mkdir -p /opt/aliyun-spot-manager

# 2. 上传文件
sudo cp aliyun-spot-manager /opt/aliyun-spot-manager/
sudo cp .env /opt/aliyun-spot-manager/
sudo chmod +x /opt/aliyun-spot-manager

# 3. 安装服务
sudo cp deploy/aliyun-spot.service /etc/systemd/system/
sudo systemctl daemon-reload

# 4. 启动服务
sudo systemctl enable aliyun-spot
sudo systemctl start aliyun-spot

# 5. 查看状态
sudo systemctl status aliyun-spot

# 6. 查看日志
sudo journalctl -u aliyun-spot -f
```

### 使用 Docker（可选）

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && go build -o aliyun-spot-manager

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/aliyun-spot-manager .
CMD ["./aliyun-spot-manager"]
```

```bash
# 构建镜像
docker build -t aliyun-spot-manager .

# 运行容器
docker run -d --name aliyun-spot \
  --env-file .env \
  --restart always \
  aliyun-spot-manager
```

## 配置说明

| 环境变量 | 必填 | 默认值 | 说明 |
|---------|------|--------|------|
| `ALIYUN_ACCESS_KEY_ID` | ✅ | - | 阿里云 AccessKey ID |
| `ALIYUN_ACCESS_KEY_SECRET` | ✅ | - | 阿里云 AccessKey Secret |
| `TELEGRAM_ENABLED` | ❌ | `true` | 是否启用 Telegram 通知 |
| `TELEGRAM_BOT_TOKEN` | ✅* | - | Telegram Bot Token |
| `TELEGRAM_CHAT_ID` | ✅* | - | Telegram Chat ID |
| `CHECK_INTERVAL` | ❌ | `60` | 检测间隔（秒） |
| `RETRY_COUNT` | ❌ | `3` | 启动失败重试次数 |
| `RETRY_INTERVAL` | ❌ | `30` | 重试间隔（秒） |
| `NOTIFY_COOLDOWN` | ❌ | `300` | 通知冷却时间（秒） |
| `LOG_LEVEL` | ❌ | `info` | 日志级别 |
| `LOG_FILE` | ❌ | - | 日志文件路径 |
| `TRAFFIC_SHUTDOWN_ENABLED` | ❌ | `true` | 是否启用流量超额自动关机 |
| `TRAFFIC_LIMIT_CHINA_GB` | ❌ | `19` | 中国大陆流量阈值（GB） |
| `TRAFFIC_LIMIT_NON_CHINA_GB` | ❌ | `195` | 非中国大陆流量阈值（GB） |
| `TRAFFIC_CHECK_INTERVAL` | ❌ | `300` | 流量检查间隔（秒） |
| `GCP_ENABLED` | ❌ | `false` | 是否启用 GCP 抢占式实例监控 |
| `GCP_PROJECT_ID` | ✅** | - | GCP 项目 ID |
| `GCP_CREDENTIALS_FILE` | ❌ | - | GCP 服务账号密钥文件路径（**systemd 下推荐**） |
| `GCP_CREDENTIALS_JSON` | ❌ | - | GCP 服务账号密钥 JSON 内容（留空使用 ADC；systemd 下不推荐） |
| `GCP_ZONES` | ❌ | - | GCP 监控区域，逗号分隔（留空自动发现） |

*当 `TELEGRAM_ENABLED=true` 时必填

**当 `GCP_ENABLED=true` 时必填

**注意：** 使用扣费查询功能需要 AccessKey 具有 BSS（费用中心）API 权限：
- `bss:QueryInstanceBill` - 查询实例账单
- 或直接授予 `AliyunBSSReadOnlyAccess` 策略

**注意：** 使用流量查询功能需要 AccessKey 具有 CDT（云数据传输）API 权限：
- `cdt:ListCdtInternetTraffic` - 查询互联网流量
- 或直接授予 `AliyunCDTReadOnlyAccess` 策略

**注意：** 使用共享带宽管理功能需要 AccessKey 具有 VPC API 权限：
- `vpc:DescribeEipAddresses` - 查询弹性公网 IP
- `vpc:DescribeCommonBandwidthPackages` - 查询共享带宽包
- `vpc:AddCommonBandwidthPackageIp` - 将 EIP 加入共享带宽包
- `vpc:RemoveCommonBandwidthPackageIp` - 将 EIP 移出共享带宽包
- 或直接授予 `AliyunVPCFullAccess` 策略

### GCP 抢占式实例配置

启用 GCP 监控后，程序会自动扫描指定项目中的所有 Preemptible/Spot VM，当实例被抢占（状态变为 TERMINATED/STOPPED）时自动重启。

**1. 在 Cloud Shell 中创建服务账号并获取密钥 JSON：**

```bash
# 创建服务账号
gcloud iam service-accounts create spot-manager \
  --display-name="Spot Instance Manager"

# 授予 Compute 实例管理权限
gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member="serviceAccount:spot-manager@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/compute.instanceAdmin.v1"

# 生成密钥 JSON 并输出到控制台
gcloud iam service-accounts keys create /dev/stdout \
  --iam-account=spot-manager@YOUR_PROJECT_ID.iam.gserviceaccount.com
```

复制输出的 JSON，在实际服务器上保存为文件：

```bash
# 在实际服务器上
cat > /opt/aliyun-spot-manager/gcp-credentials.json << 'EOF'
{粘贴刚才复制的 JSON}
EOF
chmod 600 /opt/aliyun-spot-manager/gcp-credentials.json
```

**2. 配置环境变量（systemd 下推荐使用文件路径）：**

```bash
GCP_ENABLED=true
GCP_PROJECT_ID=your-project-id
# 推荐：指定密钥文件路径（systemd EnvironmentFile 不支持 JSON 多行值）
GCP_CREDENTIALS_FILE=/opt/aliyun-spot-manager/gcp-credentials.json
# 可选：指定监控区域，留空自动发现所有区域
GCP_ZONES=us-central1-a,asia-east1-b
```

> **注意：** 使用 systemd 管理服务时，请用 `GCP_CREDENTIALS_FILE` 指定密钥文件路径，而不是将 JSON 内容写入 `GCP_CREDENTIALS_JSON`。systemd 的 `EnvironmentFile` 解析器不支持包含特殊字符的多行 JSON 值，会导致私钥解析失败。

**所需 GCP 权限：**
- `compute.instances.list` - 列出实例
- `compute.instances.get` - 获取实例状态
- `compute.instances.start` - 启动实例
- `compute.instances.stop` - 停止实例
- `compute.zones.list` - 列出可用区
- 或直接授予 `roles/compute.instanceAdmin.v1` 角色

## 通知示例

**实例被回收：**
```
🔴 实例被回收
━━━━━━━━━━━━━━━
实例: web-server-1
ID: i-xxx123
区域: cn-hangzhou
时间: 2024-01-06 15:30:00
━━━━━━━━━━━━━━━
正在尝试自动启动...
```

**实例已启动：**
```
✅ 实例已启动
━━━━━━━━━━━━━━━
实例: web-server-1
ID: i-xxx123
区域: cn-hangzhou
公网IP: 47.xxx.xxx.xxx
状态: Running ✓
启动耗时: 45 秒
━━━━━━━━━━━━━━━
```

**启动失败：**
```
❌ 启动失败
━━━━━━━━━━━━━━━
实例: web-server-1
ID: i-xxx123
区域: cn-hangzhou
错误: Insufficient balance
重试: 3 次均失败
━━━━━━━━━━━━━━━
请手动检查！
```

**扣费汇总（/billing 命令）：**
```
📊 扣费汇总 (2024-01)
━━━━━━━━━━━━━━━━━━━━━━━━
📅 统计区间: 2024-01 01日 ~ 09日 17:27
⏱ 已过天数: 9 天
🕐 总运行时长: 126.3 小时
━━━━━━━━━━━━━━━━━━━━━━━━

🖥 web-server-1 [ecs.t6-c4m1.large]
   i-xxx123 | cn-hangzhou
   ├─ 系统盘: ¥0.2907
   ├─ 镜像费用: ¥0.0000
   └─ 计算 (ecs.t6-c4m1.large): ¥0.2845
   小计: ¥0.5753

🖥 db-server [ecs.e-c4m1.large]
   i-xxx456 | cn-shanghai
   ├─ 计算 (ecs.e-c4m1.large): ¥0.1712
   ├─ 系统盘: ¥0.2079
   └─ 镜像费用: ¥0.0000
   小计: ¥0.3791

━━━━━━━━━━━━━━━━━━━━━━━━
💰 本月累计: ¥0.9544
📈 月度估算: ¥28.63
📝 按运行时长: ¥0.0076/小时 × 720小时
```

**流量统计（/traffic 命令）：**
```
📶 流量统计 (2024-01)
━━━━━━━━━━━━━━━━
📅 统计区间: 2024-01 01日 ~ 12日 12:01
━━━━━━━━━━━━━━━━

🇨🇳 中国大陆📊 总流量: 1.25 GB
   🌐 区域数: 2
   📦 产品明细:• eip: 1.20 GB
      • ipv6bandwidth: 50.00 MB📍 区域列表:
      • 杭州
      • 上海

🌏 非中国大陆
   📊 总流量: 21.39 GB
   🌐 区域数: 2
   📦 产品明细:
      • eip: 20.00 GB
      • ipv6bandwidth: 1.39 GB
   📍 区域明细:
      • 香港: 8.72 GB
      • 日本(东京): 12.67 GB

━━━━━━━━━━━━━━━━
📈 本月总流量: 22.64 GB
📊 中国大陆: 5.5% | 非中国大陆: 94.5%
```

**流量超额自动关机通知：**
```
🚨 流量超额自动关机
━━━━━━━━━━━━━━━━━━━━━━━━

📍 区域: 🌏 非中国大陆
📊 当前流量: 195.32 GB
🚫 流量阈值: 195.00 GB
⏰ 时间: 2024-01-15 03:20:00

🔴 已关闭实例:
   • web-server-hk (i-xxx789) - 香港
   • app-server-jp (i-xxx012) - 日本(东京)

━━━━━━━━━━━━━━━━━━━━━━━━
💡 使用节省停机模式，不再计费 vCPU/内存
⚠️ 自动重启已暂停，新月流量重置后恢复
```

**共享带宽管理（/cbwp 命令）：**

发送 `/cbwp` 后，Bot 会显示实例列表按钮。点击实例后自动查询该实例的 EIP 和同地域共享带宽包，显示当前状态和操作按钮（加入/移出）。操作完成后会显示结果。

```
🌐 web-server-1
   区域: cn-hangzhou
━━━━━━━━━━━━━━━━

📍 EIP: 47.xxx.xxx.xxx
   📦 当前带宽包: my-cbwp (100Mbps)
   状态: ✅ 已加入共享带宽

[🔴 移出 47.xxx.xxx.xxx]
[« 返回]
```

## Bot 交互命令

程序启动后，你可以通过 Telegram 向 Bot 发送命令来查询信息：

| 命令 | 说明 |
|------|------|
| `/billing` | 查询本月扣费汇总 |
| `/traffic` | 查询本月流量统计 |
| `/status` | 查看所有实例状态 |
| `/cbwp` | 管理共享带宽包（加入/移出） |
| `/help` | 显示帮助信息 |

**命令别名：**
- `/cost`、`/fee` - 查询扣费
- `/flow`、`/bandwidth` - 查询流量

**注意：** Bot 只会响应配置的 `TELEGRAM_CHAT_ID` 发来的消息，其他聊天会被忽略。

## 常见问题

### Q: 如何只监控特定区域？

目前程序会自动扫描所有区域。如果需要限制区域，可以修改代码或提 Issue。

### Q: 启动失败的常见原因？

1. **余额不足** - 检查阿里云账户余额
2. **资源不足** - 该可用区可能没有可用的抢占式资源
3. **权限不足** - 检查 AccessKey 权限

### Q: 如何查看详细日志？

设置 `LOG_LEVEL=debug` 可以看到更详细的日志。

## License

MIT License