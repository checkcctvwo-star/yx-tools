# yx-tools — Cloudflare 优选 IP 测速工具

单个二进制搞定 Cloudflare 优选 IP 测速与上报：内置网页界面（浏览器操作），也支持
纯命令行。测速内核基于 XIU2/CloudflareSpeedTest，支持反代 `IP:端口` 场景。

## 功能

- 测速 Cloudflare 各数据中心延迟与下载速度（IPv4 / IPv6），按机场码筛选地区
- 优选反代：粘贴文本 / 文件导入 / 多个 URL 链接自动抓取 / 随机混入官方 IPv4 网段
- 一键上报到自建 cfnew Worker 面板或 GitHub 仓库
- **分地区上传数量**：按地区配额上报（吉隆坡 / 新加坡 / 香港 / 日本 / 台北 + 手动机场码），
  地区不足可选「用其他地区补位」
- **固定附带列表**：指定 IP 或域名 + 名字，每次上传必定附带在末尾，不参与测速
- **内置定时任务**：间隔模式或每天固定时刻（24 小时制、一天多时间点），到点自动
  测速上报；Docker / NAS 里也能用，重启保留
- 下载策略可选「测够了就停」或「全部测完再挑」

## 快速开始（Docker）

```bash
docker run -d --name yx-tools --restart unless-stopped \
  -p 8080:8080 -e TZ=Asia/Shanghai \
  -v $PWD/data:/data \
  ggshuai/yx-tools:latest
```

浏览器打开 `http://服务器IP:8080`。`-e TZ=Asia/Shanghai` 请换成你所在时区（定时任务
的「每天固定时刻」按容器本地时钟执行）。结果与配置保存在 `/data` 卷里，升级镜像不丢。

可用标签：`latest`、`3.2.0`、`3`、`3.1` 等，全部 amd64 + arm64 多架构。

## 相关仓库

- GitHub（本镜像源码与新版发布）：<https://github.com/checkcctvwo-star/yx-tools>
- 上游项目：<https://github.com/byJoey/yx-tools>
- 配套 Worker 面板 cfnew：<https://github.com/byJoey/cfnew>
