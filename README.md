# Frpc WebUI

一个轻量级的 FRP 客户端 Web 管理界面，通过 Docker 部署，让你无需编辑配置文件即可管理 frpc。

> 本项目基于 [ZhensJoke/fnos-frpc](https://github.com/ZhensJoke/fnos-frpc) 修改而来，感谢原作者的开源贡献。

![Frpc WebUI](frpc.png)

## 功能特性

| 特性 | 说明 |
|------|------|
| Docker 部署 | 一键部署，支持 AMD64 / ARM64 架构 |
| Web 界面 | 图形化配置，告别手动编辑配置文件 |
| 多服务器管理 | 同时管理多个 frps 服务端连接 |
| 多协议代理 | 支持 TCP / UDP / HTTP / HTTPS 转发规则 |
| 规则备注 | 每条转发规则可添加备注，方便标记用途 |
| 日志与配置 | 顶部菜单快速查看运行日志和生成的 frpc.toml |
| 密码保护 | 登录密码保护，支持在线修改 |
| 密码重置 | 忘记密码可通过命令行 `--reset-password` 重置 |
| 自动启动 | 服务端配置支持开机自启，重启后自动恢复连接 |
| 深色/浅色主题 | 支持深色和浅色主题切换，自动适配系统偏好 |
| 轻量 | 镜像仅 ~7-8MB，零外部依赖 |

## 快速开始

### Docker Run

```bash
# 拉取镜像（自动适配 AMD64 / ARM64 架构）
docker pull lijsfun/frpc-webui:latest

# 运行容器
docker run -d \
  --name frpc-webui \
  --network host \
  -v ./data:/app/data \
  -e WEB_PORT=7500 \
  -e TZ=Asia/Shanghai \
  --restart unless-stopped \
  lijsfun/frpc-webui:latest
```

### Docker Compose

```bash
docker compose up -d
```

### 访问界面

打开浏览器访问：

```
http://你的服务器IP:7500
```

## 使用指南

### 1. 设置密码

首次访问需要设置管理密码（至少 6 位）。登录后可随时通过顶部导航栏的锁图标修改密码。

### 2. 安装 frpc

点击右上角的 GitHub 图标，前往 frp Releases 页面下载 frpc，然后上传到服务器。

> frpc 下载地址：https://github.com/fatedier/frp/releases

### 3. 添加服务器

点击 **+** 添加你的 frps 服务器：

```
名称：我的服务器
地址：frps.example.com
端口：7000
Token：your_token
```

### 4. 配置代理

添加需要穿透的服务，**代理名称需在整个 frps（服务端）里全局唯一**：

| 类型 | 用途 | 示例 |
|------|------|------|
| TCP | SSH 远程连接 | 本地 22 端口 → 远程 6022 端口 |
| HTTP | Web 服务 | 本地 80 端口 → 域名绑定 |
| HTTPS | 安全 Web | 本地 443 端口 → 域名绑定 |

每条规则支持填写**备注**信息，方便标记用途。

### 5. 启动连接

点击「启动」按钮，支持启动、停止、重启操作。

### 6. 日志与配置

通过顶部导航栏的按钮可以：

- **查看配置** - 查看当前生成的 frpc.toml 配置内容
- **运行日志** - 实时查看 frpc 输出，支持刷新和清空

> 需要先选中一个服务器，这两个按钮才会启用。

## 重置密码

如果忘记了登录密码，可以通过命令行重置：

### Docker 环境

```bash
docker exec frpc-webui /app/frpc-webui --reset-password "新密码"
```

### 直接运行

```bash
./frpc-webui --reset-password "新密码"
```

注意事项：
- 新密码至少 6 位
- 需要先通过 Web UI 设置过初始密码后才能重置
- 重置成功后程序会立即退出，需要重新启动服务

## 配置选项

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WEB_PORT` | `7500` | Web 界面端口 |
| `DATA_DIR` | `/app/data` | 数据存储目录 |
| `FRPC_PATH` | 自动检测 | frpc 二进制文件路径 |
| `TZ` | - | 时区设置 |

### 命令行参数

| 参数 | 说明 |
|------|------|
| `--reset-password <密码>` | 重置登录密码 |

### 数据目录

```
data/
├── auth.json       # 管理密码
├── servers.json    # 服务器和代理配置
├── frpc/           # frpc 二进制文件
├── conf/           # 生成的 frpc 配置
└── logs/           # frpc 运行日志
```

## 常用命令

```bash
# 查看日志
docker logs -f frpc-webui

# 重启服务
docker restart frpc-webui

# 停止服务
docker stop frpc-webui

# 重置密码
docker exec frpc-webui /app/frpc-webui --reset-password "新密码"
```

## 从源码构建

```bash
git clone https://github.com/lijsjust2/Frpc-WebUI.git
cd Frpc-WebUI

docker build -t frpc-webui .
```

或直接编译：

```bash
go build -o frpc-webui .
```

## 技术栈

- **后端**：Go（静态资源内嵌，零外部依赖）
- **前端**：原生 HTML/CSS/JS（无框架）
- **基础镜像**：scratch（空镜像）

## 致谢

本项目基于 [ZhensJoke/fnos-frpc](https://github.com/ZhensJoke/fnos-frpc) 修改开发，感谢原作者的开源贡献。

## License

MIT
