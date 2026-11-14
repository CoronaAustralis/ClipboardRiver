<p align="center">
  <img src="Assets/ClipboardRiverLogo.transparent.png" alt="Clipboard River logo" width="128" />
</p>

<h1 align="center">Clipboard River</h1>

<p align="center">
  <a href="README.md">English</a> | <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  一个强调快捷、低打扰、尽量无感的跨设备剪切板同步项目。
</p>

## 为什么是它

Clipboard River 希望跨设备复制粘贴尽量接近日常操作：一端复制，切换设备后直接粘贴。

- 快：通过 WebSocket 实时推送，减少同步等待。
- 低打扰：客户端可自动接收和写入剪切板，减少手动发送、刷新等操作。
- 实用：支持文本和单文件同步，图片文件可在管理后台预览。
- 可控：服务端可自行部署，提供历史浏览、设备权限和接入管理。
- 跨平台：Windows 和 Android 客户端已可用，iOS 计划后续支持，也可以自行开发客户端。

## 当前限制

- 最接近无感同步的体验是两端 Windows 客户端同时在线。
- 单次文件上传只支持一个文件，暂不支持多文件和文件夹同步。
- 暂不支持富文本、HTML 片段和复杂 Office 剪切板格式。
- Android 的剪切板访问和后台运行受系统限制，体验与 Windows 有差异。
- WebSocket 不补发离线期间的事件；客户端可同步最近 50 条文本，无法据此恢复全部离线历史或错过的文件事件。

## 当前仓库是什么

当前仓库只包含服务端，负责：

- 设备注册与接入管理
- 剪切板实时消息分发
- 剪切板历史存储，以及最近 50 条文本的同步区
- 文件存储和下载
- 管理后台
- Docker 部署及 Windows、Linux 二进制发布

客户端与服务端独立开发，可以按需实现文本上传、实时接收、文件传输等功能，不必实现全部协议。客户端开发说明见 [ApiRef.md](./ApiRef.md)。

- Windows：[ClipboardRiverWindowsClient](https://github.com/CoronaAustralis/ClipboardRiverWindowsClient)
- Android：[ClipboardRiverAndroidClient](https://github.com/CoronaAustralis/ClipboardRiverAndroidClient)

## 快速开始

从 [GitHub Releases](https://github.com/CoronaAustralis/ClipboardRiver/releases/latest) 下载对应平台的二进制包。

Linux（以下以 amd64 为例）：

```bash
curl -fLO https://github.com/CoronaAustralis/ClipboardRiver/releases/latest/download/clipboard-river-server_linux_amd64.tar.gz
tar -xzf clipboard-river-server_linux_amd64.tar.gz
cd clipboard-river-server_linux_amd64
./clipboard-river-server --config ./config.json
```

Windows（PowerShell）：

```powershell
Invoke-WebRequest -Uri "https://github.com/CoronaAustralis/ClipboardRiver/releases/latest/download/clipboard-river-server_windows_amd64.zip" -OutFile "clipboard-river-server_windows_amd64.zip"
Expand-Archive -Path .\clipboard-river-server_windows_amd64.zip -DestinationPath .
Set-Location .\clipboard-river-server_windows_amd64
.\clipboard-river-server.exe --config .\config.json
```

首次启动后，从日志获取管理员密码，打开 [http://127.0.0.1:8080/admin/login](http://127.0.0.1:8080/admin/login)。登录后台开启接入窗口，使用接入码注册客户端；窗口持续 5 分钟，期间不限注册次数。

| 参数 | 作用 |
| --- | --- |
| `--config <path>` | 指定 JSON 配置文件，默认 `./config.json`；不存在时自动生成 |
| `--version` | 显示版本并退出 |
| `--help` | 显示帮助并退出 |

## 服务端配置与初始化

服务端配置只从 JSON 文件读取，示例见 [config.example.json](./config.example.json)。监听地址等选项在文件中修改后重启生效。相对路径以运行程序时的工作目录为基准。

```json
{
  "server": { "listen_addr": ":8080" },
  "storage": {
    "driver": "sqlite",
    "dsn": "./data/clipboard_river.db",
    "data_dir": "./data",
    "blob_dir": "./data/blobs"
  },
  "auth": { "session_secret": "replace-with-a-long-random-secret" },
  "sync": {
    "default_retention_days": 30,
    "file_max_bytes": 5242880,
    "text_batch_limit": 200
  },
  "admin": { "username": "admin" }
}
```

首次启动按需创建默认账户和管理员，自动生成的 10 位管理员密码只输出一次，数据库仅保存密码哈希。缺失的配置文件会自动生成随机 `session_secret`，示例文件不会自动加载。

默认使用 SQLite，也支持 MySQL、PostgreSQL。每个数据库运行一个服务端进程；接入窗口和文本同步区保存在进程内存中，重启后接入关闭、文本同步区从数据库重建。

## Docker

Compose：

```bash
wget -O docker-compose.yml https://raw.githubusercontent.com/CoronaAustralis/ClipboardRiver/refs/heads/main/compose.yaml
docker compose up -d
```

直接运行（Linux）：

```bash
docker run --name clipboard-river-server -p 8080:8080 -v "$(pwd)/data:/app/data" ghcr.io/coronaaustralis/clipboardriver:latest
```

镜像支持 Linux amd64 和 arm64。`latest` 跟随最近发布的版本，也可替换为固定版本标签，例如 `ghcr.io/coronaaustralis/clipboardriver:v1.0.0`。

## 管理后台

后台提供历史浏览、文件下载、设备发送/接收开关、软删除设备、临时接入窗口、保留期限及文件限制设置。删除设备会撤销访问权限，同时保留设备记录和历史来源关联。修改管理员密码需要当前密码，新密码至少 8 位。

## Build（源码构建）

开发者安装 Go 1.26 或更新版本，在仓库根目录执行。

Linux：

```bash
mkdir -p bin
./scripts/go-local.sh build -o ./bin/clipboard-river-server ./cmd/cb-river-server
./scripts/go-local.sh test ./...
```

Windows（PowerShell）：

```powershell
New-Item -ItemType Directory -Force bin | Out-Null
powershell -ExecutionPolicy Bypass -File .\scripts\go-local.ps1 build -o .\bin\clipboard-river-server.exe ./cmd/cb-river-server
powershell -ExecutionPolicy Bypass -File .\scripts\go-local.ps1 test ./...
```

[go-local.sh](./scripts/go-local.sh) 和 [go-local.ps1](./scripts/go-local.ps1) 默认使用仓库内的 Go 缓存，`build` 替换为 `run` 可直接运行源码，例如 `./scripts/go-local.sh run ./cmd/cb-river-server --config ./config.json`。


Bash 构建脚本丢失执行权限时运行 `chmod +x scripts/go-local.sh`。
