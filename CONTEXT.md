# yx-tools

Cloudflare 优选 IP 测速工具：从一个或多个 IP 来源中测出速度最快的 Cloudflare IP，并上报给订阅渠道（GitHub 仓库、cfnew Worker 面板）。

## Language

**优选 IP**：
测速后按下载速度降序排在最前的 Cloudflare IP。
_Avoid_: 好 IP、快的 IP

**候选来源**：
一次测速的输入 IP 集合。由 URL 列表、粘贴文本和随机 Cloudflare IP 合成，按 `IP:端口` 去重。
_Avoid_: 输入源、IP 池

**反代模式**：
直接测给定的 `IP:端口` 列表，给什么测什么，不抽样、不穷举网段。
_Avoid_: 代理模式

**定时任务**：
按固定频率自动执行「合成来源 → 测速 → 优中选优上报」的完整流程。每条任务持有自己的参数、来源与上报设置快照。
_Avoid_: 计划任务、cron 任务

**优中选优**：
把一轮测速结果按速度降序取前 N 条用于上报，覆盖远端旧列表。
_Avoid_: 去重上报

**上报**：
把优选 IP 写到外部目标：GitHub 仓库中的文本文件，或 cfnew 面板的优选 IP 列表。
_Avoid_: 上传、同步

**cfnew**：
配套的 Cloudflare Workers 面板，通过 `{domain}/{uuid}/api/preferred-ips` 读写优选 IP 列表。
