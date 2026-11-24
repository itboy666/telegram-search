# 服务器初始化安装

新部署不需要先填写 `.env` 或执行 SQL。启动 Go 程序、配置域名反向代理后，第一次访问域名自动进入 `/install`。完成向导后同一进程切换为正式网站，`all` 模式同时启用机器人和财务任务，无需手动重启。

## 构建并上传

在开发机安装前端依赖后构建部署包：

```sh
cd frontend
pnpm install --frozen-lockfile
cd ..
sh scripts/package.sh amd64
# ARM64 Linux 服务器改为：sh scripts/package.sh arm64
```

输出 `dist/starindex-linux-amd64.tar.gz`（或 arm64）。构建机需要 Go 1.26+、Node 22.12+；服务器运行部署包只需要 Linux、MySQL 8.0+ 与反向代理，不需要 PHP、Node 或 Go 工具链。程序包含时区数据；远程 MySQL 使用 TLS 时，系统需有可信 CA 证书。

部署包仅包含 Go 二进制、前端产物、Telegram iOS Icons、数据库迁移文件和部署示例，不包含开发机 `.env`、Token、密钥、登录资料、日志及业务数据。每台新服务器会生成自己的安装码和密钥。

以 `/opt/starindex` 为部署目录，解压后保留以下结构：

```text
/opt/starindex/
  starindex
  public/build/
  public/icons/
  public/favicon.svg
  database/migrations/
  deploy/
  runtime/
```

由专用的 `starindex` 系统用户拥有该目录，程序运行用户需要写入项目根目录和 `runtime`。不要给整个目录设置 777 权限，也不要把项目根目录作为 Nginx 静态目录。

前台运行：

```sh
cd /opt/starindex
./starindex all --root /opt/starindex --addr 127.0.0.1:8088
```

常驻部署可按服务器实际路径修改 `deploy/starindex.service`，交给 systemd 管理。先创建 `starindex` 系统用户和组、设置目录归属，再把服务文件放到 `/etc/systemd/system/starindex.service`，执行 `systemctl daemon-reload` 和 `systemctl enable --now starindex`。使用 `journalctl -u starindex -f` 查看日志；日志不会输出安装码、数据库密码或管理员密码。

## 配置域名

把域名解析到服务器，配置 HTTPS 证书，并参考 `deploy/nginx.conf` 将全部请求代理到 `127.0.0.1:8088`。将示例域名与证书路径替换为实际值，检查 `nginx -t` 通过后重载 Nginx。

Go 仅信任本机回环地址传来的 `X-Forwarded-Proto: https`，所以该示例要求 Nginx 与 Go 在同一主机。公网请求直接伪造转发头不会被认定为 HTTPS。不要将 Go 监听端口直接开放公网。如果部署到不同容器/主机，应先调整受信代理方案，不能直接照搬该配置。

域名通过 HTTPS 访问后即可安装。只有回环地址上的 localhost/IP 调试允许 HTTP；公网安装不通过明文连接提交凭据。

## 四步安装

1. **环境检查**：在服务器项目根目录读取 `.install-token`，粘贴到向导。安装码仅本地存储，页面和状态接口不会公开。校验前端产物、图标、迁移文件和写入权限。
2. **连接数据库**：输入主机、端口、独立库名、用户和密码，点击测试。支持 MySQL 8.0 及更高主版本，使用 utf8mb4；不接受 MariaDB。已存在的库必须为空。可以勾选不存在时自动建库，正式开始安装时才创建，测试连接不会写表。
3. **站点与管理员**：设置站点名称和独立超级管理员，密码为 10–72 字节。网站公开地址采用当前访问域名，后续可在后台调整。管理员拥有现有完整 RBAC 与字段权限管理能力。
4. **安装完成**：配置写入成功后显示进入后台和查看网站入口，使用刚设置的管理员账号登录。

安装数据库用户需要目标库的 SELECT、INSERT、UPDATE、DELETE、CREATE、ALTER、INDEX、REFERENCES 权限，以及 MySQL 命名锁功能；自动建库还需对应 CREATE DATABASE 权限。不要求访问其他业务数据库。安装程序会实际执行迁移，权限不足时保留进度并报告重试；连接测试通过并不代表所有建表权限均已具备。

机器人 Token、支付和广告等凭据由安装后后台配置，不从开发环境复制。新站不导入演示资源、不创建普通用户、不启用充值奖励或广告位。初始 Bot 搜索每页 12 条，后台可继续调整。

## 锁定、恢复与旧系统

- 创建 `.env`、`.bot-token.key`、`.commerce.key`、`install.lock`，文件权限为 0600。安装结束后移除 `.install-token` 和恢复文件；管理员密码以 bcrypt 保存，不输出明文初始密码文件。
- 数据库 `ts_installation` 保存本次安装归属及完成时间。安装进程有本机锁和 MySQL 锁，重复提交或不同目录同时安装同一数据库不会覆盖管理员。
- MySQL DDL 存在隐式提交，不能把整套建表视为一个可回滚事务。因此在建表前保存受保护的 `.install-state.json`，失败后使用原安装码点击“继续未完成安装”。恢复沿用原数据库、站点与管理员；已经提交的管理员不会再次创建或重置。[MySQL 官方说明](https://dev.mysql.com/doc/refman/8.4/en/implicit-commit.html)
- 若浏览器断开，重新打开 `/install` 即可检查状态。安装已成功则跳转后台；尚未完成则输入原安装码恢复。不要删除恢复文件后直接重新安装部分已建表的库。
- 已有 `.env` 的老系统按原配置启动，不进入安装向导；数据库连接失败也不会开放安装入口。`install.lock` 存在但 `.env` 丢失时停止启动，要求恢复原配置。不要通过删除文件的方式重装已有业务库。
- 老系统更新使用原来的 `migrate` 命令，增量增加 013 安装记录表，不创建完成记录，不重置账号、余额和资源。部署更新须保留原 `.env`、两份加密密钥、安装锁、数据库及 `runtime` 运行目录。
- 初始化和新增字段权限已覆盖现有 MySQL 8.0 测试；更高版本允许连接，但不等同于所有 MySQL 后续版本已经经过兼容认证。

备份必须同时包含数据库、`.env` 与两份加密密钥。安装文件位于静态目录之外，正常 HTTP 路由不能读取；不要把这些文件加入发布包或版本库。

## 开发验收

```sh
cd server
../runtime/tools/go/bin/go test ./...
../runtime/tools/go/bin/go vet ./...
# 使用随机独立测试数据库；只创建和删除 starindex_install_test_* 夹具库。
STARINDEX_INSTALL_TEST=1 ../runtime/tools/go/bin/go test -race ./internal/app -run '^TestInstall' -count=1 -v
```

安装测试使用本地 `.env` 的数据库连接凭据创建独立空库，不更改当前业务库。覆盖安装码、HTTPS/来源校验、静态路径保护、已有配置保护、空库初始化、管理员登录、RBAC、文件权限、失败恢复、并发安装和重启锁定。
