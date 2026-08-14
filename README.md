# yx-tools

Cloudflare 优选 IP 测速工具。单个二进制，命令行和网页界面都能用。

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8.svg)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20|%20macOS%20|%20Linux-lightgrey.svg)](https://github.com/byJoey/yx-tools/releases)

测速内核基于 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)，
补了反代场景需要的 `IP:端口` 支持。

## 能做什么

- 测 Cloudflare 各数据中心的延迟和下载速度，支持 IPv4 / IPv6
- 按机场码筛地区，全球 97 个数据中心
- 反代模式：输入 `IP:端口`，结果保留端口信息
- 优选反代来源支持：粘贴文本 / 文件导入 / 多个 URL 链接（每次执行前重新抓取，失败自动跳过）/ 随机混入 Cloudflare 官方 IPv4 网段
- 一键上报到 [cfnew](https://github.com/byJoey/cfnew) 面板，或推到 GitHub 仓库
- 分地区上传数量：按地区配额上报（预设 吉隆坡/新加坡/香港/日本/台北 + 手动机场码 + 「其他地区」），地区不足可选「补位」
- 固定附带列表：指定 IP 或域名 + 名字，每次上传必定附带在列表末尾，不参与测速
- 内置定时任务：间隔模式（每 N 分钟/小时）或每天固定时刻（24 小时制、一天多个时间点），到点自动测速并按设置上报；Docker / NAS 里也能用，重启后任务保留
- 下载策略可选「测够了就停」或「全部测完再挑」（默认测够了就停）
- 网页界面实时看进度，也能纯命令行跑，适合塞进定时任务

## 装

去 [Releases](https://github.com/byJoey/yx-tools/releases) 下对应平台的包，解压就能跑。不用装 Python，不用装依赖。

不知道下哪个？照这张表挑：

| 你的设备 | 下这个 |
| :--- | :--- |
| Windows 电脑（绝大多数） | `yx_windows_amd64.zip` |
| Windows，骁龙/ARM 本 | `yx_windows_arm64.zip` |
| Mac，M1 及以后（2020 年后买的） | `yx_darwin_arm64.tar.gz` |
| Mac，Intel 芯片（2020 年前） | `yx_darwin_amd64.tar.gz` |
| Linux 服务器 / VPS（绝大多数） | `yx_linux_amd64.tar.gz` |
| Linux ARM，甲骨文免费机、树莓派 | `yx_linux_arm64.tar.gz` |
| 老的 32 位 Linux | `yx_linux_386.tar.gz` |
| FreeBSD | `yx_freebsd_amd64.tar.gz` |

不确定 Mac 是哪种芯片：点左上角苹果图标 →「关于本机」，写着 M1/M2/M3/M4 就选 arm64。
不确定 Linux 是哪种：命令行敲 `uname -m`，`x86_64` 选 amd64，`aarch64` 选 arm64。

```bash
# Linux / macOS：解压后要先加执行权限
tar -xzf yx_linux_amd64.tar.gz
chmod +x yx_linux_amd64
./yx_linux_amd64
```

解压出来的文件名带平台后缀（如 `yx_linux_amd64`），不是 `yx`。嫌长可以自己改名：
`mv yx_linux_amd64 yx`。

Windows 解压后双击 `yx_windows_amd64.exe`，会自动开浏览器。
macOS 首次运行若提示「无法验证开发者」，去「系统设置 → 隐私与安全性」点「仍要打开」。

自己编译也行：

```bash
git clone https://github.com/byJoey/yx-tools.git
cd yx-tools
go build -o yx ./cmd/yx
```

## 用

### 网页界面

```bash
./yx
```

默认监听 `127.0.0.1:8080` 并自动开浏览器。放服务器上跑就换个监听地址：

```bash
./yx web -listen 0.0.0.0:8080
```

左边配参数，右边看结果。地区搜索框输中文或机场码都行，留空就是不限地区。

### 命令行

```bash
# 测 10 个，速度下限 1MB/s
./yx test -n 10 -sl 1

# 只测香港和新加坡
./yx test -colo HKG,SIN -n 20

# 测完直接上报到 cfnew
./yx test -n 10 -upload api -domain your.workers.dev -uuid 你的UUID -clear

# 从已有结果生成反代列表
./yx proxy -limit 20
```

`-h` 看完整参数。

### Docker

```bash
docker compose up -d
```

浏览器打开 `http://服务器IP:8080`。结果和配置存在 `./data`，容器会自己把这个目录的
权限修好，不用手动 chown。

不想用 compose 就直接跑：

```bash
docker run -d --name yx-tools -p 8080:8080 -e TZ=Asia/Shanghai -v $PWD/data:/data ggshuai/yx-tools:latest
```

时区提示：定时任务的「每天固定时刻」按服务器/容器本地时钟执行，NAS 上记得给容器
设 `TZ` 环境变量（如上），设错时区任务会在别的时间跑。镜像已内置 tzdata。

镜像同时发布在 Docker Hub（`ggshuai/yx-tools`）和 GHCR（`ghcr.io/byJoey/yx-tools`），
amd64 / arm64 都有。换存放位置改环境变量 `YX_DATA_DIR` 即可。
内置定时任务在容器里照常工作，不需要宿主机 cron。

## 参数

测速：

| 参数 | 说明 | 默认 |
| :--- | :--- | :--- |
| `-colo` | 机场码，逗号分隔，如 `HKG,SIN`；留空不限 | 空 |
| `-ipv6` | 测 IPv6 段 | 否 |
| `-n` | 测速数量 | 10 |
| `-sl` | 下载速度下限 MB/s | 1 |
| `-tl` | 平均延迟上限 ms | 1000 |
| `-t` | 延迟测速线程数，路由器上别开太高 | 200 |
| `-port` | 测速端口 | 443 |
| `-url` | 测速地址 | 内置 |
| `-f` | 自定义 IP 文件，每行一条，支持 `IP:端口` | 自动下载 |
| `-nodl` | 只测延迟，跳过下载测速 | 否 |
| `-dall` | 下载阶段全部测完再按速度取前 N，而不是凑够就停（很慢） | 否 |
| `-dt` | 单个 IP 的下载测速时长上限，秒 | 10 |
| `-mt` | 整轮测速的时长上限，秒；0 不限，到点拿已测出的结果收工 | 0 |
| `-o` | 结果文件 | result.csv |

上报（跟在 `test` 后面，或单独用 `upload`）：

| 参数 | 说明 |
| :--- | :--- |
| `-upload` | `api` 上报 cfnew，`github` 推到仓库 |
| `-domain` `-uuid` | cfnew 的 Worker 域名和 UUID |
| `-repo` `-token` | GitHub 仓库 `owner/repo` 和 Token |
| `-path` | 仓库内文件路径，默认 `cloudflare_ips.txt` |
| `-limit` | 上报数量，默认 10 |
| `-clear` | 上报前清空已有 IP，建议带上，否则会越堆越多 |

界面：

| 参数 | 说明 | 默认 |
| :--- | :--- | :--- |
| `-listen` | 监听地址 | 127.0.0.1:8080 |
| `-no-open` | 不自动开浏览器 | 否 |

## 定时任务

网页里点右上角的时钟按钮：新建任务，选执行时间（间隔模式：每 1/6/12/24 小时或
自定义分钟数；每天固定时刻模式：24 小时制键盘输入，一天可多个时间点，如
`08:30`、`20:15`）、勾上报目标（GitHub / Worker 可同时勾）、填「优中选优」的
前 N 数量，保存即生效。每天固定时刻按服务器/容器本地时钟执行，表单里会显示
「服务器当前时间」作参考。
任务按保存时的页面设置运行（左侧参数 + 优选反代来源），改设置后重新保存一次即可更新。
每个任务都能停用、编辑、删除、立即执行，并显示上次/下次执行时间与执行日志。

内置调度在任何环境都能跑（包括 Docker / NAS），重启后任务保留在 `yx-config.json`。

### 分地区上传数量与固定附带列表

上报配置弹窗里可以配「分地区上传数量」：预设 吉隆坡 / 新加坡 / 香港 / 日本（东京+大阪+福冈
合并）/ 台北 五个按钮，每个填一个数量；也能手动输入任意机场码（如 `LAX`）加行；
另有「其他地区」数量兜底（配额地区之外的结果）。某地区凑不够配额时有两种策略：
「不足就少传」（默认）或「用其他地区补位」。配了地区数量后，上传总量 = 各配额之和，
原来「上报数量」输入框不再生效。注意分地区上传依赖测速结果带机场码，建议延迟测法
选「真实连接」。

「固定附带列表」里的条目（IP 或域名 + 名字）每次上传必定附带在结果**末尾**，不参与
测速、不筛选；与测速结果重复的 IP 自动去重，保留你命名的这条。Worker 上报与 GitHub
上传、手动与定时、命令行 `yx upload` 都生效。GitHub 里写成 `IP:port#名字`（域名默认
端口 443）。

### 下载策略

左侧面板「下载策略」两个选项：「测够了就停」（默认）凑够数量就收工；「全部测完再挑」
把通过延迟筛选的候选全部下载测完再按速度取前 N，结果更稳但慢很多。定时任务沿用
保存时的设置。

Linux / macOS 也保留了命令行挂 cron 的老方式：

```bash
# 每 6 小时测一次并上报
./yx cron -add "test -n 10 -sl 2 -upload api -clear" -at "0 */6 * * *"

# 看已登记的任务
./yx cron

# 清掉（只删本程序加的，不动你自己的任务）
./yx cron -remove
```

配置存在 `yx-config.json`（位置见下面「文件」一节），`-domain` `-uuid` 填过一次之后
命令里就能省掉。任务输出写到同目录的 `yx-cron.log`，添加时会打印完整路径。

Windows 用「任务计划程序」调用 `yx.exe test ...` 即可。

## 文件

跑完会生成这几个文件，默认落在当前目录；当前目录写不了（比如容器里、
装在只读位置）就自动退到程序目录、家目录 `~/.yx-tools`，最后是临时目录。
启动时会打印实际用的是哪个。想固定位置就设环境变量 `YX_DATA_DIR`。

- `result.csv` — 完整测速结果
- `ips_ports.txt` — 反代列表，`IP:端口` 一行一条
- `yx-config.json` — 配置，含 Token，注意别泄露
- `Cloudflare.txt` / `Cloudflare_ipv6.txt` — 缓存的官方 IP 段
- `yx-cron.log` — 定时任务的输出（设了定时任务才有）

## 相关

- [cfnew](https://github.com/byJoey/cfnew) — 配套的 Worker 面板
- [博客](https://joeyblog.net) ｜ [YouTube](https://youtube.com/@joeyblog) ｜ [TG 群](https://t.me/+ft-zI76oovgwNmRh)

## 致谢

测速内核来自 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)，MIT。

## 许可

MIT
