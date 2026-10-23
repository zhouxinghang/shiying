import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
} from "react";
import {
  ArrowDownToLine,
  ArrowRight,
  Check,
  CheckCheck,
  ChevronDown,
  CircleAlert,
  Clipboard,
  Clock3,
  Download,
  ExternalLink,
  Film,
  Images,
  FolderDown,
  HardDrive,
  Heart,
  HelpCircle,
  LayoutDashboard,
  LayoutGrid,
  Link2,
  List,
  LoaderCircle,
  LockKeyhole,
  Menu,
  MoreHorizontal,
  Maximize2,
  Minimize2,
  PanelRightClose,
  PanelRightOpen,
  Play,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  ShieldCheck,
  Shuffle,
  Sparkles,
  Trash2,
  Video as VideoIcon,
  UsersRound,
  X,
} from "lucide-react";
import { AccountPage } from "@/components/account-page";
import { AuthorPage, type AuthorTarget } from "@/components/author-page";
import { AlbumGallery } from "@/components/album-gallery";
import { LibraryAuthorGroups } from "@/components/library-author-groups";
import { RandomAlbumBrowser } from "@/components/random-album-browser";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  api,
  ApiError,
  type Library,
  type Preview,
  type Settings,
  type Task,
  type Video,
} from "@/lib/api";
import { bytes, cn, duration } from "@/lib/utils";
import { groupWorksByAuthor } from "@/lib/library";

type Page =
  | "home"
  | "library"
  | "tasks"
  | "favorites"
  | "random"
  | "account"
  | "authors"
  | "settings"
  | "help";
const pages: Record<Page, string> = {
  home: "工作台",
  library: "作品库",
  tasks: "下载任务",
  favorites: "本地收藏",
  random: "随机看图",
  authors: "作者作品",
  account: "抖音账号",
  settings: "偏好设置",
  help: "使用指南",
};
const statuses: Record<Task["status"], string> = {
  queued: "等待下载",
  downloading: "下载中",
  completed: "已完成",
  failed: "下载失败",
  canceled: "已取消",
  partial: "部分完成",
};
const emptyLibrary: Library = {
  tasks: [],
  stats: { completed: 0, active: 0, failed: 0, total_bytes: 0 },
};

function initialPage(): Page {
  const hash = location.hash.slice(1).split("?")[0];
  return Object.hasOwn(pages, hash) ? (hash as Page) : "home";
}
function Logo({ small = false }: { small?: boolean }) {
  return (
    <div className={cn("brand", small && "brand-small")}>
      <span className="brand-symbol">
        <Film strokeWidth={1.7} />
      </span>
      <span>
        拾影<span className="brand-en">SHIYING</span>
      </span>
    </div>
  );
}

export default function App() {
  const [page, setPage] = useState<Page>(initialPage);
  const [library, setLibrary] = useState<Library>(emptyLibrary);
  const [authorTarget, setAuthorTarget] = useState<AuthorTarget>();
  const [settings, setSettings] = useState<Settings>();
  const [loading, setLoading] = useState(true);
  const [connectionError, setConnectionError] = useState("");
  const [needsAuth, setNeedsAuth] = useState(false);
  const [mobileMenu, setMobileMenu] = useState(false);
  const [text, setText] = useState("");
  const [parseError, setParseError] = useState("");
  const [parsing, setParsing] = useState(false);
  const [preview, setPreview] = useState<Preview>();
  const [selected, setSelected] = useState<Task>();
  const [pending, setPending] = useState("");
  const [toast, setToast] = useState<{ message: string; error: boolean }>();
  const parseController = useRef<AbortController | null>(null);
  const fetching = useRef(false);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const randomViewer = useRef<HTMLElement>(null);

  const notify = useCallback(
    (message: string, error = false) => setToast({ message, error }),
    [],
  );
  useEffect(() => {
    if (!toast) return;
    const timeout = setTimeout(() => setToast(undefined), 4500);
    return () => clearTimeout(timeout);
  }, [toast]);
  const load = useCallback(async () => {
    if (fetching.current) return;
    fetching.current = true;
    try {
      const [data, config] = await Promise.all([
        api<Library>("/tasks"),
        api<Settings>("/settings"),
      ]);
      setLibrary(data);
      setSettings(config);
      setConnectionError("");
      setNeedsAuth(false);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) setNeedsAuth(true);
      else
        setConnectionError(
          error instanceof Error ? error.message : "暂时无法连接本地服务",
        );
    } finally {
      setLoading(false);
      fetching.current = false;
    }
  }, []);
  useEffect(() => {
    void load();
    const interval = setInterval(() => {
      if (!document.hidden) void load();
    }, 2500);
    return () => {
      clearInterval(interval);
      parseController.current?.abort();
    };
  }, [load]);
  useEffect(() => {
    const change = () => setPage(initialPage());
    window.addEventListener("hashchange", change);
    return () => window.removeEventListener("hashchange", change);
  }, []);
  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMobileMenu(false);
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, []);
  function navigate(next: Page) {
    setPage(next);
    location.hash = next;
    setMobileMenu(false);
    window.scrollTo({ top: 0, behavior: "instant" });
  }
  function openAuthor(content: Video) {
    setSelected(undefined);
    setPreview(undefined);
    const text = content.author_uid?.startsWith("MS4")
      ? `https://www.douyin.com/user/${content.author_uid}`
      : content.source_url;
    setAuthorTarget({ text, nonce: Date.now() });
    navigate("authors");
  }
  function newDownload() {
    navigate("home");
    setTimeout(() => textarea.current?.focus(), 50);
  }

  async function parse(event?: FormEvent) {
    event?.preventDefault();
    if (!text.trim() || parsing) return;
    setParsing(true);
    setParseError("");
    const controller = new AbortController();
    parseController.current = controller;
    try {
      setPreview(
        await api<Preview>("/parse", "POST", { text }, controller.signal),
      );
    } catch (error) {
      if (!controller.signal.aborted)
        setParseError(
          error instanceof Error ? error.message : "解析失败，请重试",
        );
    } finally {
      setParsing(false);
    }
  }
  async function paste() {
    try {
      const value = await navigator.clipboard.readText();
      if (value.trim()) {
        setText(value);
        setParseError("");
        textarea.current?.focus();
      } else notify("剪贴板为空，请先复制抖音分享链接");
    } catch {
      notify("浏览器未允许读取剪贴板，请使用 Ctrl / ⌘ + V 粘贴", true);
      textarea.current?.focus();
    }
  }
  async function addDownload() {
    if (!preview || pending) return;
    setPending("add");
    try {
      await api("/tasks", "POST", { preview_id: preview.preview_id });
      setPreview(undefined);
      setText("");
      notify("已加入下载队列");
      navigate("tasks");
      await load();
    } catch (error) {
      notify(error instanceof Error ? error.message : "添加失败", true);
    } finally {
      setPending("");
    }
  }
  async function taskAction(
    task: Task,
    action: "cancel" | "retry" | "favorite" | "delete",
  ) {
    if (pending) return;
    setPending(task.id);
    try {
      await api(
        `/tasks/${task.id}${action === "delete" ? "" : `/${action}`}`,
        action === "delete"
          ? "DELETE"
          : action === "favorite"
            ? "PATCH"
            : "POST",
        action === "favorite" ? { favorite: !task.favorite } : undefined,
      );
      if (action === "delete") setSelected(undefined);
      notify(
        {
          cancel: "任务已取消",
          retry: "已重新加入队列，将刷新视频地址",
          favorite: task.favorite ? "已取消收藏" : "已加入收藏",
          delete: "视频和下载记录已删除",
        }[action],
      );
      await load();
    } catch (error) {
      notify(error instanceof Error ? error.message : "操作失败", true);
    } finally {
      setPending("");
    }
  }
  async function favoriteAlbum(task: Task) {
    if (pending) throw new Error("请等待当前操作完成后重试");
    setPending(task.id);
    try {
      await api(`/tasks/${task.id}/favorite`, "PATCH", { favorite: true });
      setLibrary((current) => ({
        ...current,
        tasks: current.tasks.map((item) =>
          item.id === task.id ? { ...item, favorite: true } : item,
        ),
      }));
    } finally {
      setPending("");
    }
  }
  useEffect(() => {
    if (
      preview?.account_scope &&
      preview.account_scope !== settings?.cookie_revision
    )
      setPreview(undefined);
  }, [settings?.cookie_revision, preview?.account_scope]);
  const selectedTask =
    library.tasks.find((task) => task.id === selected?.id) ?? selected;
  const completed = library.tasks.filter((task) =>
    ["completed", "partial"].includes(task.status),
  );

  if (needsAuth) return <Login onLogin={load} />;
  return (
    <div className={cn("app-shell", page === "random" && "random-browsing")}>
      {page === "random" && (
        <RandomAlbumBrowser
          tasks={library.tasks}
          loading={loading}
          keyboardEnabled={!selected && !preview}
          viewerRef={randomViewer}
          onFavorite={favoriteAlbum}
          onOpen={setSelected}
          onExit={() => navigate("library")}
        />
      )}
      {mobileMenu && (
        <button
          className="sidebar-scrim"
          aria-label="关闭导航"
          onClick={() => setMobileMenu(false)}
        />
      )}
      <aside id="sidebar" className={cn("sidebar", mobileMenu && "is-open")}>
        <a
          href="#home"
          className="brand-link"
          onClick={() => navigate("home")}
          aria-label="拾影工作台"
        >
          <Logo />
        </a>
        <div className="workspace-label">
          个人工作空间 <span>LOCAL</span>
        </div>
        <nav aria-label="主导航">
          <div className="nav-group">
            {(
              [
                { key: "home", icon: LayoutDashboard },
                { key: "library", icon: Film },
                { key: "tasks", icon: FolderDown },
                { key: "favorites", icon: Heart },
                { key: "random", icon: Shuffle },
                { key: "authors", icon: UsersRound },
                { key: "account", icon: ShieldCheck },
              ] as const
            ).map(({ key, icon: Icon }) => (
              <button
                key={key}
                className={cn("nav-item", page === key && "active")}
                onClick={() => navigate(key)}
                aria-current={page === key ? "page" : undefined}
              >
                <Icon size={18} strokeWidth={1.7} />
                <span>{pages[key]}</span>
                {key === "tasks" && library.stats.active > 0 && (
                  <span className="nav-count">{library.stats.active}</span>
                )}
                {key === "home" && page === key && (
                  <span className="nav-active-dot" />
                )}
              </button>
            ))}
          </div>
          <div className="nav-divider" />
          <div className="nav-caption">管理</div>
          <div className="nav-group">
            <button
              className={cn("nav-item", page === "settings" && "active")}
              onClick={() => navigate("settings")}
            >
              <Settings2 size={18} strokeWidth={1.7} />
              偏好设置
            </button>
            <button
              className={cn("nav-item", page === "help" && "active")}
              onClick={() => navigate("help")}
            >
              <HelpCircle size={18} strokeWidth={1.7} />
              使用指南
            </button>
          </div>
        </nav>
        <div className="sidebar-bottom">
          <div className="local-note">
            <span className="local-note-icon">
              <HardDrive size={19} />
            </span>
            <strong>喜欢的，妥善收藏</strong>
            <p>
              视频与图片保存在你的设备上
              <br />
              每一份喜欢，都有迹可循。
            </p>
            <button onClick={() => navigate("settings")}>
              查看存储设置 <ArrowRight size={13} />
            </button>
          </div>
          <div className="sidebar-profile">
            <span className="profile-avatar">我</span>
            <div>
              <strong>我的私人收藏室</strong>
              <span>
                <i className={connectionError ? "offline-dot" : ""} />
                {connectionError ? "服务连接中断" : "本地空间"}
              </span>
            </div>
            <LockKeyhole size={14} />
          </div>
        </div>
      </aside>
      <div className="main-shell" inert={mobileMenu}>
        <header className="topbar">
          <div className="breadcrumb">
            <Button
              variant="ghost"
              size="icon"
              className="mobile-toggle"
              aria-label="打开导航"
              aria-expanded={mobileMenu}
              aria-controls="sidebar"
              onClick={() => setMobileMenu(true)}
            >
              <Menu />
            </Button>
            <span>我的空间</span>
            <span className="breadcrumb-slash">/</span>
            <strong>{pages[page]}</strong>
          </div>
          <div className="topbar-right">
            <span className="local-badge">
              <i className={connectionError ? "offline-dot" : ""} />
              {connectionError ? "连接中断" : "本地运行"}
            </span>
            <span className="topbar-divider" />
            <Button
              variant="ghost"
              size="icon"
              title="偏好设置"
              aria-label="偏好设置"
              onClick={() => navigate("settings")}
            >
              <Settings2 />
            </Button>
            <span className="profile-avatar small">我</span>
          </div>
        </header>
        <main
          id="main"
          className={cn("main-content", page !== "home" && "inner-page")}
        >
          {connectionError && (
            <div className="connection-error" role="alert">
              <CircleAlert size={17} />
              <span>服务连接失败：{connectionError}</span>
              <Button size="sm" variant="outline" onClick={() => void load()}>
                重新连接
              </Button>
            </div>
          )}
          {page === "home" && (
            <>
              <section className="welcome">
                <div>
                  <div className="eyebrow">
                    <span /> YOUR PERSONAL VIDEO ARCHIVE
                  </div>
                  <h1>
                    把喜欢的，留在身边<span>。</span>
                  </h1>
                  <p>收藏灵感、记录心动。让每一份喜欢，都有自己的归处。</p>
                </div>
                <div className="welcome-stamp" aria-hidden="true">
                  <span className="stamp-line" />
                  <Film size={20} strokeWidth={1.3} />
                  <span>
                    KEEP WHAT
                    <br />
                    MOVES YOU.
                  </span>
                </div>
              </section>
              <div className="capture-layout">
                <section
                  className="capture-panel"
                  aria-labelledby="capture-heading"
                >
                  <div className="section-heading">
                    <span className="square-icon">
                      <Link2 size={18} />
                    </span>
                    <h2 id="capture-heading">从一个链接开始</h2>
                    <span className="quiet-badge">抖音视频 / 图集</span>
                  </div>
                  <p className="section-description">
                    粘贴作品链接或分享文案，解析后即可保存到本地。
                  </p>
                  <form onSubmit={parse}>
                    <div
                      className={cn(
                        "link-input-wrap",
                        parsing && "is-parsing",
                        parseError && "has-error",
                      )}
                    >
                      <textarea
                        ref={textarea}
                        id="share-link"
                        aria-label="抖音分享链接或分享文案"
                        aria-describedby={
                          parseError ? "parse-error" : "link-help"
                        }
                        placeholder="在这里粘贴抖音分享链接或分享文案…"
                        value={text}
                        onChange={(event) => {
                          setText(event.target.value);
                          setParseError("");
                        }}
                        onKeyDown={(event) => {
                          if (
                            (event.metaKey || event.ctrlKey) &&
                            event.key === "Enter"
                          )
                            void parse();
                        }}
                        maxLength={10000}
                        disabled={parsing}
                      />
                      <div className="input-tools">
                        <span>
                          <Link2 size={14} /> v.douyin.com / douyin.com
                        </span>
                        {text ? (
                          <button
                            type="button"
                            aria-label="清空链接"
                            disabled={parsing}
                            onClick={() => {
                              setText("");
                              setParseError("");
                            }}
                          >
                            <X size={15} />
                          </button>
                        ) : (
                          <button type="button" onClick={() => void paste()}>
                            <Clipboard size={14} />
                            粘贴
                          </button>
                        )}
                      </div>
                    </div>
                    {parseError && (
                      <p id="parse-error" className="inline-error" role="alert">
                        <CircleAlert size={15} />
                        {parseError}
                      </p>
                    )}
                    <div className="capture-footer">
                      <span id="link-help">
                        <ShieldCheck size={14} />
                        原始画质 · 本地存储
                      </span>
                      {parsing ? (
                        <Button
                          variant="outline"
                          type="button"
                          onClick={() => parseController.current?.abort()}
                        >
                          <LoaderCircle className="animate-spin" />
                          解析中 · 取消
                        </Button>
                      ) : (
                        <Button
                          type="submit"
                          disabled={!text.trim()}
                          className="parse-button"
                        >
                          解析作品 <ArrowRight size={16} />
                        </Button>
                      )}
                    </div>
                  </form>
                </section>
                <aside className="guide-panel">
                  <div className="guide-title">
                    <Sparkles size={17} />
                    <span>三步，留住喜欢</span>
                  </div>
                  <ol>
                    {[
                      { title: "复制分享链接", desc: "抖音 → 分享 → 复制链接" },
                      { title: "粘贴并解析", desc: "确认视频或图集与作者" },
                      { title: "下载到本地", desc: "保存视频或图片，随时回看" },
                    ].map((step, i) => (
                      <li key={step.title}>
                        <span className="step-number">0{i + 1}</span>
                        <div>
                          <strong>{step.title}</strong>
                          <p>{step.desc}</p>
                        </div>
                        {i === 2 && <Check size={15} className="step-check" />}
                      </li>
                    ))}
                  </ol>
                  <button
                    className="guide-link"
                    onClick={() => navigate("help")}
                  >
                    第一次使用？看看详细指南 <ArrowRight size={13} />
                  </button>
                </aside>
              </div>
              <section className="stats-grid" aria-label="收藏概览">
                {[
                  {
                    label: "已保存作品",
                    value: library.stats.completed.toString(),
                    unit: "个",
                    icon: VideoIcon,
                    description: "每一段喜欢，都在这里",
                  },
                  {
                    label: "下载任务",
                    value: library.stats.active.toString(),
                    unit: "个进行中",
                    icon: ArrowDownToLine,
                    description: library.stats.failed
                      ? `${library.stats.failed} 个任务需要重试`
                      : "你的收藏，有条不紊",
                  },
                  {
                    label: "已用存储",
                    value: bytes(library.stats.total_bytes).split(" ")[0],
                    unit: bytes(library.stats.total_bytes).split(" ")[1],
                    icon: HardDrive,
                    description: "文件安全保存在本地",
                  },
                ].map((stat) => (
                  <div className="stat-card" key={stat.label}>
                    <div className="stat-top">
                      <span>{stat.label}</span>
                      <stat.icon size={17} strokeWidth={1.6} />
                    </div>
                    <div className="stat-value">
                      {loading ? (
                        <span className="skeleton-number" />
                      ) : (
                        stat.value
                      )}
                      <span>{stat.unit}</span>
                    </div>
                    <p>{stat.description}</p>
                  </div>
                ))}
              </section>
              <section className="recent-section">
                <div className="library-heading">
                  <div>
                    <h2>
                      最近保存
                      <span className="count-tag">{completed.length}</span>
                    </h2>
                    <p>留住片刻灵感，随时重温喜欢。</p>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => navigate("library")}
                  >
                    查看全部 <ArrowRight size={14} />
                  </Button>
                </div>
                {loading ? (
                  <SkeletonGrid />
                ) : completed.length ? (
                  <div className="video-grid">
                    {completed.slice(0, 6).map((task) => (
                      <VideoCard
                        key={task.id}
                        task={task}
                        onOpen={setSelected}
                        onFavorite={(task) => void taskAction(task, "favorite")}
                        disabled={!!pending}
                      />
                    ))}
                  </div>
                ) : (
                  <EmptyState kind="home" onNew={newDownload} />
                )}
              </section>
            </>
          )}
          {(page === "library" || page === "tasks" || page === "favorites") && (
            <LibraryView
              key={page}
              page={page}
              library={library}
              loading={loading}
              pending={pending}
              onNew={newDownload}
              onOpen={setSelected}
              onAction={taskAction}
            />
          )}
          <AuthorPage
            active={page === "authors"}
            target={authorTarget}
            tasks={library.tasks}
            onPreview={setPreview}
            onTask={setSelected}
            onTasks={() => navigate("tasks")}
            onQueued={load}
          />
          <AccountPage
            active={page === "account"}
            cookieRevision={settings?.cookie_revision || ""}
            cookieConfigured={!!settings?.cookie_configured}
            tasks={library.tasks}
            onPreview={setPreview}
            onTask={setSelected}
            onTasks={() => navigate("tasks")}
            onQueued={load}
            onSettings={() => navigate("settings")}
          />
          {page === "settings" && (
            <SettingsPage
              settings={settings}
              onSaved={(value) => {
                setSettings(value);
                notify("设置已保存");
              }}
              onError={(message) => notify(message, true)}
            />
          )}
          {page === "help" && (
            <HelpPage
              onNew={newDownload}
              onSettings={() => navigate("settings")}
            />
          )}
          <footer className="page-footer">
            <span>
              <Film size={13} /> 拾影 <span className="footer-dot">·</span>{" "}
              让喜欢，有处安放
            </span>
            <span>
              你的内容，只在你的设备上 <LockKeyhole size={12} />
            </span>
          </footer>
        </main>
      </div>
      <Dialog
        open={!!preview}
        onOpenChange={(open) => {
          if (!open && pending !== "add") setPreview(undefined);
        }}
      >
        <DialogContent>
          <DialogTitle>找到这段喜欢了</DialogTitle>
          <DialogDescription>
            确认作品信息，将视频或整组图片保存到本地收藏室。
          </DialogDescription>
          {preview && (
            <>
              {preview.video.kind === "image_album" ? (
                <AlbumGallery content={preview.video} />
              ) : (
                <div className="preview-cover">
                  <Cover video={preview.video} />
                  <span>{duration(preview.video.duration)}</span>
                </div>
              )}
              <div className="preview-meta">
                <h3>{preview.video.title}</h3>
                <p>
                  @{preview.video.author}
                  <span>
                    {preview.video.kind === "image_album"
                      ? `${preview.video.image_count} 张图片`
                      : `${preview.video.width} × ${preview.video.height}`}
                  </span>
                </p>
              </div>
              <Button
                variant="outline"
                onClick={() => openAuthor(preview.video)}
              >
                <UsersRound />
                查看作者全部作品
                <ArrowRight />
              </Button>
              <div className="preview-note">
                <ShieldCheck size={16} />
                保存视频或整组图片及封面，支持逐个导出和 ZIP 打包。
              </div>
              <Button
                size="lg"
                disabled={!!pending}
                onClick={() => void addDownload()}
              >
                {pending === "add" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Download />
                )}
                保存到本地
              </Button>
            </>
          )}
        </DialogContent>
      </Dialog>
      <TaskDialog
        task={selectedTask}
        open={!!selected}
        onClose={() => setSelected(undefined)}
        onAction={taskAction}
        pending={pending}
        onAuthor={openAuthor}
        onReturnFocus={
          page === "random"
            ? () => randomViewer.current?.focus({ preventScroll: true })
            : undefined
        }
      />
      {toast && (
        <div
          className={cn("toast", toast.error && "toast-error")}
          role="status"
        >
          {toast.error ? <CircleAlert size={18} /> : <CheckCheck size={18} />}
          <span>{toast.message}</span>
          <button aria-label="关闭提示" onClick={() => setToast(undefined)}>
            <X size={15} />
          </button>
        </div>
      )}
    </div>
  );
}

function Cover({ video }: { video: Video }) {
  const [failed, setFailed] = useState(false);
  return (
    <>
      {!failed && video.cover ? (
        <img
          src={video.cover}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
          onError={() => setFailed(true)}
        />
      ) : (
        <div className="cover-placeholder">
          <Film size={32} strokeWidth={1} />
          <span>拾影 · 本地收藏</span>
        </div>
      )}
    </>
  );
}

function VideoCard({
  task,
  onOpen,
  onFavorite,
  disabled,
}: {
  task: Task;
  onOpen: (task: Task) => void;
  onFavorite: (task: Task) => void;
  disabled: boolean;
}) {
  return (
    <article className="video-card">
      <button
        className="video-thumbnail"
        onClick={() => onOpen(task)}
        aria-label={`查看 ${task.title}`}
      >
        <Cover video={task} />
        <span className="video-duration">
          {task.kind === "image_album"
            ? `${task.image_count} 张图片`
            : duration(task.duration)}
        </span>
        <span className="play-hover">
          {task.kind === "image_album" ? (
            <Images size={22} />
          ) : (
            <Play size={22} fill="currentColor" />
          )}
        </span>
      </button>
      <div className="video-info">
        <button
          className="video-title"
          title={task.title}
          onClick={() => onOpen(task)}
        >
          {task.title}
        </button>
        <div className="video-author">@{task.author}</div>
        <div className="video-card-footer">
          <span>
            {new Date(task.created_at).toLocaleDateString("zh-CN", {
              month: "2-digit",
              day: "2-digit",
            })}
            <i />
            {bytes(task.downloaded)}
          </span>
          <div>
            <Button
              variant="ghost"
              size="icon"
              className={cn("card-action", task.favorite && "is-favorite")}
              disabled={disabled}
              aria-label={task.favorite ? "取消收藏" : "收藏作品"}
              aria-pressed={task.favorite}
              onClick={() => onFavorite(task)}
            >
              <Heart fill={task.favorite ? "currentColor" : "none"} />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="card-action"
              aria-label="作品详情"
              onClick={() => onOpen(task)}
            >
              <MoreHorizontal />
            </Button>
          </div>
        </div>
      </div>
    </article>
  );
}

function EmptyState({ kind, onNew }: { kind: string; onNew: () => void }) {
  const copy =
    kind === "favorites"
      ? ["把特别喜欢的，放在这里", "点击作品上的爱心，即可加入你的专属收藏。"]
      : kind === "search"
        ? ["没有找到相关作品", "试试其他关键词，或切换筛选条件。"]
        : kind === "tasks"
          ? ["下载队列，准备就绪", "添加第一个作品，在这里查看下载进度。"]
          : [
              "第一段喜欢，等你收藏",
              "粘贴一条抖音链接，开启你的私人作品收藏室。",
            ];
  return (
    <div className="empty-state">
      <div className="empty-art" aria-hidden="true">
        <div className="empty-orbit" />
        <div className="art-card back">
          <Film size={25} strokeWidth={1} />
        </div>
        <div className="art-card front">
          <Play size={21} fill="currentColor" strokeWidth={1} />
          <span />
        </div>
        <span className="art-spark">+</span>
        <span className="art-dot" />
      </div>
      <h3>{copy[0]}</h3>
      <p>{copy[1]}</p>
      {kind !== "search" && kind !== "favorites" && (
        <button className="empty-cta" onClick={onNew}>
          <Plus size={14} />
          添加第一个作品 <ArrowRight size={13} />
        </button>
      )}
    </div>
  );
}
function SkeletonGrid({ portrait = false }: { portrait?: boolean }) {
  return (
    <div
      className={cn("video-grid", portrait && "library-portrait-grid")}
      aria-label="正在加载作品"
    >
      {[1, 2, 3].map((i) => (
        <div key={i} className="skeleton-card">
          <div />
          <span />
          <span />
        </div>
      ))}
    </div>
  );
}

function LibraryView({
  page,
  library,
  loading,
  pending,
  onNew,
  onOpen,
  onAction,
}: {
  page: "library" | "tasks" | "favorites";
  library: Library;
  loading: boolean;
  pending: string;
  onNew: () => void;
  onOpen: (task: Task) => void;
  onAction: (
    task: Task,
    action: "cancel" | "retry" | "favorite" | "delete",
  ) => Promise<void>;
}) {
  const [filter, setFilter] = useState("all");
  const [search, setSearch] = useState("");
  const [order, setOrder] = useState("new");
  const [grouping, setGrouping] = useState<"all" | "author">(() =>
    new URLSearchParams(location.hash.split("?")[1]).get("view") === "all"
      ? "all"
      : "author",
  );
  useEffect(() => {
    const update = () =>
      setGrouping(
        new URLSearchParams(location.hash.split("?")[1]).get("view") === "all"
          ? "all"
          : "author",
      );
    window.addEventListener("hashchange", update);
    return () => window.removeEventListener("hashchange", update);
  }, []);
  const [layout, setLayout] = useState<"grid" | "list">(
    page === "tasks" ? "list" : "grid",
  );
  const base = library.tasks.filter((task) =>
    page === "library"
      ? ["completed", "partial"].includes(task.status)
      : page === "favorites"
        ? task.favorite && ["completed", "partial"].includes(task.status)
        : true,
  );
  const filtered = base
    .filter(
      (task) =>
        (filter === "all" ||
          (filter === "active"
            ? ["queued", "downloading"].includes(task.status)
            : task.status === filter)) &&
        `${task.title} ${task.author}`
          .toLocaleLowerCase()
          .includes(search.toLocaleLowerCase()),
    )
    .sort((a, b) =>
      order === "new"
        ? b.created_at.localeCompare(a.created_at)
        : a.created_at.localeCompare(b.created_at),
    );
  const groups = groupWorksByAuthor(
    [...base].sort((a, b) =>
      order === "new"
        ? b.created_at.localeCompare(a.created_at)
        : a.created_at.localeCompare(b.created_at),
    ),
  );
  const totalAuthors = groups.length;
  function renderTasks(tasks: Task[]) {
    return layout === "grid" ? (
      <div className="video-grid library-portrait-grid">
        {tasks.map((task) => (
          <VideoCard
            key={task.id}
            task={task}
            onOpen={onOpen}
            onFavorite={(task) => void onAction(task, "favorite")}
            disabled={!!pending}
          />
        ))}
      </div>
    ) : (
      <div className="task-list">
        {tasks.map((task) => (
          <div className="task-row" key={task.id}>
            <button
              className="task-thumb"
              onClick={() => onOpen(task)}
              aria-label={`查看 ${task.title}`}
            >
              <Cover video={task} />
              {["completed", "partial"].includes(task.status) &&
                (task.kind === "image_album" ? (
                  <Images size={16} />
                ) : (
                  <Play size={16} fill="currentColor" />
                ))}
            </button>
            <div className="task-row-info">
              <button onClick={() => onOpen(task)} className="video-title">
                {task.title}
              </button>
              <p>
                @{task.author}
                <span>·</span>
                {task.kind === "image_album"
                  ? `${task.image_count} 张图片`
                  : duration(task.duration)}
                <span>·</span>
                {bytes(task.downloaded)}
              </p>
              {task.status === "downloading" && (
                <div
                  className="download-progress"
                  role="progressbar"
                  aria-label="下载进度"
                  aria-valuenow={
                    task.files_total
                      ? Math.round(
                          (task.files_completed / task.files_total) * 100,
                        )
                      : undefined
                  }
                  aria-valuemin={0}
                  aria-valuemax={100}
                >
                  <div
                    style={{
                      width: task.files_total
                        ? `${Math.min((task.files_completed / task.files_total) * 100, 100)}%`
                        : "35%",
                    }}
                    className={!task.total ? "indeterminate" : ""}
                  />
                </div>
              )}
              {["failed", "partial"].includes(task.status) && (
                <p className="task-error">{task.error}</p>
              )}
            </div>
            <span className={cn("status-badge", `status-${task.status}`)}>
              {task.status === "downloading" && (
                <LoaderCircle className="animate-spin" size={12} />
              )}
              {["completed", "partial"].includes(task.status) && (
                <Check size={12} />
              )}
              {statuses[task.status]}
              {task.status === "downloading" &&
                ` ${task.files_completed}/${task.files_total} 个文件`}
            </span>
            <div className="task-actions">
              {["failed", "canceled", "partial"].includes(task.status) && (
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="重试下载"
                  disabled={!!pending}
                  onClick={() => void onAction(task, "retry")}
                >
                  <RefreshCw />
                </Button>
              )}
              {["queued", "downloading"].includes(task.status) && (
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="取消下载"
                  disabled={!!pending}
                  onClick={() => void onAction(task, "cancel")}
                >
                  <X />
                </Button>
              )}
              {["completed", "partial"].includes(task.status) && (
                <Button variant="ghost" size="icon" asChild>
                  <a
                    href={`/api/tasks/${task.id}/${task.kind === "image_album" || (task.files?.length && !task.files.some((file) => file.kind === "video" && file.state === "done")) ? "archive" : "file?download=1"}`}
                    aria-label="导出作品"
                  >
                    <ArrowDownToLine />
                  </a>
                </Button>
              )}
              <Button
                variant="ghost"
                size="icon"
                aria-label="任务详情"
                onClick={() => onOpen(task)}
              >
                <MoreHorizontal />
              </Button>
            </div>
          </div>
        ))}
      </div>
    );
  }
  const subtitle =
    page === "tasks"
      ? "从等待到完成，每一段下载都清晰可见。"
      : page === "favorites"
        ? "值得反复回看的，值得特别收藏。"
        : "那些让你停下来的片刻，都在这里。";
  return (
    <>
      <div className="page-heading">
        <div>
          <div className="eyebrow">YOUR COLLECTION, YOUR SPACE</div>
          <h1>
            {pages[page]}
            <span className="heading-count">{base.length}</span>
          </h1>
          <p>{subtitle}</p>
        </div>
        <Button onClick={onNew}>
          <Plus />
          新建下载
        </Button>
      </div>
      <section className="library-section">
        <div className="library-toolbar">
          <Tabs
            value={page === "tasks" ? filter : grouping}
            onValueChange={(value) =>
              page === "tasks"
                ? setFilter(value)
                : (() => {
                    setGrouping(value as "all" | "author");
                    location.hash = value === "all" ? `${page}?view=all` : page;
                  })()
            }
          >
            <TabsList>
              <TabsTrigger value="all">
                {page === "tasks" ? "全部任务" : "全部作品"}
                <span className="tab-count">{base.length}</span>
              </TabsTrigger>
              {page !== "tasks" && (
                <TabsTrigger value="author">
                  <UsersRound size={15} aria-hidden="true" />
                  按作者分组
                  <span className="tab-count">{totalAuthors}</span>
                </TabsTrigger>
              )}
              {page === "tasks" && (
                <>
                  <TabsTrigger value="active">
                    进行中
                    <span className="tab-count">{library.stats.active}</span>
                  </TabsTrigger>
                  <TabsTrigger value="completed">已完成</TabsTrigger>
                  <TabsTrigger value="partial">部分完成</TabsTrigger>
                  <TabsTrigger value="failed">失败</TabsTrigger>
                  <TabsTrigger value="canceled">已取消</TabsTrigger>
                </>
              )}
            </TabsList>
          </Tabs>
        </div>
        {(page === "tasks" || grouping === "all") && (
          <div className="library-filters">
            <label className="search-box">
              <Search size={16} />
              <input
                aria-label="搜索作品或作者"
                placeholder="搜索作品或作者…"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
              {search && (
                <button aria-label="清空搜索" onClick={() => setSearch("")}>
                  <X size={14} />
                </button>
              )}
            </label>
            <div className="filter-actions">
              <label className="sort-select">
                <select
                  aria-label="排序方式"
                  value={order}
                  onChange={(event) => setOrder(event.target.value)}
                >
                  <option value="new">最新添加</option>
                  <option value="old">最早添加</option>
                </select>
                <ChevronDown size={14} />
              </label>
              {page !== "tasks" && (
                <div className="layout-switch">
                  <button
                    aria-label="网格视图"
                    aria-pressed={layout === "grid"}
                    onClick={() => setLayout("grid")}
                    className={layout === "grid" ? "selected" : ""}
                  >
                    <LayoutGrid size={16} />
                  </button>
                  <button
                    aria-label="列表视图"
                    aria-pressed={layout === "list"}
                    onClick={() => setLayout("list")}
                    className={layout === "list" ? "selected" : ""}
                  >
                    <List size={17} />
                  </button>
                </div>
              )}
            </div>
          </div>
        )}
        {loading ? (
          <SkeletonGrid portrait={page !== "tasks"} />
        ) : grouping === "author" && page !== "tasks" ? (
          <LibraryAuthorGroups
            groups={groups}
            page={page}
            layout={layout}
            setLayout={setLayout}
            order={order}
            setOrder={setOrder}
          >
            {renderTasks}
          </LibraryAuthorGroups>
        ) : !filtered.length ? (
          <EmptyState kind={base.length ? "search" : page} onNew={onNew} />
        ) : (
          renderTasks(filtered)
        )}
        {(page === "tasks" || grouping === "all") && (
          <div className="library-bottom">
            <span>
              共 {filtered.length} 个{page === "tasks" ? "任务" : "作品"}
            </span>
            <span>
              <ShieldCheck size={13} />
              仅保存在本地
            </span>
          </div>
        )}
      </section>
    </>
  );
}

function TaskDialog({
  task,
  open,
  onClose,
  onAction,
  pending,
  onAuthor,
  onReturnFocus,
}: {
  task?: Task;
  open: boolean;
  onClose: () => void;
  onAction: (
    task: Task,
    action: "cancel" | "retry" | "favorite" | "delete",
  ) => Promise<void>;
  pending: string;
  onAuthor: (content: Video) => void;
  onReturnFocus?: () => void;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [playbackError, setPlaybackError] = useState(false);
  const [infoVisible, setInfoVisible] = useState(
    () => window.matchMedia("(min-width: 900px)").matches,
  );
  const [maximized, setMaximized] = useState(false);
  const [narrow, setNarrow] = useState(
    () => window.matchMedia("(max-width: 899px)").matches,
  );
  const panelId = useId();
  const infoButton = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    setConfirmDelete(false);
    setPlaybackError(false);
    setMaximized(false);
    setInfoVisible(window.matchMedia("(min-width: 900px)").matches);
  }, [task?.id, open]);
  useEffect(() => {
    const media = window.matchMedia("(max-width: 899px)");
    const change = () => {
      setNarrow(media.matches);
      if (media.matches) setInfoVisible(false);
    };
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  const saved = !!task && ["completed", "partial"].includes(task.status);
  const playable =
    saved &&
    (task?.files?.some(
      (file) => file.kind === "video" && file.state === "done",
    ) ||
      !task?.files?.length);
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!value) onClose();
      }}
    >
      <DialogContent
        onCloseAutoFocus={
          onReturnFocus
            ? (event) => {
                event.preventDefault();
                onReturnFocus();
              }
            : undefined
        }
        onEscapeKeyDown={(event) => {
          if (!confirmDelete && narrow && infoVisible) {
            event.preventDefault();
            setInfoVisible(false);
            infoButton.current?.focus({ preventScroll: true });
          }
        }}
        className={
          confirmDelete
            ? "max-w-lg"
            : cn("work-detail-dialog", maximized && "work-detail-maximized")
        }
      >
        {confirmDelete ? (
          <>
            <DialogTitle>删除这个作品？</DialogTitle>
            <DialogDescription>
              本地视频、图集图片、封面、收藏状态和下载记录将一起删除。
            </DialogDescription>
            <p className="text-sm leading-6 text-muted-foreground">
              {task?.title}
            </p>
            <div className="flex justify-end gap-3">
              <Button variant="outline" onClick={() => setConfirmDelete(false)}>
                保留作品
              </Button>
              <Button
                variant="destructive"
                disabled={!!pending}
                onClick={() => {
                  if (task) void onAction(task, "delete");
                }}
              >
                {pending ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Trash2 />
                )}
                确认删除
              </Button>
            </div>
          </>
        ) : (
          task && (
            <>
              <header className="work-detail-header">
                <span className="work-detail-symbol">
                  {task.kind === "image_album" ? (
                    <Images size={19} aria-hidden="true" />
                  ) : (
                    <VideoIcon size={19} aria-hidden="true" />
                  )}
                </span>
                <div className="work-detail-heading">
                  <DialogTitle title={task.title}>
                    {task.title || "作品详情"}
                  </DialogTitle>
                  <DialogDescription>
                    @{task.author} · {statuses[task.status]}
                  </DialogDescription>
                </div>
                <div className="work-detail-tools">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="work-maximize-button"
                    aria-label={maximized ? "退出最大化预览" : "最大化预览"}
                    title={maximized ? "退出最大化预览" : "最大化预览"}
                    aria-pressed={maximized}
                    onClick={() => {
                      setMaximized(!maximized);
                      if (!maximized) setInfoVisible(false);
                    }}
                  >
                    {maximized ? <Minimize2 /> : <Maximize2 />}
                  </Button>
                  <Button
                    ref={infoButton}
                    variant="outline"
                    size="sm"
                    aria-controls={panelId}
                    aria-expanded={infoVisible}
                    onClick={() => setInfoVisible(!infoVisible)}
                  >
                    {infoVisible ? <PanelRightClose /> : <PanelRightOpen />}
                    {narrow
                      ? infoVisible
                        ? "返回预览"
                        : "作品信息"
                      : infoVisible
                        ? "收起信息"
                        : "作品信息"}
                  </Button>
                </div>
              </header>
              <div className="work-detail-body" data-info={infoVisible}>
                <div
                  className="work-detail-main"
                  inert={narrow && infoVisible ? true : undefined}
                  aria-hidden={narrow && infoVisible ? true : undefined}
                >
                  {task.kind === "image_album" ? (
                    <AlbumGallery
                      content={task}
                      saved={saved}
                      viewer
                      focusMode={maximized}
                      keyboardEnabled={!narrow || !infoVisible}
                    />
                  ) : (
                    <div className="work-video-stage">
                      {playable ? (
                        <video
                          key={task.id}
                          className="video-player"
                          controls
                          playsInline
                          preload="metadata"
                          poster={task.cover}
                          src={`/api/tasks/${task.id}/file`}
                          onError={() => setPlaybackError(true)}
                        />
                      ) : (
                        <>
                          <Cover video={task} />
                          <div className="work-media-pending">
                            <span
                              className={cn(
                                "status-badge",
                                `status-${task.status}`,
                              )}
                            >
                              {statuses[task.status]}
                            </span>
                            <span>
                              {task.files_completed}/{task.files_total}{" "}
                              个文件已保存
                            </span>
                          </div>
                        </>
                      )}
                    </div>
                  )}
                  {playbackError && (
                    <p role="alert" className="work-playback-error">
                      <CircleAlert size={16} />
                      视频暂时无法播放，可导出后使用本地播放器打开。
                    </p>
                  )}
                </div>
                <aside
                  id={panelId}
                  className="work-detail-info"
                  aria-label="作品信息"
                  hidden={!infoVisible}
                >
                  <div className="work-info-heading">
                    <h2>作品信息</h2>
                    {narrow && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setInfoVisible(false);
                          infoButton.current?.focus({ preventScroll: true });
                        }}
                      >
                        <ArrowRight />
                        返回预览
                      </Button>
                    )}
                  </div>
                  <h3 className="detail-title">
                    {task.description || task.title}
                  </h3>
                  <div className="detail-meta">
                    <span>
                      <Clock3 size={14} />
                      {task.kind === "image_album"
                        ? `${task.image_count} 张图片`
                        : duration(task.duration)}
                    </span>
                    <span>
                      <HardDrive size={14} />
                      {bytes(task.downloaded)}
                    </span>
                    <span>
                      {task.kind === "image_album"
                        ? `${task.files_completed}/${task.files_total} 个文件已保存`
                        : `${task.width} × ${task.height}`}
                    </span>
                  </div>
                  {task.error && (
                    <div className="inline-error">
                      <CircleAlert size={15} />
                      {task.error}
                    </div>
                  )}
                  {task.files?.length > 0 && (
                    <details className="stored-files">
                      <summary>
                        文件清单 · {task.files_completed}/{task.files_total}{" "}
                        已保存
                      </summary>
                      <ul>
                        {task.files.map((file) => (
                          <li key={file.name}>
                            <span>
                              {file.name}
                              <small>
                                {file.state === "done"
                                  ? `${bytes(file.bytes)}${file.retained ? " · 保留上次文件" : ""}`
                                  : file.error || "等待保存"}
                              </small>
                            </span>
                            {file.state === "done" &&
                            ["completed", "partial"].includes(task.status) ? (
                              <a
                                href={`/api/tasks/${task.id}/files/${file.name}?download=1`}
                                aria-label={`导出 ${file.name}`}
                              >
                                <ArrowDownToLine size={16} />
                              </a>
                            ) : (
                              <CircleAlert size={15} />
                            )}
                          </li>
                        ))}
                      </ul>
                    </details>
                  )}
                  <Button variant="outline" onClick={() => onAuthor(task)}>
                    <UsersRound />
                    查看作者全部作品
                    <ArrowRight />
                  </Button>
                  <div className="detail-actions">
                    {["completed", "partial"].includes(task.status) &&
                      (task.kind === "image_album" ||
                        !task.files?.length ||
                        task.files.some(
                          (file) =>
                            file.kind === "video" && file.state === "done",
                        )) && (
                        <Button asChild>
                          <a href={`/api/tasks/${task.id}/file?download=1`}>
                            <ArrowDownToLine />
                            {task.kind === "image_album"
                              ? "打包导出图集"
                              : "导出视频"}
                          </a>
                        </Button>
                      )}
                    {["completed", "partial"].includes(task.status) &&
                      task.kind !== "image_album" && (
                        <Button variant="outline" asChild>
                          <a href={`/api/tasks/${task.id}/archive`}>
                            <FolderDown />
                            导出全部文件
                          </a>
                        </Button>
                      )}
                    {["failed", "canceled", "partial"].includes(
                      task.status,
                    ) && (
                      <Button
                        disabled={!!pending}
                        onClick={() => void onAction(task, "retry")}
                      >
                        <RefreshCw />
                        重新下载
                      </Button>
                    )}
                    {["queued", "downloading"].includes(task.status) && (
                      <Button
                        variant="outline"
                        disabled={!!pending}
                        onClick={() => void onAction(task, "cancel")}
                      >
                        <X />
                        取消下载
                      </Button>
                    )}
                    <Button variant="outline" asChild>
                      <a
                        href={task.source_url}
                        target="_blank"
                        rel="noreferrer"
                      >
                        <ExternalLink />
                        抖音原作品
                      </a>
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={task.favorite ? "取消收藏" : "收藏作品"}
                      aria-pressed={task.favorite}
                      disabled={!!pending}
                      onClick={() => void onAction(task, "favorite")}
                    >
                      <Heart fill={task.favorite ? "currentColor" : "none"} />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="ml-auto text-destructive"
                      aria-label="删除作品和记录"
                      disabled={!!pending}
                      onClick={() => setConfirmDelete(true)}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </aside>
              </div>
            </>
          )
        )}
      </DialogContent>
    </Dialog>
  );
}

function SettingsPage({
  settings,
  onSaved,
  onError,
}: {
  settings?: Settings;
  onSaved: (settings: Settings) => void;
  onError: (message: string) => void;
}) {
  const [cookie, setCookie] = useState("");
  const [saving, setSaving] = useState(false);
  const [revealed, setRevealed] = useState(false);
  async function save(clear = false) {
    setSaving(true);
    try {
      onSaved(
        await api<Settings>("/settings", "PUT", {
          cookie: clear ? "" : cookie,
        }),
      );
      setCookie("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }
  return (
    <>
      <div className="page-heading">
        <div>
          <div className="eyebrow">MAKE YOURSELF AT HOME</div>
          <h1>偏好设置</h1>
          <p>简单配置，让收藏更顺手。</p>
        </div>
      </div>
      <div className="settings-panel">
        <div className="settings-section-title">
          <span className="square-icon">
            <ShieldCheck size={19} />
          </span>
          <div>
            <h2>抖音访问配置</h2>
            <p>配置 Cookie 后使用作品详情接口解析；遇到验证时可更新 Cookie。</p>
          </div>
          <span
            className={cn(
              "status-badge",
              settings?.cookie_configured
                ? "status-completed"
                : "status-canceled",
            )}
          >
            {settings?.cookie_configured ? "已配置" : "未配置"}
          </span>
        </div>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <label htmlFor="douyin-cookie" className="form-label">
            抖音 Cookie <span>可选</span>
          </label>
          <p className="field-hint">
            在浏览器打开抖音并登录，从开发者工具的网络请求中复制 Cookie 请求头。
          </p>
          <div className="cookie-input-wrap">
            <textarea
              id="douyin-cookie"
              placeholder={
                settings?.cookie_configured
                  ? "已安全保存。填写新 Cookie 可替换现有配置…"
                  : "粘贴完整 Cookie，例如：sessionid=…; ttwid=…"
              }
              value={cookie}
              onChange={(event) => setCookie(event.target.value)}
              className={cn("cookie-textarea", !revealed && "masked")}
              autoComplete="off"
              spellCheck={false}
              maxLength={8192}
            />
            <button type="button" onClick={() => setRevealed(!revealed)}>
              {revealed ? "隐藏内容" : "显示内容"}
            </button>
          </div>
          <div className="settings-note">
            <LockKeyhole size={15} />
            <span>
              Cookie 仅存储在本机 SQLite
              中，保存后不会回显。请妥善保管数据目录。
            </span>
          </div>
          <div className="settings-actions">
            {settings?.cookie_configured && (
              <Button
                type="button"
                variant="ghost"
                disabled={saving}
                onClick={() => void save(true)}
              >
                清除已保存 Cookie
              </Button>
            )}
            <Button type="submit" disabled={saving || !cookie.trim()}>
              {saving && <LoaderCircle className="animate-spin" />}保存配置
            </Button>
          </div>
        </form>
      </div>
      <div className="settings-panel">
        <div className="settings-section-title">
          <span className="square-icon">
            <HardDrive size={19} />
          </span>
          <div>
            <h2>本地存储</h2>
            <p>作品文件与记录由同一个 Go 服务管理。</p>
          </div>
        </div>
        <dl className="settings-facts">
          <div>
            <dt>数据目录</dt>
            <dd>
              <code>{settings?.data_dir || "正在读取…"}</code>
            </dd>
          </div>
          <div>
            <dt>作品目录</dt>
            <dd>
              <code>
                {settings ? `${settings.data_dir}/videos` : "正在读取…"}
              </code>
            </dd>
          </div>
          <div>
            <dt>同时下载</dt>
            <dd>{settings?.concurrency ?? 2} 个任务</dd>
          </div>
          <div>
            <dt>单文件上限</dt>
            <dd>{bytes(settings?.max_video_bytes ?? 2 * 1024 ** 3)}</dd>
          </div>
          <div>
            <dt>当前版本</dt>
            <dd>拾影 v{settings?.version ?? "1.0.0"}</dd>
          </div>
        </dl>
        <p className="storage-help">
          需要更换存储位置？启动服务时使用 <code>-data /你的目录</code>{" "}
          参数。迁移前先停止服务，再移动整个数据目录。
        </p>
      </div>
    </>
  );
}

function HelpPage({
  onNew,
  onSettings,
}: {
  onNew: () => void;
  onSettings: () => void;
}) {
  return (
    <>
      <div className="page-heading">
        <div>
          <div className="eyebrow">A LITTLE GUIDE TO KEEPING</div>
          <h1>收藏，从这里开始</h1>
          <p>无需复杂操作，三步保存一段喜欢。</p>
        </div>
        <Button onClick={onNew}>
          开始收藏 <ArrowRight />
        </Button>
      </div>
      <div className="help-steps">
        {[
          {
            icon: Clipboard,
            title: "在抖音复制链接",
            description:
              "打开想保存的视频或图集，点击分享按钮，选择“复制链接”。完整分享文案也可以直接使用。",
          },
          {
            icon: Link2,
            title: "回到拾影解析",
            description:
              "把链接粘贴到工作台，点击“解析作品”，确认封面、作者和作品内容。",
          },
          {
            icon: Download,
            title: "保存，随时重温",
            description:
              "点击“保存到本地”，等待下载完成。在作品库播放、收藏，或导出到自己的设备。",
          },
        ].map((step, i) => (
          <article key={step.title}>
            <div className="help-step-top">
              <step.icon size={23} strokeWidth={1.5} />
              <span>0{i + 1}</span>
            </div>
            <h2>{step.title}</h2>
            <p>{step.description}</p>
          </article>
        ))}
      </div>
      <section className="settings-panel faq">
        <h2>你可能想知道</h2>
        <details open>
          <summary>支持哪些链接？</summary>
          <p>
            支持 v.douyin.com
            短链接、douyin.com/video/…、douyin.com/note/…、iesdouyin.com/share/video/…，以及包含这些链接的分享文案。支持单个视频和图片图集，图集可逐张导出或打包
            ZIP。可以在“作者作品”中分页查看主页并批量选择下载。暂不支持直播和
            TikTok。
          </p>
        </details>
        <details>
          <summary>为什么有时解析失败？</summary>
          <p>
            抖音可能要求验证码或有效登录状态，作品也可能被删除或设为私密。先确认原视频可以播放，再尝试
            <button onClick={onSettings}>更新 Cookie</button>。Cookie
            不保证绕过平台验证，遇到限制时请稍后再试。
          </p>
        </details>
        <details>
          <summary>作品保存在哪里？会上传到云端吗？</summary>
          <p>
            视频和图集保存在 Go 服务所在设备的数据目录下，下载记录保存在
            SQLite。你可以在设置中查看路径，也可以逐个导出或打包 ZIP
            保存到浏览器所在的设备。
          </p>
        </details>
        <details>
          <summary>关闭页面后，下载还会继续吗？</summary>
          <p>
            只要 Go
            服务保持运行，后台下载就会继续。服务重启后会重新排队未完成任务，从头下载；失败和已取消的任务可以手动重试。
          </p>
        </details>
        <details>
          <summary>如何备份我的收藏？</summary>
          <p>
            停止服务后，备份整个数据目录，其中包含 shiying.db、SQLite 辅助文件和
            videos 文件夹。请一并保管好已保存的 Cookie。
          </p>
        </details>
        <details>
          <summary>可以在其他设备访问吗？</summary>
          <p>
            默认仅本机可访问。监听局域网地址时需设置
            SHIYING_ACCESS_TOKEN，打开网页后输入访问口令；经公网访问时请自行配置
            HTTPS 反向代理。
          </p>
        </details>
      </section>
      <p className="usage-note">
        <ShieldCheck size={15} />
        请仅下载你有权保存的内容，并尊重创作者的作品与使用范围。
      </p>
    </>
  );
}

function Login({ onLogin }: { onLogin: () => Promise<void> }) {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setPending(true);
    try {
      await api("/session", "POST", { token });
      await onLogin();
    } catch (error) {
      setError(error instanceof Error ? error.message : "登录失败");
    } finally {
      setPending(false);
    }
  }
  return (
    <main className="login-page">
      <div className="login-card">
        <Logo />
        <span className="login-icon">
          <LockKeyhole size={28} strokeWidth={1.3} />
        </span>
        <h1>回到你的私人收藏室</h1>
        <p>输入服务启动时设置的访问口令。</p>
        <form onSubmit={submit}>
          <label htmlFor="access-token" className="form-label">
            访问口令
          </label>
          <input
            id="access-token"
            autoFocus
            type="password"
            value={token}
            onChange={(event) => {
              setToken(event.target.value);
              setError("");
            }}
            autoComplete="current-password"
            required
          />
          {error && (
            <p role="alert" className="inline-error">
              {error}
            </p>
          )}
          <Button size="lg" className="w-full" disabled={pending || !token}>
            {pending ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <ArrowRight />
            )}
            进入拾影
          </Button>
        </form>
        <span className="login-foot">
          <ShieldCheck size={13} />
          你的内容，只在你的设备上
        </span>
      </div>
    </main>
  );
}
