# 宝塔 Go 项目部署

服务器在 `/home/website/idcsobot` 检出 `main`，用 Go 1.26+、Node.js 22.12+、pnpm 10 和 Linux 的 `flock` 构建。宝塔负责运行 Linux 二进制，应用连接 MySQL 8.0+。在部署账户的终端确认 `go`、`node`、`pnpm` 在 `PATH` 中。Go SDK 位于其他目录时，可以用 `STARINDEX_GO=/绝对路径/go sh scripts/deploy-aapanel.sh` 指定。

## 首次部署

服务器可通过 HTTPS 克隆公开仓库。若项目目录已是该仓库的检出，直接进入「日常更新」。

```sh
git clone --branch main https://github.com/itboy666/telegram-search.git /home/website/idcsobot
cd /home/website/idcsobot
sh scripts/deploy-aapanel.sh
```

在宝塔「网站 → Go项目 → 添加项目」填写项目配置。[宝塔官方说明](https://docs.bt.cn/user-guide/site/go/) · [aaPanel 官方说明](https://www.aapanel.com/docs/Function/go.html)

| 面板字段 | 值 |
| --- | --- |
| 项目执行文件 | `/home/website/idcsobot/runtime/starindex` |
| 项目名称 | `idcsobot` |
| 项目端口 | `8088` |
| 执行命令 | `/home/website/idcsobot/runtime/starindex all --root /home/website/idcsobot --addr 127.0.0.1:8088` |
| 运行用户 | 有权访问项目目录、读取站点配置并写入项目目录和 `runtime` 的服务账户，例如 `www` |
| 开机启动 | 开启 |
| 放行端口 | 关闭 |
| 绑定域名 | `idcso.iid.hk`，开启外网映射和 HTTPS |

Nginx 将域名请求转发到 `http://127.0.0.1:8088`，保留原始 `Host`，并用 `proxy_set_header X-Forwarded-Proto $scheme;` 把访问协议传给 Go。HTTPS 转发头用于安装向导和会话安全判断。反向代理设置可参考 [Nginx 配置示例](../deploy/nginx.conf)。

首次安装访问 `https://idcso.iid.hk/install`。安装码在服务器项目目录的 `.install-token` 中。[服务器初始化安装](installation.md#四步安装)

已有站点沿用服务器的 `.env`、两份密钥、安装锁、数据库和 `runtime` 目录。切换到宝塔前，停止该站点原有的服务进程，避免重复运行同一机器人的长轮询。

## 日常更新

在开发机提交修改并推送到 `main`，然后用下面的命令更新服务器。

```sh
cd /home/website/idcsobot
git pull --ff-only && sh scripts/deploy-aapanel.sh
```

脚本按锁文件安装依赖，完成前端和 Go 构建后再发布产物。命令成功后，在宝塔项目的「服务状态」点击「重启」，然后检查本机和公开站点。

```sh
curl -fsS http://127.0.0.1:8088/healthz
curl -fsS https://idcso.iid.hk/healthz
```

涉及数据库结构的版本，先备份数据库和服务器密钥。部署并重启后，用超级管理员登录「系统设置 → 数据库升级」，查看待升级项并执行升级。也可以使用有权读取服务配置的账户在终端运行以下命令。

```sh
/home/website/idcsobot/runtime/starindex migrate --root /home/website/idcsobot
```

服务器配置和密钥集中保存在项目目录的 `.env`、`.bot-token.key`、`.commerce.key`，随数据库一并独立备份。
