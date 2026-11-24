# 星索 StarIndex

Go + MySQL 8 的 Telegram 资源搜索、用户和营销管理系统。Vue 3 / TypeScript / Element Plus 提供网站、用户中心与后台界面，统一使用 Telegram iOS Icons。

Go 后端是模块化单体，同一二进制运行 HTTP、Telegram 长轮询和支付广告任务，也可以将三类进程分别部署。原 PHP 文件保留为迁移对照及回退资料，Go 正常运行不调用 PHP。

## 本地入口

- 网站：`http://127.0.0.1:8088/`
- 后台：`http://127.0.0.1:8088/admin`
- 用户中心：`http://127.0.0.1:8088/user`
- Telegram 绑定与账号安全：`http://127.0.0.1:8088/user/security`
- 健康检查：`http://127.0.0.1:8088/healthz`

沿用 `.env` 中的数据库连接和原有管理员账号。账号资料位于本地忽略文件 `本地登录信息.txt`，不要提交到版本库。已有资源、用户、角色、余额、流水、广告及机器人配置保留。

## 启动

通过 Git 更新服务器并用宝塔管理 Go 进程的步骤见 [宝塔 Go 项目部署](docs/aapanel-deployment.md)。

新服务器推荐使用初始化安装向导：构建 `sh scripts/package.sh amd64`（ARM64 改为 `arm64`），将生成的 Linux 部署包上传到服务器，启动 `./starindex all --root /opt/starindex --addr 127.0.0.1:8088` 并配置 HTTPS 域名反向代理。未配置的新站首次访问域名自动进入 `/install`，完成环境检查、数据库连接和管理员创建后立即启用网站并锁定安装入口。详见 [服务器初始化安装](docs/installation.md)，部署示例在 `deploy/`。已有系统仍沿用原配置和下面的增量迁移命令。

本机已安装项目隔离的 Go 工具链，系统其他项目不受影响：

```sh
# 构建前端；修改 Vue 后需要重新执行
sh scripts/build.sh

# 构建并运行数据库增量迁移
sh scripts/go-dev.sh migrate

# 构建并同时运行 HTTP、机器人和财务任务
sh scripts/go-dev.sh
```

Ctrl+C 优雅停止。直接使用二进制时：

```sh
runtime/starindex all --root . --addr 127.0.0.1:8088
runtime/starindex serve --root . --addr 127.0.0.1:8088
runtime/starindex bot --root .
runtime/starindex commerce --root .
runtime/starindex maintain --root .
```

切换前停止旧 PHP supervisor（历史入口 `php scripts/dev.php stop`），不要让同一个 Bot 同时运行两个长轮询进程。命令 `serve` 不启动机器人和财务 worker。

新环境需要 Go 1.26+、MySQL 8.0+、Node 22.12+，前端目录运行 `pnpm install --frozen-lockfile`。`scripts/install-go.sh` 为本机 macOS ARM64 提供已校验下载；其他系统从 Go 官方渠道安装工具链。迁移在空库中会创建表和随机初始管理员，密码仅写入权限为 0600 的 `本地登录信息-Go.txt`；已有管理员不会重置。

## 权限与身份

- 管理员表、普通用户表、会话表和 Cookie 分离。
- 后台 18 个业务权限模块，角色可配置读、新增、修改、删除、审核等操作。
- 字段支持隐藏、只读、可编辑；服务端同时检查读响应、写字段、筛选及排序。
- 多角色授权取并集，停用和撤权即时在后续请求生效；超级管理员保护和审计记录完整保留。
- 用户可在网站发起 Telegram 登录或绑定，在机器人私聊确认后回到原浏览器完成。请求 5 分钟有效、仅使用一次，绑定要求原网站账号及原会话。
- 已有独立 Telegram 账号不会自动与另一个网站账号合并资金或历史；可直接通过 Telegram 登录原账号。
- 设置网站密码后可解绑 Telegram；密码修改、解绑会撤销其他登录会话。Bot `/login` 可主动获取一次性网站登录码；`/account` 和 `/wallet` 直接打开机器人内钱包。

机器人私聊支持 `/recharge` 充值、文字/截图凭证、TRC20 核查，以及 `/ads` 填写广告、预览、确认扣款和查询审核状态，无需跳转网站。资金与网站共用，广告审核后生效。操作与并发恢复说明见 [机器人内充值与广告](docs/bot-commerce.md)。

命令菜单按私聊、普通群成员、群管理员分别注册：个人账户和财务功能只在私聊可见、可用，群成员只显示搜索帮助，群管理员额外显示群管命令。钱包账户 ID 显示本人 Telegram ID；「我的」已移除网站登录入口，旧按钮仅刷新钱包。

私聊菜单、帮助和填写提示只保留最新临时页面：按钮优先原地更新，新页面发送成功后清理旧提示及已消费的普通输入。搜索结果、收录回执、充值与广告订单、付款凭证和交易哈希继续保留；群消息与更新前的聊天历史不参与清理。规则、持久重试与恢复边界见 [机器人内充值与广告](docs/bot-commerce.md#聊天整洁与消息保留)。

## 业务功能

网站和 Bot 共用公开搜索服务、链接规范化与重复校验。支持资源/分类/收录管理、用户禁投与审核、收藏、API 密钥和额度；Bot 支持欢迎语动态标签、可配置菜单、群搜索、仅本人翻页、引用消息删除降级、`/add 链接` 自动提取标题、群广告拦截和多种验证码。

Bot 新搜索默认只展示群组和频道，首排提供「群频道 / 群组 / 频道 / 帖子 / 更多筛选」。帖子按公开 Telegram 消息链接识别，包含图文、视频、音乐、文件等；「更多筛选」保留原有分类及「全部」。切换分类从第一页重新生成快照，翻页保留当前筛选和结果顺序。网站/API 默认仍搜索全部类型；更新前的旧搜索会话保留原来的范围。

用户中心包含钱包、充值、资金流水、每日首搜奖励和广告购买。USDT 使用六位小数整数最小单位，扣款和流水事务提交。TRC20 核账校验地址、合约、金额、时间、交易成功及确认数，并使用唯一交易哈希防止重复入账。广告审核通过后计时，拒绝退款；模糊发送状态需人工核实，避免自动重复投放。

Bot Token、Tronscan API Key 在后台加密配置。`.bot-token.key`、`.commerce.key` 必须与数据库一同安全备份。搜索奖励和广告位是否开放按后台实际设置执行。

## 群内直接搜索

机器人加入普通群、超级群或话题群后，成员直接发送 `服务器` 等关键词即可搜索，无需 `/search` 或 @机器人。结果回复发起消息，话题群保留原话题；翻页和分类筛选仅限发起人，其他成员点击会收到提示。

普通成员身份的机器人需由所有者在 BotFather 的 `/setprivacy` 中选择目标机器人并设置 `Disable`，否则 Telegram 不会把普通群消息交给机器人。更改后，已加入的群需将机器人移出并重新添加，新加入的群直接使用新设置。管理员身份的机器人可以接收普通群消息。

群主和当前管理员可发送 `/groupsettings` 管理本群验证、广告拦截和防刷屏。总后台验证策略继续生效；广告拦截另需机器人拥有管理员及删除消息权限。机器人消息、服务消息、自动转发和匿名频道身份不会触发搜索，编辑消息不重复搜索。

## 群管理与随机搜索

后台「机器人管理 → 群组安全」支持按群设置消息字数上限、媒体/转发拦截、新人验证和欢迎模板，沿用字段级 RBAC。超长消息静默删除且不触发搜索。欢迎语支持用户、群、机器人及时间等 14 个标签，显示字数和预览；新规则默认关闭。

新人验证与搜索验证码分别配置。超级群中新人先禁言，通过私聊完成按钮、图片字母数字、四则运算、emoji、顺序 emoji 或随机验证，成功后恢复群默认权限；超时可保留禁言或移出。需要机器人管理员及限制成员权限。检测到其他管理员调整或权限操作结果不确定时交由人工核对。未通过者无法使用旧搜索结果和旧搜索验证码绕过验证。

新搜索使用等概率随机顺序，翻页复用两小时快照；「换一组」重新抽取。本次最多浏览 100 页、最多 5,000 条，页面同时展示实际匹配总数和窗口说明。点赞推荐功能已取消：机器人、网站和后台不显示推荐次数或按钮，旧推荐接口返回已取消，历史点赞不参与排序。

实现细节、并发边界及一万至十万群的后续架构见 [群管理与随机搜索](docs/group-management-and-recommendations.md)。当前仍是单 Bot 长轮询与 MySQL 搜索，没有启用文档中的集群队列、Redis 或搜索引擎。

## 数据导入

Go 导入器支持 JSON 和 CSV，默认只预检：

```sh
runtime/starindex import --root . --file runtime/imports/example.json --source my_dataset
runtime/starindex import --root . --file runtime/imports/example.json --source my_dataset --apply
```

JSON 使用 `{"rows":[{"title":"资源名称","url":"https://t.me/example","type":"channel"}]}`。已有 XLSX 采集流程生成的规范化 JSON 可以直接复用。导入分批事务处理，同一链接只保留一条，已有下架/封禁记录不会被导入覆盖；每次运行保存可追溯报告。没有在本轮重新下载或伪造采集数据。

## 验证

```sh
cd server
../runtime/tools/go/bin/go test ./...
../runtime/tools/go/bin/go vet ./...
# 在当前本地 MySQL 上创建并清理隔离测试夹具，不进行真实转账或发送
STARINDEX_TEST_MYSQL=1 STARINDEX_DB_TEST=1 STARINDEX_AUTH_TEST=1 STARINDEX_BOT_TEST=1 STARINDEX_RECOMMENDATION_TEST=1 \
  ../runtime/tools/go/bin/go test -race ./internal/app -count=1
```

跨语言 HTTP 兼容测试仍保留在 `tests/*.php`，仅测试工具使用 PHP，可通过 `STARINDEX_TEST_URL` 指向 Go 服务。新业务测试位于 `server/internal/app/*_test.go`。

当前搜索使用 MySQL LIKE 和有限页码。扩容方案与运行边界见 [Go 架构说明](docs/go-architecture.md)，资金与群安全验收见 [专项说明](docs/go-commerce-security.md)。
