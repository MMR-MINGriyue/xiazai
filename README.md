# 下载器（类 IDM）

自用的高速下载工具，目标是替代 IDM：**多协议、多线程、下载快**。

基于 [Gopeed](https://github.com/GopeedLab/gopeed) 二次开发（Go 引擎 + Flutter UI），在其 HTTP / BT / 磁力 / ED2K 能力之上，补了 SFTP / FTP、视频站下载、全局限速和 IDM 式界面。

> 个人项目，不追求全平台发行；Windows 优先，按自己的下载场景持续打磨。

---

## 特性

### 协议

| 协议 | 说明 |
|------|------|
| HTTP / HTTPS | 多连接分段，慢启动扩连 + work stealing |
| BitTorrent / 磁力 | 沿用上游引擎 |
| ED2K | 沿用上游引擎 |
| SFTP | 密码 / 密钥认证，多连接分段，支持目录递归 |
| FTP / FTPS | REST 断点 + 多连接，支持目录递归 |
| 视频站 | B 站 / YouTube 等，yt-dlp 解析 + 多线程下载 + ffmpeg 合并 |

### 速度

- **多线程分段下载**：HTTP / SFTP / FTP 按连接切段并发拉取
- **IDM 式动态分段**：空闲连接从慢段尾部抢活（work stealing），避免「一快一慢」
- **基准**：本地限速服务器上，8 连接相对单连接约 **7.4×** 加速

### 实用能力

- 断点续传、崩溃恢复（分段进度持久化）
- 全局限速（令牌桶，设置页或 CLI `-R`）
- 任务列表：进度 / 速度 / 剩余时间 / 状态筛选 / 协议徽章
- 分段可视化：详情里实时色块显示各段状态
- 底部状态栏：全局速度、今日流量、限速提示
- 视频站：选清晰度 → 自动装 yt-dlp / ffmpeg → 下载合并；列表显示「合并中」状态

---

## 快速开始

### 源码构建

```bash
# 引擎 / CLI
go build -o bin/gopeed ./cmd/gopeed

# 本地 API（开发）
go build -o bin/gopeed-api ./cmd/api
./bin/gopeed-api   # 默认 127.0.0.1:9999
```

CLI 示例：

```bash
# HTTP 多连接
gopeed -C 8 -D ./downloads https://example.com/bigfile.zip

# 全局限速 1MB/s
gopeed -R 1048576 -D ./downloads https://example.com/file.bin

# SFTP / FTP
gopeed -D ./downloads sftp://user:pass@host/path/file.bin
gopeed -D ./downloads ftp://user:pass@host/path/file.bin
```

### 视频站

1. 粘贴 B 站 / YouTube 等链接（新建任务）
2. 若提示缺少组件，点「安装组件」（自动下载 yt-dlp / ffmpeg 到 `~/.gopeed/tools`）
3. 选择清晰度后开始下载；任务列表会显示下载中 / **音视频合并中** / 完成

依赖外部组件：`yt-dlp`、`ffmpeg`（可自动安装，也可自行放入 PATH 或 `~/.gopeed/tools`）。

### Flutter UI

```bash
cd ui/flutter
flutter pub get
# 桌面构建需本机安装 VS2022「使用 C++ 的桌面开发」工作负载
flutter build windows --debug
```

无桌面工具链时可用 `flutter analyze` / `flutter build web` 做静态检查与 Web 预览。

---

## 架构（简图）

```
┌─────────────────────────────┐
│  Flutter 桌面 UI             │
│  任务列表 / 分段图 / 设置     │
└──────────────┬──────────────┘
               │ REST (localhost)
┌──────────────▼──────────────┐
│  Gopeed 引擎 (Go)            │
│  调度 / 限速 / 协议 Fetcher   │
│  HTTP · BT · ED2K · SFTP · FTP │
│  + videosvc (yt-dlp/ffmpeg)  │
└──────────────┬──────────────┘
               │ Native Messaging
┌──────────────▼──────────────┐
│  浏览器扩展 host（接管下载）  │
└─────────────────────────────┘
```

相对上游的主要改动在：`internal/protocol/sftp`、`internal/protocol/ftp`、`internal/ratelimit`、`internal/videosvc`，以及 `ui/flutter` 的 IDM 式定制。

---

## 开发与测试

```bash
# 引擎相关单测
go test ./internal/protocol/sftp/ ./internal/protocol/ftp/ \
        ./internal/ratelimit/ ./internal/videosvc/ -count=1

# UI 静态检查
cd ui/flutter && flutter analyze --no-pub
```

CI（GitHub Actions）：push 到 `main` 会跑 Linux / Windows 的 Go 测试与构建，并做 Flutter analyze；产物在 Actions Artifact。

---

## 仓库说明

- 上游：[GopeedLab/gopeed](https://github.com/GopeedLab/gopeed)（本仓库为 fork / 二次开发）
- 许可：与上游一致，见 [LICENSE](./LICENSE)
- 文档：设计与计划在根项目 `docs/superpowers/`（spec / M1–M4 计划 / 完成度盘点）

---

## 路线（按优先级）

1. 桌面端完整构建与悬浮进度窗（依赖本机 VS C++ 工具链）
2. 视频 job 持久化、合并后自动隐藏临时任务
3. HLS / m3u8 支持
4. Windows 打包与最终速度复测

---

## 致谢

- [Gopeed](https://github.com/GopeedLab/gopeed) — 基础下载器与扩展体系
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) / [FFmpeg](https://ffmpeg.org/) — 视频解析与合并
