# 拾影 · 个人抖音作品收藏室

参考本仓库实现的轻量个人版本。React + TypeScript + shadcn/ui（Radix）+ Tailwind 前端，Go 标准库 HTTP 服务，SQLite 持久化。前端构建后通过 `go:embed` 打包到 Go 可执行文件中；运行时只需一个进程，无需 Python、Node、Redis、PostgreSQL 或浏览器服务。

## 本地启动

构建需要 **Go 1.24+、Node.js 22.12+、npm**。

```bash
cd personal
make build
./bin/shiying
```

打开 <http://127.0.0.1:8080>。默认只监听本机，SQLite 和媒体文件放在当前工作目录的 `data/` 下。启动日志会显示地址；按 Ctrl+C 停止。

```bash
# 自定义存储目录和端口
./bin/shiying -addr 127.0.0.1:8090 -data /你的磁盘/拾影

# 也可以使用环境变量
SHIYING_DATA_DIR=/你的磁盘/拾影 ./bin/shiying
```

发布时只需要分发 `bin/shiying`，不需要携带 `ui/`、前端依赖或源代码。构建产物已忽略，不纳入 Git。

## 使用

1. 复制抖音视频或图集的链接，完整分享文案也可以。
2. 在工作台解析，确认作者、作品信息；图集支持翻页、缩略图和键盘左右键。
3. 点击保存，后台下载视频 / 全组图片、封面和 `meta.json`。
4. 作品库默认按作者展示名称、头像、签名、粉丝数与本地作品数量，点击作者进入其已保存作品。作品仍使用 3:4 竖版卡片，支持搜索、排序、播放、查看图集与本地收藏；单独导出原文件，或将整个作品打包 ZIP。
5. 在作品预览或详情中点击“查看作者全部作品”，也可以进入“作者作品”粘贴主页或作品分享链接。支持加载更多 / 全部、视频与图集筛选、关键词搜索、单选及全选当前结果，跨页保留选择。点击“下载所选”加入现有后台队列；已有任务会标记并跳过。
6. 在“抖音账号”读取已配置 Cookie 的本人账号，切换“我的喜欢”或“我的收藏”。两类列表均支持加载全部、搜索筛选、跨页选择及批量下载；与侧栏“本地收藏”区分。
7. 任务支持文件进度、取消、重试和删除。部分失败时保留已保存文件，重试失败也保留上次成功文件并标记来源，ZIP 标记为 `partial` 并附带下载报告。

支持 `v.douyin.com`、`v.amemv.com`、`douyin.com/video/...`、`douyin.com/note/...`、`douyin.com/slides/...`、`?modal_id=...` / `?vid=...`、`iesdouyin.com/share/video/...`，以及省略协议的常见分享链接。作者作品页支持主页分页和批量选择下载；暂不支持直播和 TikTok。

### 作品库展示

- 侧栏“随机看图”以整页展示本地已保存图集的第一张图片，按随机顺序逐部浏览。空格下一张，B 上一张，M 收藏当前作品并下一张，Enter 打开作品详情，Esc 返回作品库；也可点击页面按钮。每轮遍历全部图集，回看保留浏览顺序；已收藏作品再次按 M 会保留收藏，收藏失败时停留当前作品。详情打开期间暂停随机浏览快捷键，关闭后继续原位置。
- 点击作品打开接近全屏的详情浏览器，视频和图片按原比例完整显示；信息栏独立滚动，可收起，右上角支持“最大化预览”。图集缩略图默认收起，可随时展开，支持左右方向键、上一张 / 下一张和导出当前图片。手机端占满窗口，作品信息以抽屉形式打开；Escape 先关闭信息抽屉，再关闭详情。
- 作品库与本地收藏使用 3:4 竖版封面，桌面多列、手机双列；保留列表视图以及图集张数 / 视频时长、收藏和详情操作。
- “按作者分组”先展示作者资料卡，点击进入该作者的本地作品页。作者列表可搜索名称、已缓存签名或作品标题；作者内可单独搜索作品，时间排序作用于作者及组内作品。“全部作品”保留直接浏览所有作品的视图。
- 作者作品页有独立地址，刷新后保留所选作者，支持浏览器返回和“返回作者列表”；当前网页中返回列表时保留作者搜索与滚动位置。
- 优先按作者 `author_uid` 归组，同名不同账号不会混合；同一账号更换昵称仍属于同一组。旧记录没有作者 ID 时按作者名归组，无作者信息的记录显示为“未知作者”。
- 先读取本地作者资料缓存，再按可见卡片逐个补齐资料；成功资料持久化，24 小时内复用，作者页可手动更新。资料更新失败时保留缓存与本地作品。平台限制或缺少 Cookie 时暂停自动补齐，避免继续重复请求；旧记录缺少作者标识时明确标注资料不可用。
- 作者头像通过服务代理，接口不暴露签名头像地址或 Cookie。粉丝数未知时显示“未获取”，不以 0 冒充；资料来自抖音，作品数量来自本地下载记录。浏览作品不重新下载或移动媒体文件。

### 解析方式与限制

配置 Cookie 后优先请求与原服务相同的网页作品详情 API，并用 Go 移植的 `a_bogus`、SM3、`x-secsdk-web-signature` 签名。基础参数和顺序、Cookie 中的 `msToken` / `uifid` / `s_v_web_id`、缺失 token 的本地占位规则与原服务对应。当前使用内部一致的 Chrome 146 / Windows / 1920×1080 配置。

解析与下载的规则对照见 [PARITY.md](PARITY.md)。原服务的 Python 解析器和清单规划器直接生成测试基准，Go 测试逐字段比较媒体清单、图集顺序、视频镜像和计划文件；参考源码改变时哈希检查会提示重新核对。视频先尝试 `play_addr`，接着按上游顺序尝试各码率和 `download_addr`；最后一组可能带水印，这是原服务的最后回退规则，不保证任何作品均无水印。

另外保留移动分享页 `_ROUTER_DATA` 和网页 `RENDER_DATA` / `__NEXT_DATA__` 作为兼容回退。兼容 `aweme.snssdk.com` 旧入口时，只允许 HTTPS 指定播放路径，将 `playwm` 归一化为 `play` 后逐跳验证 CDN。

单体版本使用 Go 标准 HTTP 传输、单份 Cookie，未移植原服务的身份池、浏览器 TLS 模拟、代理调度和浏览器签名 RPC。因此核心数据与文件策略可离线对照，平台风控下的端到端成功率不能承诺与原服务完全一致。不运行 Python 或浏览器服务。

**抖音验证和页面变化会影响成功率。** 未取得视频数据、作品私密或被删除时会报错；不会伪造成功。如果公开分享页受限，可在偏好设置中填写浏览器里的完整抖音 Cookie。Cookie 不是验证绕过方案，不能保证所有链接都能解析。已在配置 Cookie 后对真实公开视频完成分享页解析、旧播放入口跳转和 MP4 下载验证；自动回归测试使用脱敏数据与自生成视频，实际可用性仍受网络、作品和抖音风控影响。

Cookie 存在本机 SQLite 中，设置接口返回配置状态与随机版本标识，不回显原文；不会转发到视频 CDN。本人资料只提取昵称、头像、公开资料和作品计数，不保存本人资料接口的完整响应。数据目录包含登录凭据和私有列表缓存，请妥善保管。仅下载你有权保存的内容。

抖音请求失败时，服务会向标准错误输出 `WARN` 日志，记录失败阶段（`stage`）、请求方法与接口、作品 / 作者 ID 或分页参数、耗时、Cookie 是否已配置、HTTP 状态、响应类型、平台状态码与错误提示，以及上游返回的 `X-Tt-Logid` / `X-Request-Id` 和 `Retry-After`（如有）。HTTP 错误响应最多读取 4 KiB 用于诊断；只提取 JSON 状态与提示或 HTML 标题，提示最多保留 512 个字符，不输出完整响应、Cookie、签名和 URL 查询凭据。回退请求中的失败也会记录，成功请求不打印失败日志。Docker 部署可通过 `docker compose logs -f shiying` 查看。

### Cookie 本人的喜欢与收藏

- 需要登录后的完整 Cookie，包含 `sessionid` / `sessionid_ss` / `sid_tt` 等登录会话。服务通过 `/user/profile/self/` 确认本人身份；不会将游客 `query/user` 返回的 ID 当成登录账号，也不接受填写他人 ID 来读取个人列表。
- “我的喜欢”使用当前本人 `sec_uid` 请求喜欢列表；“我的收藏”读取该 Cookie 账号收藏的全部作品，包含未归入收藏夹的作品，不以收藏夹目录代替作品列表。视频与图集使用同一套解析与下载规则。
- 首次各读取一页，可“加载更多”或“加载全部作品”，分页可停止。加载列表只缓存作品信息，勾选后点击“下载所选”才会保存媒体。全选作用于全部已加载且匹配当前筛选的可下载作品；跨页保留选择。
- 为减少大列表的页面开销，先显示 80 个已加载结果，可继续显示更多；全选仍包含全部已加载结果。批量下载自动按 100 项分批，并与作品库、作者作品页的任务去重。
- 账号资料显示的平台计数不保证等于可取得数量；私密、删除、失效、权限限制和平台风控可能使部分作品不可读。收藏接口提示失效作品时会显示通知；只在平台返回末页后标记加载结束。
- Cookie 更换或清除后，旧账号的列表缓存、预览和页面选择失效；读取中的旧响应不能写回新账号。相同 Cookie 再次保存不会清空选择。已经加入队列或下载到本地的作品保留。
- “重新读取账号”会重建页面列表与选择；网页刷新后需重新加载列表。不同账号不会共享个人列表缓存；公开作者目录和已保存的本地作品不受影响。

### 作者作品与批量选择

- 与原服务 `author_profile_params` / `author_posts_params` 及解析器对照，使用作者 `sec_uid`，不以可变的数字 uid 代替。作者接口需要已配置 Cookie。
- 首次加载一页，平台可能返回额外置顶作品。继续分页时按作品 ID 去重，不按主页显示总数提前结束；只在 `has_more=false` 时显示到末页。
- “加载全部作品”逐页请求，页间间隔 500ms，可停止并保留已加载内容；错误或重复游标会停止，不把已有结果清空。平台风控仍可能限制分页。
- “全选当前结果”只选择已经加载、匹配筛选、未在队列且可下载的作品。跨页保留选择；如需选择所有可见作品，先加载全部，再清除筛选并全选。
- 批量提交按每批最多 100 项顺序发送，选择超过 100 项也可提交。可停止后续批次，当前批次会等待结果；已提交任务在后台继续下载。结果分别显示新增、已有、失败和未提交数量，失败项保留选中。
- 私密 / 删除 / 无媒体作品会禁用选择；已有失败、取消或部分完成任务请进入下载任务重试。平台返回的可见作品不一定等于主页标记总数。

### 下载行为

- 两个并发作品任务，每个任务最多并发下载 4 个文件。与原服务默认值一致：单文件 512 MiB、单图片 / 封面最多 32 MiB，每个作品最多 64 个媒体文件，封面最后加入。
- 按原服务顺序尝试全部镜像；检查 Content-Type 和实际写入字节，计算 SHA-256，`fsync` 后原子保存。支持原服务的视频 / 图片格式，不以 MP4 文件头拒绝 WebM 等有效视频。
- 文件名固定为 `video.ext`、`image-01.ext`、`cover.ext`，导出使用 `dtk-douyin-作品ID-文件名`。`meta.json` 与下载报告不含 Cookie 或签名媒体地址。
- 链接超过 10 分钟或手动重试时先刷新；刷新失败时仍可尝试已有镜像，与原服务相同。任务最长 15 分钟。
- 持久化每个文件的状态、大小、类型与哈希；全部成功为完成，部分成功为部分完成，全失败为失败。取消和重试有 attempt 隔离，旧 worker 不会覆盖新任务。
- SQLite 自动增量迁移，兼容旧 Cookie、收藏和 `videos/任务ID.mp4`。新作品位于 `videos/任务ID/`。重启恢复未完成任务，从头下载，**不支持断点续传**。
- 同一作品不重复建记录；删除任务会移除对应的图片、视频、封面、元数据和记录。请勿同时运行多个实例访问同一数据目录。

## 局域网与访问口令

非本机监听必须设置 `SHIYING_ACCESS_TOKEN`；网页显示访问口令登录页，使用 HttpOnly / SameSite Cookie 建立会话。CLI 也可使用 `Authorization: Bearer <口令>`。更换口令会使旧会话失效。

```bash
export SHIYING_ACCESS_TOKEN='请替换成你自己的长随机口令'
./bin/shiying -addr 0.0.0.0:8080
```

默认配置适用于可信个人环境。经公网访问时使用 HTTPS 反向代理；本服务不提供多用户、配额、账户管理或公网防滥用能力。

## Docker 单服务部署

在 `personal/` 下：

```bash
export SHIYING_ACCESS_TOKEN='请替换成你自己的长随机口令'
docker compose up --build -d
```

打开 <http://127.0.0.1:8081>，输入刚才设置的口令。项目下的 `./data` 目录保存 SQLite 和视频。Compose 将主机的 `8081` 端口映射到容器的 `8080`，局域网设备可通过主机 IP 访问。

构建使用 BuildKit。Go 依赖默认通过 `https://goproxy.cn,direct` 下载，保留默认的校验和数据库验证；模块下载和编译缓存会在同一构建器上复用。

如果构建停在 `RUN go mod download`，先中止当前构建，再显示完整下载日志重新构建：

```bash
docker compose --progress plain build shiying
docker compose up -d
```

Dockerfile 中的 `go mod download -x` 会显示正在访问的地址和请求结果。`modernc.org/sqlite` 及其依赖较大，首次下载可能需要等待；如果出现超时、DNS 或 TLS 错误，应检查构建器的网络和代理。

可通过环境变量更换 Compose 使用的模块代理，例如网络能够访问 Go 官方代理时：

```bash
GOPROXY=https://proxy.golang.org,direct docker compose --progress plain build shiying
```

直接使用 Docker 构建时，通过构建参数覆盖：

```bash
docker build --progress=plain --build-arg GOPROXY=https://proxy.golang.org,direct -t shiying .
```

只在主机执行 `go env -w GOPROXY=...` 不会修改镜像构建环境。`GOPROXY` 中逗号后的 `direct` 只在代理返回 404 / 410 时回退，不会解决代理本身的连接超时。不要通过清空缓存或关闭校验和验证来处理网络问题。

备份前停止服务，再备份整个数据目录或 Docker 卷，避免漏掉 SQLite WAL 文件。

Docker 配置已提供，本机未安装 Docker，因此未实测镜像构建；本地二进制构建和运行已验证。

## 开发与验证

```bash
# 终端一：Go API
go run ./cmd/shiying

# 终端二：React 热更新（/api 代理到 8080）
cd ui
npm ci
npm run dev
```

开发代理使用浏览器请求的 Host，因此无需开启 CORS。生产前重新执行 `make build`。直接 `go run` 而没有先构建前端时，根页面会显示构建提示，API 仍可使用。

```bash
go test -race ./...
go vet ./...
cd ui && npm test && npm run build
```

测试覆盖原服务解析 / 清单样本对照、签名与参数顺序、作者资料与分页对照、Cookie 本人身份与喜欢 / 收藏分页、个人列表批量下载、换 Cookie 时的缓存隔离与请求竞态、短主页链接展开、游标循环保护、批量去重及逐项失败结果、作者目录重启持久化、图集完整下载和部分失败、镜像回退、大小 / 类型 / 哈希校验、ZIP / 逐张导出、本地预览、Range 播放、旧数据库 / MP4 迁移、取消重试与恢复、访问口令与 Cookie 防泄露。

需要可重复的浏览器验收时，先构建前端，再运行 `SHIYING_UI_QA=1 go test ./internal/app -run '^TestBrowserQA$' -v -timeout 0`。打开 <http://127.0.0.1:8081> 并解析 `https://www.douyin.com/video/7000000000000000001`。图集测试链接为 `https://www.douyin.com/note/7000000000000000002`。“抖音账号”提供合成登录账号及两页喜欢 / 收藏列表。此环境使用独立临时数据库与本地自生成素材，HTTP 请求使用测试替身，不会访问抖音。Ctrl+C 停止，测试服务不会编入发布二进制。

作品库展示验收可同时设置 `SHIYING_UI_QA_LIBRARY=1`，预置 7 个已完成作品、5 个作者身份（含同名账号、旧记录和视频 / 图集），用于验证竖图、作者分组、搜索排序及手机布局。

## 目录与 API

```text
cmd/shiying/           启动参数、信号处理、HTTP 服务
internal/app/         解析、HTTP 安全边界、SQLite、队列、API、测试
internal/web/         嵌入构建后的前端资源
ui/                   React + shadcn/ui 源码
data/shiying.db        SQLite（运行时生成）
data/videos/任务ID/   视频 / 图片、封面和 meta.json（运行时生成）
```

| 接口 | 用途 |
| --- | --- |
| `GET /api/health` | 存活检查 |
| `POST /api/session` | 口令登录，`{ "token": "..." }` |
| `POST /api/parse` | 解析，`{ "text": "分享文案或链接" }`，返回 `preview_id` |
| `GET /api/tasks` | 任务、收藏与统计 |
| `POST /api/tasks` | 下载，`{ "preview_id": "..." }` |
| `POST /api/authors/resolve` | `{ "text": "主页或作品分享链接" }`，返回作者资料；需要 Cookie |
| `GET /api/authors/{uid}/posts?cursor=...` | 作者公开作品，按平台 max_cursor 分页；返回预览标识、可下载状态、已有任务 |
| `GET /api/authors/{uid}/avatar` | 代理已解析作者的头像 |
| `GET /api/library/authors` | 已保存作品作者的本地资料缓存，不请求抖音 |
| `GET /api/authors/{uid}/profile` | 读取作者资料，24 小时内复用缓存；`?refresh=1` 强制更新，失败时可回退缓存 |
| `GET /api/account` | 核对当前 Cookie 本人，返回白名单资料、随机账号版本 `scope` 及平台计数 |
| `GET /api/account/likes?scope=...&cursor=...` | 当前账号的喜欢作品，`max_cursor` 分页 |
| `GET /api/account/bookmarks?scope=...&cursor=...` | 当前账号的全部收藏作品，`cursor` 分页；上游为只读 POST |
| `GET /api/account/avatar?scope=...` | 当前版本账号的头像代理 |
| `POST /api/tasks/batch` | `{ "preview_ids": ["..."] }`，每请求 1–100 项，逐项返回 added / existing / failed |
| `POST /api/tasks/{id}/cancel` | 取消 |
| `POST /api/tasks/{id}/retry` | 刷新地址并重试 |
| `PATCH /api/tasks/{id}/favorite` | `{ "favorite": true }` |
| `DELETE /api/tasks/{id}` | 删除任务和本地文件 |
| `GET /api/tasks/{id}/file` | 视频播放，支持 Range；加 `?download=1` 导出；图集转 ZIP |
| `GET /api/tasks/{id}/images/{index}` | 从 1 开始的图片索引；`?download=1` 单张导出 |
| `GET /api/tasks/{id}/files/{name}` | 清单中的已保存文件和 meta.json |
| `GET /api/tasks/{id}/archive` | ZIP：成功文件、meta.json 与 download-report.json |
| `GET /api/tasks/{id}/cover` | 优先本地封面，未保存时按镜像代理 |
| `GET /api/settings` | 存储路径、Cookie 配置状态 |
| `PUT /api/settings` | `{ "cookie": "..." }`；空字符串清除 |

单作品预览在内存保留 30 分钟、最多 100 条，重启后需重新解析；作者目录的作品预览存入 SQLite，刷新分页不改变预览标识，重启后仍可提交，下载时按 10 分钟规则刷新过期地址。作者页的加载列表和选中状态保留在当前网页会话内，刷新网页后需重新加载；服务重启本身不会清空已入队任务。写入接口限制 JSON 大小，校验跨站来源；抓取限制抖音/字节媒体域名，逐跳验证重定向。拨号使用系统 DNS 解析结果，不按 IP 网段过滤。

本实现遵循仓库根目录 Apache-2.0 许可证。参考来源包括 `src/dtk/urls/`、`src/dtk/media/domains.py`、`src/dtk/platforms/douyin/parser.py`、`src/dtk/signing/native/`、`docker/downloader/safety.go`，未修改原有 Python 服务与 Web 控制台。
