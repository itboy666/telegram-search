# 星索 StarIndex · PHP 历史版本说明

> 此文件保留迁移前的实现与验收记录。当前运行入口、技术栈和命令以项目根目录 README.md 为准。

基于 ThinkPHP 8、Vue 3 / Element Plus、MySQL 8 的 Telegram 资源搜索与管理系统。当前是适合早期运营的模块化单体：网站、后台、开放接口和机器人复用同一个搜索及审核服务。

## 本地使用

已配置的地址：

- 网站：http://127.0.0.1:8088/
- 后台：http://127.0.0.1:8088/admin
- 用户中心：http://127.0.0.1:8088/user
- 管理员账号与随机初始密码：项目根目录 `本地登录信息.txt`。
- 数据库连接：`.env`。Bot Token 可在后台「机器人连接」配置；未在后台保存时兼容 `.env`。敏感文件已加入 `.gitignore`。

在项目根目录启动网站和机器人：

```sh
php scripts/dev.php
```

保持终端运行，Ctrl+C 同时停止本次启动的两个进程。日志位于 `runtime/web.log`、`runtime/bot.log`。本地开发只监听 127.0.0.1，外部设备无法直接访问。PHP 内置服务器用于本地开发；部署时使用 Nginx + PHP-FPM，网站根目录必须指向 `public`。

也可以在另一个终端运行 `php scripts/dev.php stop`，停止该脚本启动的本地服务。

修改前端后重新构建：

```sh
sh scripts/build.sh
```

也可在 `frontend` 下执行 `pnpm dev`，访问 http://127.0.0.1:5178/ 。后端仍需在 8088 运行。Node 不在 PATH 时，构建脚本会尝试这台电脑的 Codex 内置 Node；其他电脑请正常安装 Node 22.12+。

## 重新安装

本项目锁定的开发依赖需要 PHP 8.4.1+（实际验证 8.4.23）、pdo_mysql、curl、mbstring、openssl、Composer 2、MySQL 8.0+ 和 Node 22.12+。

1. `composer install`。
2. 将 `.example.env` 复制为 `.env`，设置已有数据库连接和 Bot Token。
3. `php think app:install` 创建表、随机初始管理员、4 个分类与3个真实官方频道。重复执行不会重置管理员或清空内容。
4. 在 `frontend` 运行 `pnpm install --frozen-lockfile`，然后在根目录运行 `sh scripts/build.sh`。
5. `php think bot:check` 验证机器人，再运行 `php scripts/dev.php`。

ThinkPHP 的 8.x 版本由 Composer 锁定（当前安装 8.1.4）。filesystem 使用 3.x，避开旧脚手架 filesystem 2.x 依赖链的安全公告。

## 功能

| 应用 | 可使用的功能 |
| --- | --- |
| admin | 真实数据概览、资源管理、分类与审核、普通用户管理、独立管理员管理、RBAC 角色及字段权限、机器人状态与 Token 配置、欢迎语与菜单可视化配置、API 密钥、站点设置、操作日志 |
| index | 中文搜索首页、关键词与类型/分类筛选、精选/成员排序、分页、资源详情、公开 Telegram 链接 |
| user | 注册/登录、收藏、提交收录、查看审核状态和备注 |
| api | 网站公开查询接口，以及带 Bearer Token、每日额度和分钟限流的开放搜索 API |
| Telegram | 自定义 /start 菜单、私聊与群聊搜索、搜索者专属筛选及翻页、话题群回复、热搜、发现、榜单、我的、帮助、链接提交待审核 |

Vue 3 使用 Element Plus（Element UI 的 Vue 3 对应方案）。白色主题、统一控件高度和间距规范见 `design-system/starindex/MASTER.md`。

## Telegram 调试

```sh
php think bot:check
php think bot:poll
# 单次轮询（用于诊断）
php think bot:poll --once
```

- 私聊 `/start` 显示后台配置的欢迎语与按钮；`/help` 显示使用说明；直接发关键词或 `/search 关键词` 搜索；`/discover` 浏览全部。
- 私聊发送 `/add` 查看收录格式；只需 `/add https://t.me/username` 即可提交公开 Telegram 链接，系统自动读取当前公开页面的标题、简介及可识别的资源类型，保存为待审核申请，通过后台审核后发布。公开帖子链接读取对应帖子的内容或文件名称；获取失败时提示重试，不保存占位标题。机器人命令菜单、帮助和「提交收录」按钮统一使用 `/add`；旧命令 `/submit` 仍兼容，旧格式附带的名称和简介不覆盖自动获取的数据。网站提交仍使用现有表单。
- 支持私聊、普通群与超级群；群搜索复用本站已发布的资源，不读取第三方机器人数据库，也不自动抓取全网频道和群组。
- 同目录使用文件锁，只运行一个 worker；后台更新 Token 后，worker 在当前轮询结束后自动加载，无需手动重启。正常轮询最长 20 秒，网络超时或重试时可能更久。
- 若已有 webhook，会调用 `deleteWebhook` 切换到长轮询，保留待处理更新。
- 搜索会话有效 2 小时；按钮绑定发起用户、聊天、消息和页面版本。旧按钮或他人的按钮不会修改当前结果。
- 搜索结果采用紧凑列表：每条仅显示 Telegram iOS 图标、标题链接和成员数，不显示采集说明或资源简介。分类首行是群组、频道、视频、更多筛选；展开后第二行是全部、音乐、图文、其他、店铺，第三行是机器人、文件、网站。使用公开目录数据的列表末尾保留来源与许可署名。
- 默认每页 12 条，在「Telegram 机器人 → 搜索展示」设置为 1–30 条。配置即时作用于新搜索；已有搜索固定原条数，展开/收起筛选保留当前页，切换类型回到第 1 页。网页与开放 API 分页设置独立。
- 已有安装升级运行 `php think app:upgrade-bot-search`，新增会话分页与筛选状态字段，再重启长轮询加载代码；可重复执行。旧会话保留 5 条分页。全新安装由 `app:install` 自动迁移。
- offset 在处理完成后保存，已完成 update 去重；429 按 retry_after 等待，暂时网络错误退避重试；401/404/409 暂停该凭据的轮询并等待后台重新保存，避免持续冲突，也保留后台修复入口。
- Telegram 的远端发送和本地数据库无法原子提交，极端宕机窗口可能重复发送首条结果。不能宣称严格 exactly-once。翻页以原消息编辑和版本检查处理重试。
- 遵循 [Telegram Bot API](https://core.telegram.org/bots/api#getupdates) 的轮询与回调接口。

### 群聊搜索与操作权限

将机器人添加到你管理的群组，并允许机器人发送消息。当前机器人是 `@kaikaikaidevBot`，在群里发送：

```text
/search@kaikaikaidevBot Python
```

结果会回复原关键词消息；话题群会保留同一话题。每条结果单独保存发起人的 Telegram 用户 ID、群 ID、消息 ID、页码和版本。同群多人同时搜索相同关键词也各自独立。下一页、上一页、所有分类、展开/收起与搜索帮助都经过服务端归属校验。非发起人点击时使用 Telegram 原生弹窗提示：

> 不能操作别人的搜索结果。请发送关键词，创建你自己的搜索。

拒绝操作不会编辑结果、发送群内提示、更新搜索状态或消耗发起人的搜索额度。弹窗不缓存，避免不同成员共用旧的回调响应。此限制针对交互按钮；群里已展示的公开资源链接仍可由其他成员查看和打开。

- `bot:check` 显示是否允许加群、隐私模式以及当前机器人的完整搜索指令。2026-11-16 实测当前机器人允许加群、隐私模式开启。
- 隐私模式开启时，优先使用 `/search@机器人用户名 关键词`；回复该机器人的消息发送关键词也可触发。普通 `/search 关键词` 仅在 Telegram 将该指令交给本机器人时有效。
- 若希望像参考群一样直接发送普通关键词，在 **BotFather → /mybots → 选择机器人 → Bot Settings → Group Privacy** 关闭隐私模式（或 `/setprivacy` → 选择机器人 → Disable），再将机器人重新加入群组使配置生效。它随后会接收群内普通消息；本系统只处理人类用户的文字搜索，不处理其他机器人的消息、频道自动转发或服务消息。无需为了搜索额外授予删消息、封禁成员等管理权限。
- 已作为群管理员的机器人通常也能收到普通群消息；`getMe` 返回的隐私标志反映 Bot 的全局设置，不表示每个群里的管理员权限。
- 群内 `/start`、`/help` 给出群搜索说明；`/add`（含 `/add@机器人用户名`）引导私聊提交，不在群里创建收录申请；搜索结果底部使用「搜索帮助」，只向点击者弹窗。个人资料、自定义主菜单和提交收录保留在私聊中。
- 发给其他机器人的命令会被忽略。匿名管理员、频道代发等 `sender_chat` 身份不能建立个人搜索会话；使用搜索指令时会提示切换为个人身份。
- 所有按钮仍保留 2 小时过期和页面版本校验。已失效或旧版本的按钮不会改变结果。
- 本次修改不新增数据表；更新代码后重启 `bot:poll`。群权限回归可运行 `php tests/bot_groups.php`，使用模拟 Telegram 事件和真实 MySQL 回滚事务，不向任何真实群发送夹具消息。

Telegram 规则参考：[Privacy Mode](https://core.telegram.org/bots/features#privacy-mode)、[answerCallbackQuery](https://core.telegram.org/bots/api#answercallbackquery)、[ReplyParameters](https://core.telegram.org/bots/api#replyparameters)。

## 后台 Bot Token 配置

群管理员命令、私聊/群聊验证码、广告规则和防刷设置见 [机器人安全使用说明](docs/bot-security.md)。后台入口在「机器人管理 → 搜索验证 / 群组安全」，已有安装先执行 `php think app:upgrade-bot-security` 并重启长轮询。首次升级默认不强制验证码，可按需在后台或群内开启。

进入「机器人连接」（`/admin/bot`），填写新的 Bot Token，点击「验证并保存」。服务端调用 Telegram `getMe` 核对机器人身份，验证成功才保存；格式错误、无效 Token 或连接失败均保留原配置。保存后连接状态自动刷新。

- 后台配置优先于 `.env`。`bot:poll`、`bot:check` 和 `bot:icons` 统一读取相同配置。
- Token 使用 AES-256-GCM 加密保存在 `ts_settings.telegram_bot_credentials`；不出现在通用设置、公开接口、状态响应、操作日志或前端构建中。页面仅显示配置状态，输入框中的「显示」仅针对本次输入。
- 独立加密密钥保存在项目根目录 `.bot-token.key`，首次保存时自动生成，权限 `0600`，已加入 `.gitignore`。迁移或恢复后台配置时应同时安全备份此文件；网站根目录必须为 `public`。密钥缺失或密文损坏不会退回旧 `.env` Token。
- 非超级管理员需要 `bot.read` 查看页面、`bot.update` 修改操作权限及 `bot.token` 字段写权限。只读权限只查看状态，不能获取完整凭据。
- 搜索每页条数独立使用 `bot.page_size` 字段权限，支持隐藏、只读、读写。`POST /admin/api/bot` 可单独提交 `page_size`，无须填写 Token；同时提交时，任一配置验证失败都不会部分保存。
- 更换为同一个机器人时保留 offset；不同机器人不继承旧 offset、运行状态和图标诊断。已在途的旧配置请求不能覆盖新配置状态，切换后不继续处理旧轮询批次。
- 自动加载要求 `bot:poll` 进程正在运行；保存配置不会在 Web 请求内启动进程。首次使用仍需运行 `php scripts/dev.php` 或部署独立的轮询服务。

## 欢迎语、按钮与图标

进入后台「欢迎语与菜单」（`/admin/bot-menu`）配置：

- 欢迎语、欢迎图标、底部提示，支持换行、动态标签和实时预览。
- 按行添加、移动和移除按钮；每行最多 5 个，最多 10 行、24 个按钮。
- 每个按钮的文字、Telegram iOS Icons 图标、颜色、启停状态。
- 点击动作：热搜、发现、群频道榜、我的、帮助、提交说明、指定关键词搜索、自定义文字回复、HTTPS 链接。

保存后下一次 `/start` 即时读取新配置，不需要重启；修改前的菜单按钮会提示重新发送 `/start`。系统设置页只保留编辑入口，避免两处欢迎语互相覆盖。热搜取近 7 天真实搜索记录，群频道榜按已录入成员数排序，不声称是实时全网榜。

欢迎语与底部提示语均支持以下标签。可点击「插入动态标签」中的按钮，在光标处插入或替换选中文字，也可直接输入标签；插入位置可在两个字段间切换。

| 标签 | 发送时显示 |
| --- | --- |
| `{{nickname}}` | Telegram 名字与姓氏组成的昵称，缺失时为「Telegram 用户」 |
| `{{username}}` | 带 `@` 的用户名，未设置时为「未设置」 |
| `{{user_id}}` | 当前 Telegram 用户 ID |
| `{{first_name}}` | 名字，缺失时为「Telegram 用户」 |
| `{{last_name}}` | 姓氏，缺失时为「未设置」 |
| `{{language_code}}` | Telegram 提供的语言代码，缺失时为「未知」 |
| `{{is_premium}}` | 是否为 Telegram Premium：是 / 否 |
| `{{datetime}}` | 当前日期和时间，例如 `2026-11-16 10:30:00` |
| `{{date}}` | 当前日期，格式 `Y-m-d` |
| `{{time}}` | 当前时间，24 小时制，格式 `H:i:s` |
| `{{weekday}}` | 中文星期，例如「星期一」 |
| `{{timezone}}` | 项目配置的时区，当前为 `Asia/Shanghai` |
| `{{site_name}}` | 系统设置中的站点名称 |

示例：

```text
你好，{{nickname}}！
用户名：{{username}} · 用户 ID：{{user_id}}
欢迎使用 {{site_name}}，发送关键词开始搜索。
当前时间：{{datetime}} · {{weekday}}
```

`/start` 和「返回主菜单」都使用本次操作携带的用户信息，时间在生成消息时按 `config/app.php` 的 `default_timezone` 计算。已发送的消息不会自行更新时间。后台预览明确使用示例用户，不读取或展示真实用户资料；预览时钟每秒更新。

未知标签和不完整的双花括号会阻止保存并显示字段错误。标签仅作一次纯文本替换，用户昵称中的标签不会再次展开，特殊字符会在替换后转义。欢迎语和底部提示语展开后分别最多显示 1,800 和 240 个 Unicode 字符，超出部分以 `…` 收尾；后台预览使用相同规则并提示截断。

网页与 Bot 统一使用 **Telegram iOS Icons / tgiosicons**。2026-11-16 已同步整包 **399 款原始图标**；`config/tgios_icons.php` 保留 400 个兼容名称（原有 `menu` / `other` 指向同一图标），既有菜单无需重配。按钮与欢迎语共用带图标预览的可搜索选择器，按原始 ID 去重，支持中文、英文、图标编号和完整 ID；同类变体以包内编号区分，长列表使用虚拟滚动。

Telegram 消息用 `<tg-emoji>`，按钮用 `icon_custom_emoji_id`；所有 ID 以字符串保存。网页用同一份原始 TGS 导出的 SVG，通过 CSS mask 跟随文字颜色并完整显示，不裁切边缘。来源记录在 `public/icons/telegram-ios/SOURCE.md`，正常运行不需要额外下载或安装 Python。

Telegram 是否保留自定义图标取决于 Bot 账号资格及客户端支持。代码会检查真实发送响应，后台显示保留情况；诊断只保存数量与状态，不保存消息正文或聊天标识。2026-11-16 换用 `@kaikaikaidevBot` 后，欢迎图标和 6 个菜单图标均被 Telegram 保留，客户端显示成功。Token 可在后台加密保存，未保存时兼容 `.env`。

仅当图标包更新或需要补齐本地图标资源时：

```sh
python3 -m venv runtime/icon-tools
runtime/icon-tools/bin/pip install lottie==0.7.2
php think bot:icons --all
# 也可以指定已有转换器及并行进程数（1–4，默认 4）
php think bot:icons --all --converter /absolute/path/lottie_convert.py --workers 4
# 只核对并补齐当前配置的图标资源
php think bot:icons
```

命令复用已下载的 TGS 和有效 SVG，中断后可以再次运行；全部资源导出成功后才发布扩展图标清单。原始 TGS 与包含来源 SHA-256 的 `manifest.json` 保存在 `resources/icons/telegram-ios/`。图标名称和中英文检索词按原包 emoji 标注整理；它们是搜索分类，不是作者提供的独立图标标题。更新图标配置属于代码变更，需重启常驻 Bot 进程；在后台选择已有图标无需重启。

## 接口约定

成功：`{"code":0,"data":{},"message":"ok"}`；错误使用相应 HTTP 状态码及 `message`，表单可附带 `errors`。

```text
GET /api/public/config
GET /api/public/categories
GET /api/public/search?q=Telegram&type=channel&page=1&page_size=12
GET /api/public/resources/1

GET /api/v1/search?q=Telegram&page=1
Authorization: Bearer YOUR_API_KEY
```

开放 API 默认关闭，在后台「系统设置」开启后，再到「开放 API」创建密钥。只存储密钥的 SHA-256，明文仅创建时展示一次。额度为按服务端日期的每日调用次数，另有每分钟 60 次限制。当前供服务端集成使用，不开放跨域浏览器调用。

管理与用户接口使用 HttpOnly / SameSite Cookie 会话，写操作要求 `X-CSRF-Token`。登录响应和 `/admin/api/me`、`/user/api/me` 返回对应 csrf_token；权限范围分离。密码使用 PHP password_hash。

## 管理员、普通用户与 RBAC

管理员与普通用户使用独立身份及会话存储：

| 身份 | 账号表 | 会话表 | 登录入口 | 登录接口 |
| --- | --- | --- | --- | --- |
| 管理员 | `ts_admins` | `ts_admin_sessions` | `/admin/login` | `POST /admin/api/login` |
| 普通用户 / Telegram 用户 | `ts_users` | `ts_sessions`（仅 user scope） | `/user/login` | `POST /user/api/login` |

同名账号可分别存在，密码、会话、停用和注销互不影响。普通用户表不再包含 `role` 字段；网站注册和 Bot 只写普通用户表。Cookie 分别为 `starindex_admin`、`starindex_user`，CSRF 也独立验证。

后台新增「管理员管理」和「角色与权限」。现有管理员已迁移为超级管理员，原账号、密码和有效后台会话保留。超级管理员保持启用，拥有全部后台权限；仅超级管理员可创建管理员、分配角色、重置密码和配置授权。新建管理员不会自动获得超级权限；未分配有效角色时只能登录、查看自己的账号及退出。

RBAC 使用 `ts_roles`、`ts_admin_roles`、`ts_role_permissions`、`ts_role_fields`：

- 一个管理员可关联多个角色，授权取并集；停用角色不参与授权。未授权默认拒绝，没有显式拒绝规则覆盖其他角色。
- 模块操作权限覆盖工作台、资源、分类、收录审核、普通用户、站点设置、Bot 菜单、API 密钥、日志和机器人连接；新增、编辑、审核、归档等分别授权。
- 字段分为「隐藏」「只读」「可编辑」。写字段必须同时有查看权限，并且持有所需操作权限。例如，编辑资源名称需要 `resources.update` 与 `resources.title` 写入权；归档还需要 `resources.archive` 与 `resources.status` 写入权。
- 后端每次请求重新读取有效角色和授权。读响应按白名单裁剪，写请求若包含无权修改或未知字段，整次请求返回 403；部分更新保留未提交字段。工作台嵌套资源、日志和 Bot 数据也经过权限检查。
- 搜索只匹配有权读取的字段；无权读取的筛选或排序字段不能用于查询。记录 `id` 始终可见；类型/分类的显示名称跟随对应字段。密码哈希、密钥哈希及会话凭据不进入业务权限目录。
- 前端导航、操作按钮、字段显示和只读状态同步授权；直接访问未授权页面进入访问权限提示页。最终授权由后端执行。
- 修改角色或角色归属后，下次请求立即生效。修改密码、停用管理员会撤销其后台会话；当前超级管理员无法被停用。

权限目录集中在 `config/admin_permissions.php`，新增业务模块需要同时配置接口映射、字段白名单和前端入口。当前是模块操作与字段级 RBAC，不包含按租户、部门或资源归属筛选的数据行权限。`bot_menu.rows` 作为一个配置字段授权，暂未细分到单个按钮的属性。已公开到网站/Bot 的内容仍遵循公开接口规则；后台字段隐藏不改变公开资源的发布状态。

例如，为内容运营角色勾选「查看资源」「编辑资源」，将「资源名称」设为可编辑、「发布状态」设为只读、其他字段保留隐藏。绑定该角色的管理员只能修改名称，不能读取链接或归档资源。新建资源若没有状态写入权，默认创建为隐藏状态。

升级旧版数据库：

```sh
php scripts/dev.php stop
php think app:upgrade-access
php scripts/dev.php
```

`app:install` 也会应用此迁移。迁移可重复运行，版本记录在 `ts_schema_migrations`；创建表的 DDL 与账号迁移事务分开执行。迁移前的管理员记录和会话保存在 `runtime/migrations/admin-split-*.json`，文件权限为 `0600`，包含密码哈希及会话凭据，不应分享或提交。迁移保留管理员 ID，因此原审计记录及审核人引用继续指向对应管理员。旧管理员若关联普通用户业务数据，会保留一个停用且清除登录密码的用户身份以保护归属；本地现有管理员无这类关联，已从普通用户表移出。

## 数据与扩展边界

MySQL 已在 8.0.46 实测。InnoDB / utf8mb4、参数绑定，未使用已废弃的 `VALUES()` 更新语法；后续 MySQL 8.4 / 9.x 升级仍需运行集成测试，不能把 SQL 兼容设计等同于已经在这些版本验证。

当前使用 MySQL LIKE 搜索，适合小规模起步；页码最多 100，单页最多 50 条。它不承诺千万数据或几十万人同时翻页的性能。后续可替换 `SearchService` 为 OpenSearch 实现，加入 Redis 搜索会话/缓存和消息队列，再拆分 Bot worker，而不用重做四个应用的交互。

已收录内容来自人工录入、用户提交审核和下述可追溯的数据导入，不代表已取得原 SOSO 机器人的数据库。网站账号与 Telegram 身份目前独立，没有做自动绑定。支付、会员、广告投放、持续网络采集和数据行权限尚不在这一版中。

## 本地测试数据导入

2026-11-16 已下载指定 Telegram 会话中的 4 份 XLSX（搜群、服务器、开发、视频），原表共 1,336 行。按公开链接去重后插入 1,159 个群组：其中 1,140 个可搜索，19 个名称疑似涉及未成年人性内容的记录已隐藏；18 行重复、159 行邀请链接未导入。原有 3 个官方频道保持原样。

原始表格、逐行来源、整理后的 JSON 和复核结果位于 Git 忽略的 `runtime/imports/telegram-20261116/`。原表的「人数」映射为群成员数，「在线人数」保留为采集快照说明；没有根据文件名把群组改成视频类型，也没有编造标签或分类。人数不是实时查询结果，链接可访问性尚未逐个验证。

可重复运行插入型导入命令，已有链接（包括隐藏资源）只跳过，不覆盖：

```sh
# 默认只预检
php think resources:import runtime/imports/telegram-20261116/resources.json --source=xlsx_20261116_groups
# 执行插入
php think resources:import runtime/imports/telegram-20261116/resources.json --source=xlsx_20261116_groups --apply
```

命令接受包含 `rows` 数组的 UTF-8 JSON，每行使用 `title`、`url`、`type`、`members`、`description`、`tags` 字段；可附 `source_file`、`source_sheet`、`source_row` 定位原记录。仅支持公开 Telegram 链接。数据库异常回滚该批次，逐行结果及新建 ID 写入 `runtime/import-reports/`。

### 十万条公开频道数据

2026-11-16 已从 [Zenodo 14749767](https://zenodo.org/records/14749767) 下载并校验 **Telegram channels graph dataset**。作者 **Svitlana Shapovalova、Anton Sofiienko**，发布于 **2025-01-27**，许可为 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)。只使用 `Channels.csv` 中的频道元数据，不导入消息正文、转发关系或提及关系。

| 本次处理 | 数量 |
| --- | ---: |
| 来源频道记录 | 265,724 |
| 有效、筛选后唯一链接 | 253,780 |
| 选取的唯一链接 | 105,000 |
| 新增入库 | 104,997 |
| 已存在而跳过 | 3 |
| 导入后资源总数 / 唯一链接数 | 106,159 / 106,159 |
| 可公开搜索的资源 | 106,140 |

原有 1,162 条资源逐条内容摘要与导入前一致；其中 19 条隐藏记录继续保持隐藏。选取结果含 **1,381 条汉字标题**、**9,799 条主题规则命中记录**，二者可能重叠；其余为多语言频道，主要用于测试目录规模。这不是十万条中文行业资源，也不代表频道当前仍可访问。

选取优先级为汉字标题、主题词命中、英语、快照时间已知。主题由标题和用户名规则推断，涵盖开发、AI、服务器、教育、设计、招聘、旅游、新闻、摄影、营销、音乐、体育，不是来源数据集提供的人工分类。只过滤了名称中明显的成人及违法目录，未逐个审核频道实际内容。

本次转换包括规范化公开链接、重复用户名保留较新快照、过长标题截为 180 字并在报告保留原名、添加主题标签；`1970-01-01 00:00:00` 视为缺少快照时间。来源没有成员数，104,997 条新记录的 `members` 全为 `NULL`，不把浏览量或转发量冒充人数。资源简介明确标注历史快照及未核验状态，网站详情展示作者、来源和许可。

原始压缩包位于 `runtime/imports/telegramgraph-20261116/TelegramChannelsGraphDataset.zip`。发布者 MD5 为 `d0f1e673261bb58b95f156ef0524848e`；导入器每次验证该校验和和 CSV 字段结构，失败时不写库。

```sh
# 默认预检
php think resources:collect-dataset runtime/imports/telegramgraph-20261116/TelegramChannelsGraphDataset.zip --limit=105000
# 分批写入，每批 2,500 条，数据库插入每组 500 条
php think resources:collect-dataset runtime/imports/telegramgraph-20261116/TelegramChannelsGraphDataset.zip --limit=105000 --apply
```

正式导入报告：`runtime/import-reports/dataset-20261116-123038-14060d/summary.json`，同目录 42 份批次报告保留原行号、原始元数据、转换规则、入库 ID 或跳过原因。报告先保存来源映射再提交批次，处理中断后可以重跑，已经提交的批次不会重复插入。再次预检报告 `dataset-20261116-123130-def893/summary.json` 确认 105,000 条全部已存在、预计新增为 0。

### 全局链接去重与投稿限制

网站用户提交、Telegram `/add`、后台录入、审核发布、JSON / 数据集导入共用链接身份与事务锁。以下规则适用于 Telegram 类型；新增 `website` 使用独立的普通网址规则，详见 [分类采集说明](docs/catalog-collection.md)：

- `@ExampleBot`、`t.me/ExampleBot/`、`http://telegram.me/ExampleBot?start=abc`、`https://t.me/s/ExampleBot` 统一为 `https://t.me/examplebot`。
- 忽略大小写、查询参数、片段和末尾斜杠；帖子预览地址与普通帖子地址统一，帖子 ID 的前导零移除。不同帖子 ID 仍是不同链接。
- 不接受邀请链接、服务路径、带用户名密码或端口的 URL、错误域名和无效帖子编号。
- `resources.url_hash` 和 `submissions.url_hash` 各有唯一索引；`ts_link_locks` 按同一 URL 的 SHA-256 在事务内串行化跨表写入，防止两个用户或导入与投稿同时占用同一链接。批量锁按哈希排序；死锁/锁等待超时只在完整外层事务进行最多 5 次尝试，仍失败时返回可重试提示。
- 任意用户已提交的链接，在待审核、已通过或已拒绝状态下都不能再次提交；导入同样跳过。通过审核是唯一允许将该待审核申请转换为正式资源的路径，保留申请与资源的关联。
- 导入只新增，不覆盖或重新发布已有记录。链接存在时，用户会看到以下具体结果，而不是数据库唯一索引错误。

| 已有状态 | 提交反馈 |
| --- | --- |
| 正常发布 | 已存在、已正常收录 |
| 下架 / 归档 | 当前已下架 / 已归档，不能重复提交 |
| 封禁 / 失效 | 当前已被封禁 / 已标记失效，附管理员填写的原因 |
| 待审核 | 已存在申请，正在审核 |
| 已拒绝 | 申请已被拒绝，附审核备注 |
| 用户禁止投稿 | 你已被禁止提交收录，附用户投稿限制原因 |

资源状态由后台维护；上述“封禁/失效”不表示已经接入自动检测 Telegram 状态的服务。存在性检查优先于资源名称校验，所以只发 `/add 已有链接` 也能获得明确状态。

后台「资源管理 → 编辑」新增「已封禁」「已失效」和「状态说明」；异常资源不出现在公开搜索。后台「用户管理 → 投稿权限」可单独禁止或恢复投稿并填写原因，不影响用户登录、搜索、收藏；「停用账号」仍是另一个独立操作。新字段 `resources.status_reason`、`users.submission_allowed`、`users.submission_note` 均支持隐藏、只读、可编辑的 RBAC 授权，修改用户投稿权限还需要 `users.update`。

已有安装先停止网站写入和 Bot worker，运行 `php think app:upgrade-catalog`，再启动服务。迁移版本为 `004_catalog_identity`，全新安装由 `app:install` 自动执行。迁移会先检查旧链接规范化后的重复记录；存在冲突时停止并要求先处理，不自动删除或合并历史记录。迁移可以重复执行。

每天可运行 `php think app:maintain` 清理过期会话/限流桶、7天前已完成更新、90天前搜索记录；该命令不会删除资源、用户和审核记录。定时任务尚未自动安装。

## 用户中心、支付与广告

新增用户钱包、USDT-TRC20 自动核账、线下充值审核、每日首搜奖励、六类广告位、广告购买审核及退款。入口为 `/user`，后台分为「财务管理」和「营销管理」；机器人私聊 `/account` 或 `/wallet` 可取得一次性网页登录码。原收藏与提交入口为 `/user/collections`。

已有安装运行 `php think app:upgrade-commerce`。`scripts/dev.php` 已同时启动 `commerce:work`，生产环境需单独守护该命令。收款地址、Tronscan API Key、收款说明和奖励规则在后台「支付与奖励设置」配置。`.commerce.key` 为加密密钥，需与数据库一起安全备份，不能加入 Git。

广告位初始关闭，设定价格并开放后才能购买；群/频道位需配置目标与机器人权限。搜索奖励默认关闭，线下转账在填写收款说明后启用。详细资金约束、支付边界、投放流程及测试方式见 `docs/commerce.md`。

## 验证

```sh
# 需 8088 服务和本地 MySQL 已运行；会创建临时测试数据并在 finally 清理
php tests/integration.php
php tests/admin_rbac.php
php tests/bot_credentials.php
php tests/bot_search.php
# 可选真实连通性验收：重新保存当前 Token（不更换机器人，不发送消息）
php tests/bot_credentials.php --live
php tests/bot_menu.php
php tests/menu_http.php
php tests/welcome_template.php
# 前端模板测试使用 Node 22.18+ / 24 的原生 TypeScript 支持
node tests/welcome_template.mjs
php tests/icon_catalog.php
php tests/resource_import.php
php tests/catalog_identity.php
php tests/catalog_concurrency.php
php tests/catalog_http.php
php tests/commerce.php
php tests/commerce_concurrency.php
php tests/commerce_http.php
php tests/website_catalog.php
php tests/telegram_preview.php
sh scripts/build.sh
```

集成测试覆盖登录、CSRF、权限隔离、公开内容过滤、链接去重、收藏归属、审核单次执行、API 开关/限额/撤销，以及模拟 Telegram transport 的分页、版本、归属、过期、去重和429重试。菜单测试额外覆盖动作校验、图标、旧菜单拒绝、接口权限与当前用户标签替换；PHP / TypeScript 模板测试共用渲染样例，覆盖时区、缺失值、格式校验和展开长度。服务测试回滚数据，HTTP 测试清理临时管理员。浏览器测试结果见 `docs/validation.md`。
