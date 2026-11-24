# 分类采集与网站资源支持

目标为机器人、视频、图文、音乐、文件、店铺、其他、网站各 50,000 条。前七类仅使用 Telegram 链接，网站类使用普通公开 HTTP/HTTPS 地址。频道与群组的原有 106,159 条记录不计入本轮目标。

## 数据来源与处理

### 网站

来源：[Curlie 官方下载说明](https://curlie.org/docs/en/rdf.html)，[CC BY 3.0](https://creativecommons.org/licenses/by/3.0/)。原始归档下载入口为 `https://curlie.org/directory-dl`，本次跳转到发布者合作托管地址。

只读取 `World`、`Business`、`Top` 的网站及分类 TSV。优先简体/繁体中文，再补充英文行业目录；按规范化网址去重。标题、简介清理 HTML 和超长字段，分类路径保留为标签。不会按网站所属行业把普通网址改成 Telegram 视频、店铺等类型。

本次预检扫描 496,888 行，排除无效网址 37 行、重复 1,456 行、明显不适合通用演示的内容 18 行；选取 50,000 条，其中中文目录 18,747 条。扫描数包含未被选取的其他语言记录。已实际新增 50,000 条。网站内容来自目录快照，尚未逐条检测网站存活状态。

正式报告：`runtime/import-reports/catalog-curlie-20261116-160453-747607/summary.json`。复跑报告：`runtime/import-reports/catalog-curlie-20261116-161051-9ddab5/summary.json`，50,000 条全部为已有链接，预计新增为零。

### 机器人

- [TGrpg/awesome-telegram](https://github.com/TGrpg/awesome-telegram)，CC BY 4.0，只读取明确标注 `kind=bot` 的条目。
- [danyspin97/TelegramBotsList](https://github.com/danyspin97/TelegramBotsList)，Unlicense，只取实际列出的 Telegram 链接，不从 GitHub 项目名猜测用户名。
- [erkcet/awesome-telegram-bots](https://github.com/erkcet/awesome-telegram-bots)，CC0。

每个候选读取匿名公开页面，只有观察到 `Start Bot` 操作才接纳为机器人。保留检查时间；没有向第三方机器人发送消息。公开页面可识别为机器人不代表其所有功能可用。Bot 月活数不当作群成员数。

正式核验：758 个候选，40 个重复、2 个排除、28 个非机器人或访问失败，实际新增 **688** 个机器人。其中 TGrpg 来源 596 条、danyspin97 来源 88 条、erkcet 来源 4 条。首次预检为 627 个通过；部分临时网络失败在正式批次重试后恢复，因此最终数量以正式报告为准。核验缓存有效期为一天。

正式报告：`runtime/import-reports/catalog-bots-20261116-162451-49f09c/summary.json`。

### 视频、图文、音乐、文件、其他

先从上述目录中的软件、开发、学习、设计等频道发现入口，再从已有频道元数据发现音乐入口。入口分类只用于发现频道；真正入库的每一条都是页面实际出现的帖子链接。

- 视频：视频播放器元素。
- 音乐：音频元素；语音不会当作音乐。
- 文件：文件附件元素，保留可见文件名。
- 图文：照片或文字帖子。
- 其他：语音、贴纸、投票等已识别元素。
- 无法判断类型、没有公开帖子的页面、其他频道的嵌入链接不入库。

不猜测消息编号；通过页面实际提供的 `before` 游标翻页。标题/摘要只保留短片段，不下载视频、音乐、附件正文。公开预览缓存和每批原始链接、原帖子 ID、时间、类型依据一起保留。

本轮每频道读取最多 5 页，属于有界采集批次，并非抓完全部历史。命令支持提高页数继续采集；复跑跳过已有链接。遇到 429 停止本批网络请求，已提交批次保留。

实际处理 167 个频道入口、704 个公开预览页：129 个入口达到本次页数上限，20 个到达历史末尾，3 个没有公开帖子，15 个不可访问。正式新增 11,897 条帖子；后续复核将其中 **491 条**类型信息不足的记录暂时隐藏，剩余 **11,406 条**公开可搜索。

这 491 条记录含文字 caption，但页面同时标记媒体无法预览。已修正解析器：有文字不代表原帖可以归为图文；无法识别实际媒体时跳过。审计仅检查本轮报告的入库 ID，并核对来源、规范化标题、URL、类型和状态；原数据保留，可人工核验后恢复。审计先隐藏 436 条，再按入库时的标题 trim 规则补齐 55 条；共 491 条。没有删除帖子、收藏或审核历史。

音乐入口未返回可明确识别的音频元素，不能凭频道名称或文字说明归类。机器人数量受本轮公开目录候选集限制；视频、图文、文件和其他类型受公开预览范围及每频道 5 页的批次上限限制。后续增加合适的公开来源或提高采集页数可以继续补充，不能保证每类都能达到 50,000 条。

正式帖子报告：`runtime/import-reports/public-20261116-162451-a17491/summary.json`；质量审计：同目录 `preview-audit-apply.json`。原始正式报告保留审计前数量，最终公开数量以末尾核对表为准。

店铺需要能确认其店铺身份的独立数据来源；没有把优惠频道、商城网站或普通机器人自动改成店铺来补数量。

### 未采用的数据

- [Zenodo 17281308](https://zenodo.org/records/17281308)：机器人研究数据集 API 标为 restricted，没有可直接下载的数据文件；未申请访问或绕过限制。
- [TeraGram 23592983](https://zenodo.org/records/23592983)：公开消息预览有媒体类型，但 `messages.id` 是内部自增 ID；原发布者 `data_models/message.py` 明确使用内部主键。缺少真实 Telegram 消息编号映射，不能拼接成帖子地址，未导入。
- [Pushshift 3607497](https://zenodo.org/records/3607497)：研究了 2020 年发布的旧数据快照、频道文件和部分消息样本；没有将研究样本或未核验的历史消息批量发布到资源库。
- 原有 Zenodo 14749767 的频道 CSV 没有帖子媒体字段，未重新分类为视频/音乐等类型。

## 可重跑命令

```sh
# 网站：默认预检
php think resources:collect-catalog curlie runtime/imports/catalog-20261116/curlie-rdf --limit=50000
# 网站：正式写入
php think resources:collect-catalog curlie runtime/imports/catalog-20261116/curlie-rdf --limit=50000 --apply
# 机器人：公开页面核验 + 预检；追加 --apply 写入
php think resources:collect-catalog bots runtime/imports/catalog-20261116 --limit=50000
# 帖子：每频道最多 5 页，每种媒体目标 50000；追加 --apply 写入
php think resources:collect-public runtime/imports/catalog-20261116 --pages=5 --target=50000
# 离线核对保存的正式批次：不重爬、不新增资源
php scripts/verify-catalog-batches.php \
  runtime/import-reports/catalog-20261116-verification.json \
  runtime/import-reports/catalog-curlie-20261116-160453-747607 \
  runtime/import-reports/catalog-bots-20261116-162451-49f09c \
  runtime/import-reports/public-20261116-162451-a17491
```

所有写入继续调用 `ResourceImport`，共用资源、投稿唯一索引和跨表事务锁。只新增，不覆盖现有资源、封禁状态、投稿审核结果。每批提交前保存来源映射，提交后保存资源 ID 与跳过原因。`complete` 表示该次命令执行完毕，数量是否达标看 `target_reached` / `targets_reached_by_type` 和实际数据库统计。

原始资料和批次报告在 `.gitignore` 覆盖的 `runtime/` 下；源码与说明中没有凭据。来源目录内保留原始归档、许可证、文件 SHA-256（采集报告）、核验缓存和公开预览 HTML。

## 网站类型与去重规则

前后台、公开 API、收藏页及 Telegram 分类按钮支持 `website`。网站按钮使用已有 Telegram iOS Icons 的全球线框图标 `5776233299424843260`，没有另换图标包。界面沿用现有 `ui-ux-pro-max` 规范，表单提示随类型变化，网站结果显示「访问网站」。

普通网址仅支持 HTTP/HTTPS；域名及协议小写、默认端口移除、根路径补 `/`、片段移除。路径、查询顺序、HTTP/HTTPS、www 与非 www 保守地保持区别，避免误合并不同网页。拒绝本地地址、私有 IP、内嵌账号密码、危险协议、控制字符、畸形 UTF-8 和超过 500 字的地址。Telegram 类型继续沿用原有规范化；Telegram 链接不能通过网站分类规避规则。

公开搜索 API 的每条数据增加可空字段 `source_attribution`，包含来源、作者、许可和转换说明。网页搜索及收藏页展示适用来源署名，机器人列表也保留目录来源与许可信息。

## 本轮验收

已验证网站关键词搜索、前后翻页、后台表单切换，375px 手机宽度没有水平溢出。实际搜索「物流」得到 164 条网站结果；12 条/页。截图：

- `docs/screenshots/catalog-websites-industry.jpg`
- `docs/screenshots/catalog-websites-mobile.jpg`

手机截图用于展示 375px 布局，拍摄于网站导入后；最终数量以以下数据库核对和已更新的桌面截图为准。

## 最终数量与缺口

2026-11-16 16:47（Asia/Shanghai）实查 MySQL：

| 类型 | 本轮已入库 | 其中公开可搜索 | 距每类 50,000 条的公开数量缺口 |
| --- | ---: | ---: | ---: |
| 机器人 | 688 | 688 | 49,312 |
| 视频 | 1,858 | 1,858 | 48,142 |
| 图文 | 9,310 | 8,819 | 41,181 |
| 音乐 | 0 | 0 | 50,000 |
| 文件 | 671 | 671 | 49,329 |
| 店铺 | 0 | 0 | 50,000 |
| 其他 | 58 | 58 | 49,942 |
| 网站 | 50,000 | 50,000 | 0 |
| 合计 | **62,585** | **62,094** | **337,906** |

只有网站达到本轮数量目标；其余类别未达到。前七类没有用普通网站或重分类的旧频道补数。采集进程已结束，当前没有后台持续补采任务。

全库记录 **168,744**，唯一 URL 和唯一 `url_hash` 均为 **168,744**，hash 不匹配为 0。公开可搜索 **168,234**，隐藏 **510**（旧有 19 + 本轮待核验 491）。原有频道 105,000 条、群组 1,159 条保持原类型和状态；它们不计入本轮新增。

65 个正式批次的全部 62,585 条离线复跑预检均识别为已有资源，预计新增 **0**；隐藏记录也占用原有链接身份。Telegram 类型中的非 Telegram 链接、网站类型中的 Telegram 链接、非 HTTP/HTTPS 网站链接均为 **0**。本轮 fixture 用户、资源残留均为 **0**。

最终核对报告：`runtime/import-reports/catalog-20261116-verification.json`。

网站、API 与 Bot 共用搜索和链接去重规则。`website_catalog.php` 34 项、链接身份 52 项、并发去重 10 项、HTTP 26 项、导入 9 项、Bot 搜索 57 项、群聊 72 项通过；公开预览解析检查通过。100 个 PHP 文件语法检查、Vue 类型检查及 Vite 构建通过。已重启开发服务，网站 HTTP 200，Bot 状态 `running`、心跳正常且无当前错误。此处 Bot 交互行为来自模拟 transport 回归与运行状态检查，没有将它写成新增数据的真实聊天发送验收。
