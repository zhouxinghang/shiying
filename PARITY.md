# 与参考服务的行为对照

此文区分可验证的核心规则与单体部署适配。Go 服务不调用 Python 服务；对照基准由仓库当前实际 Python 实现生成，而非手写期望值。

| 规则 | 参考实现 | 单体实现与验证 |
| --- | --- | --- |
| 链接提取、短链逐跳检查、modal_id / vid 优先、note / slides | `src/dtk/urls/{parse,patterns,expand}.py` | `parser.go`、`network.go`；端口、域名边界、循环跳转、作品 ID 时间范围测试 |
| 详情请求与原生签名 | `platforms/douyin/params.py`、`signing/native/` | `signing.go`；参数值及插入顺序对照、SM3/字段基准、访客签名摘要与 Cookie 一致性测试 |
| 视频 / 图集识别、封面顺序、URL 去重、私密 / 删除 / 风控 | `platforms/douyin/parser.py`、`platforms/common.py` | `parser.go`；7 组原服务样本逐字段对照，私密 / 删除仍保留归一化元数据，下载入口给出不可用提示 |
| 作者资料与作品分页 | `platforms/douyin/{params,parser}.py` | `authors.go`：sec_uid、作者资料、每页 count=20、max_cursor 及全部策略参数顺序；真实 Python 生成作者资料 / 列表 / 下一页请求基准，Go 对照验证 |
| 本人喜欢的作品 | `params.py:author_likes_params` 与同一作品解析器 | `account.go`：GET favorite，sec_user_id 固定为 Cookie 本人、max_cursor 分页；Python 生成首 / 次页有序参数基准，含签名身份、视频 / 图集与批量去重测试 |
| 本人全部收藏作品 | 仓库历史 `8c98fb7:crawlers/douyin/web/{models.py,web_crawler.py}` 的 UserCollection / fetch_user_collection_videos | POST listcollection，空正文、cursor/count 查询与 cursor 响应；使用当前原生签名、解析和清单规则。首 / 次页方法、游标及无用户 ID 的语义通过测试与真实请求验证 |
| 主流、码率、下载地址顺序 | `media/plan.py` | `media.go`；保留上游顺序；带水印下载地址放最后，不按码率重排 |
| 图集图片与封面 | `media/plan.py` | 按原图片序号命名，不因拒绝某个域名而重新编号；封面最后、仅一个；每任务 64 项，图片 32 MiB |
| 文件上限与链接刷新 | `core/config.py`、`worker/ops/media_download.py` | 默认 512 MiB，镜像超过 600 秒先刷新；刷新失败仍尝试已有清单 |
| 下载 | `docker/downloader/download.go` | `queue.go`；相同 UA / Referer / identity 编码，媒体不携带 Cookie；顺序镜像回退、MIME 前缀、实际字节上限、非空检查、SHA-256、sync 和原子重命名 |
| 文件并发与结果 | `docker/downloader/queue.go` | 每任务 4 文件并发；全成功、全失败、部分成功分别记录，成功文件可独立导出；失败重试会保留先前文件并明确标记 |
| 元数据和导出名 | `media/plan.py` | `meta.json` 保留原服务字段与无签名 URL 规则；`dtk-douyin-作品ID-文件名` |

## 单体适配和兼容扩展

- 原服务有身份池、按身份生成的浏览器参数 / TLS 模拟、代理、重试调度及签名 RPC；个人版是单份 Cookie + 固定一致的 Chrome 146 Windows 参数 + Go 标准 HTTP。没有等价的浏览器 TLS 指纹，因此不能把离线规则一致解释成线上风控成功率完全一致。
- 有 Cookie 时优先原网页详情接口；保留移动分享 HTML、RENDER_DATA / NEXT_DATA 作为额外兼容路径。移动页可能缺少 sec_uid 等完整元数据，只在该兼容路径允许使用缩略字段。
- 媒体白名单在原列表上保留旧分享页所需的 `aweme.snssdk.com` 指定 HTTPS 播放接口；不开放其他路径或该域名的任意子域名。非默认端口限制比参考下载器更严格。
- 全局并发为 2 个作品，单作品文件并发仍为 4。存储使用内部任务 ID 目录，以兼容旧版 SQLite 及已下载 MP4；不改变用户已有文件路径。
- 作者目录及预览保存在 SQLite，供多页选择与批量提交使用；页面按作品 ID 去重，后端校验游标是否前进，前端检测分页循环。批量提交复用既有单作品队列，每请求最多 100 项，返回逐项结果并防重复。
- 本人身份通过 `/user/profile/self/` 核对，并要求登录会话 Cookie。当前参考服务的会话探测测试指出 `query/user` 会向游客返回 ID，不能用它确认个人列表的归属；本人资料接口是个人版补充。只提取白名单资料，不保存原始本人响应。
- 当前参考服务的 `author_collections_params` 对应 `collects/list` 收藏夹目录；个人版复用其 cursor/count 有序参数形状，但通过历史原服务的 `listcollection` 读取全部收藏作品，包含未分组作品。两者不是同一接口，不能互相代替。
- 喜欢和收藏按随机 Cookie 版本单独缓存，所有上游请求固定使用一次读取的 Cookie；更换 Cookie 会清除私有目录，阻止旧请求、旧预览和旧选择继续提交。公开作者目录与已经保存的任务保持不变。
- 任务接口沿用个人版简化格式，解析响应同时提供 `content` 与兼容的 `video` 字段。成功文件可逐个导出；ZIP 是个人版附加功能，包含成功媒体、原格式 meta.json 和无签名 URL 的下载报告。
- 文件发布使用带 attempt 的暂存目录，终态后开放本地文件。取消、删除、重试与旧 worker 隔离。断点续传未实现，恢复会重下。

## 重现对照

需要 Python 3.12+ 和 pydantic 2，仅用于开发时重新生成基准，运行 Go 二进制不需要它们。

```bash
python personal/scripts/reference_parity.py
cd personal
go test -race ./...
go vet ./...
cd ui && npm run build
```

基准为 `internal/app/testdata/reference_parity.json`。脚本直接导入仓库解析器及清单规划器，替换合成样本域名为允许的合成媒体域名，不读取真实 Cookie。Go 测试还检查参考源码 SHA-256：上游规则变化后需重新生成并审查差异。

2026-10-18 真实验收：用户提供的图集成功解析 9 张图片，9 张图片及封面 10/10 文件保存成功，合计 5,011,001 字节。临时测试未将 Cookie 或带签名媒体地址写入测试基准。

2026-10-18 作者功能真实验收：使用用户先前提供的图集定位作者，成功取得作者资料、第一页 22 个作品及第二页 20 个作品，均保留平台分页标记。浏览器临时环境验证跨页多选、全选和 3 个视频 / 图集批量入队后全部完成。

正式服务浏览器追加验收：通过“查看作者全部作品”进入真实作者页，逐页加载至 `has_more=false`，按作品 ID 去重后得到 312 个作品，与主页标记 312 一致；已保存的图集被识别并从可选数量中排除。未自动提交这些真实作品的批量下载。

2026-10-18 本人列表真实验收：当前已配置 Cookie 通过本人资料接口识别；喜欢前两页分别取得 20 / 19 个作品，全部收藏前两页分别取得 19 / 18 个作品。确认喜欢使用 GET + max_cursor，收藏使用 POST + cursor，且列表回显账号与本人一致。测试未批量下载真实个人列表；账号信息、私有作品内容、Cookie 和签名地址均未写入测试基准。

本人列表浏览器验收：合成账号的喜欢 / 收藏均加载到第二页末页，跨页选择保留；从收藏页批量提交的 2 个视频和 1 个图集全部完成，喜欢页同步识别为已保存。切换合成 Cookie 后私有列表重新加载，已下载任务保留；375px 手机布局通过检查。正式服务更新后本人身份、喜欢 20 项与收藏 19 项的首分页再次通过核对，原有 14 条已完成下载记录保留。
