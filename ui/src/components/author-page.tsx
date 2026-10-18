import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import {
  ArrowRight,
  Check,
  CircleAlert,
  Download,
  ExternalLink,
  Images,
  LoaderCircle,
  RefreshCw,
  Search,
  UserRound,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  api,
  ApiError,
  type AccountSession,
  type AuthorProfile,
  type AuthorPost,
  type AuthorPageData,
  type BatchResult,
  type Preview,
  type Task,
} from "@/lib/api";
import { cn, duration } from "@/lib/utils";

export type AuthorTarget = { text: string; nonce: number };
const stateLabels: Record<Task["status"], string> = {
  queued: "已在队列",
  downloading: "下载中",
  completed: "已保存",
  partial: "部分完成",
  failed: "下载失败",
  canceled: "已取消",
};
const aborted = (e: unknown) => e instanceof Error && e.name === "AbortError";

export function AuthorPage({
  active,
  target,
  tasks,
  onPreview,
  onTask,
  onTasks,
  onQueued,
  personal,
  onPersonalInvalid,
}: {
  active: boolean;
  target?: AuthorTarget;
  tasks: Task[];
  onPreview: (preview: Preview) => void;
  onTask: (task: Task) => void;
  onTasks: () => void;
  onQueued: () => Promise<void>;
  personal?: { session: AccountSession; feed: "likes" | "bookmarks" };
  onPersonalInvalid?: () => void;
}) {
  const [input, setInput] = useState("");
  const [author, setAuthor] = useState<AuthorProfile>();
  const [items, setItems] = useState<AuthorPost[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [cursor, setCursor] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [busy, setBusy] = useState<"" | "resolve" | "more" | "all">("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const lastPersonal = useRef("");
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const [visibleCount, setVisibleCount] = useState(80);
  useEffect(() => setVisibleCount(80), [filter, search]);
  const [submitting, setSubmitting] = useState(false);
  const [batchProgress, setBatchProgress] = useState({ done: 0, total: 0 });
  const [batchSummary, setBatchSummary] = useState("");
  const [batchFailures, setBatchFailures] = useState<BatchResult[]>([]);
  const [submitted, setSubmitted] = useState<Record<string, string>>({});
  const workController = useRef<AbortController | null>(null);
  const batchController = useRef<AbortController | null>(null);
  const lastTarget = useRef<number | undefined>(undefined);
  const cursorHistory = useRef<Set<string>>(new Set());
  const batchInFlight = useRef(false);
  const taskMap = useMemo(
    () => new Map(tasks.map((task) => [task.source_url, task])),
    [tasks],
  );
  useEffect(() => {
    const known = new Set(tasks.map((task) => task.id));
    setSubmitted((previous) => {
      const entries = Object.entries(previous).filter(
        ([, id]) => !known.has(id),
      );
      return entries.length === Object.keys(previous).length
        ? previous
        : Object.fromEntries(entries);
    });
  }, [tasks]);
  const taskOf = (item: AuthorPost) => taskMap.get(item.content.source_url);
  const exists = (item: AuthorPost) =>
    !!taskOf(item) || !!submitted[item.content.id];
  const selectable = (item: AuthorPost) => item.downloadable && !exists(item);
  const filtered = useMemo(
    () =>
      items.filter(
        (item) =>
          (filter === "all" || item.content.kind === filter) &&
          `${item.content.title} ${item.content.description}`
            .toLocaleLowerCase()
            .includes(search.toLocaleLowerCase()),
      ),
    [items, filter, search],
  );
  const available = filtered.filter(selectable);
  const selectedItems = items.filter(
    (item) => selected.has(item.content.id) && selectable(item),
  );
  const allSelected =
    available.length > 0 &&
    available.every((item) => selected.has(item.content.id));

  const appendPage = (page: AuthorPageData) => {
    setItems((previous) => {
      const rows = new Map(previous.map((item) => [item.content.id, item]));
      for (const item of page.items) rows.set(item.content.id, item);
      return [...rows.values()];
    });
    setCursor(page.cursor);
    setHasMore(page.has_more);
    setNotice(page.notice || "");
  };
  async function requestPage(
    profile: AuthorProfile,
    next: string,
    signal: AbortSignal,
  ) {
    const page = await api<AuthorPageData>(
      personal
        ? `/account/${personal.feed}?scope=${encodeURIComponent(personal.session.scope)}${next ? `&cursor=${encodeURIComponent(next)}` : ""}`
        : `/authors/${encodeURIComponent(profile.uid)}/posts${next ? `?cursor=${encodeURIComponent(next)}` : ""}`,
      "GET",
      undefined,
      signal,
    );
    if (personal && page.account_scope !== personal.session.scope)
      throw new ApiError(
        "Cookie 账号已变更，请重新读取账号",
        409,
        "ACCOUNT_CHANGED",
      );
    if (
      page.has_more &&
      (!page.cursor ||
        page.cursor === next ||
        cursorHistory.current.has(page.cursor))
    )
      throw new Error(
        "作品分页游标重复，已停止加载。已取得的作品仍可选择下载。",
      );
    if (page.has_more) cursorHistory.current.add(page.cursor);
    return page;
  }
  function invalidatePersonal(e: unknown) {
    if (
      personal &&
      e instanceof ApiError &&
      [
        "ACCOUNT_CHANGED",
        "ACCOUNT_MISMATCH",
        "ACCOUNT_UNVERIFIED",
        "COOKIE_REQUIRED",
      ].includes(e.code || "")
    )
      onPersonalInvalid?.();
  }
  useEffect(() => {
    if (!active || !personal) return;
    const key = personal.session.scope + ":" + personal.feed;
    if (lastPersonal.current === key) return;
    lastPersonal.current = key;
    const controller = new AbortController();
    workController.current?.abort();
    workController.current = controller;
    setBusy("resolve");
    setError("");
    setItems([]);
    setAuthor(personal.session.profile);
    setSelected(new Set());
    setHasMore(true);
    setCursor("");
    cursorHistory.current.clear();
    let committed = false;
    void requestPage(personal.session.profile, "", controller.signal)
      .then((page) => {
        if (!controller.signal.aborted) {
          committed = true;
          appendPage(page);
        }
      })
      .catch((e) => {
        if (!controller.signal.aborted) {
          invalidatePersonal(e);
          setError(e instanceof Error ? e.message : "读取个人列表失败");
        }
      })
      .finally(() => {
        if (workController.current === controller) setBusy("");
      });
    return () => {
      if (!committed && lastPersonal.current === key) lastPersonal.current = "";
      controller.abort();
    };
  }, [active, personal?.session.scope, personal?.feed]);
  async function resolve(text: string) {
    if (!text.trim() || batchInFlight.current) return;
    workController.current?.abort();
    const controller = new AbortController();
    workController.current = controller;
    setBusy("resolve");
    setError("");
    setBatchSummary("");
    setBatchFailures([]);
    setAuthor(undefined);
    setItems([]);
    setSelected(new Set());
    setSubmitted({});
    setCursor("");
    setHasMore(false);
    setSearch("");
    setFilter("all");
    cursorHistory.current.clear();
    try {
      const profile = await api<AuthorProfile>(
        "/authors/resolve",
        "POST",
        { text },
        controller.signal,
      );
      if (controller.signal.aborted) return;
      setAuthor(profile);
      setHasMore(true);
      const page = await requestPage(profile, "", controller.signal);
      if (!controller.signal.aborted) appendPage(page);
    } catch (e) {
      if (!controller.signal.aborted) invalidatePersonal(e);
      if (!controller.signal.aborted)
        setError(e instanceof Error ? e.message : "读取作者作品失败");
    } finally {
      if (workController.current === controller) setBusy("");
    }
  }
  useEffect(() => {
    if (
      active &&
      target &&
      !batchInFlight.current &&
      lastTarget.current !== target.nonce
    ) {
      lastTarget.current = target.nonce;
      setInput(target.text);
      void resolve(target.text);
    }
  }, [active, target, submitting]);
  useEffect(
    () => () => {
      workController.current?.abort();
      batchController.current?.abort();
    },
    [],
  );
  useEffect(() => {
    if (!active) workController.current?.abort();
  }, [active]);
  async function loadMore(all = false) {
    if (!author || busy || !hasMore || submitting) return;
    const controller = new AbortController();
    workController.current = controller;
    setBusy(all ? "all" : "more");
    setError("");
    let next = cursor;
    try {
      do {
        const page = await requestPage(author, next, controller.signal);
        if (controller.signal.aborted) break;
        appendPage(page);
        next = page.cursor;
        if (!all || !page.has_more) break;
        await new Promise<void>((done, reject) => {
          const abort = () => {
            clearTimeout(timer);
            reject(new DOMException("Aborted", "AbortError"));
          };
          const timer = setTimeout(() => {
            controller.signal.removeEventListener("abort", abort);
            done();
          }, 500);
          controller.signal.addEventListener("abort", abort, { once: true });
          if (controller.signal.aborted) abort();
        });
      } while (!controller.signal.aborted);
    } catch (e) {
      if (!controller.signal.aborted) invalidatePersonal(e);
      if (!controller.signal.aborted)
        setError(
          e instanceof Error ? e.message : "加载失败，已加载的作品已保留",
        );
    } finally {
      if (workController.current === controller) setBusy("");
    }
  }
  function toggle(id: string) {
    setSelected((previous) => {
      const next = new Set(previous);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  function toggleAll() {
    setSelected((previous) => {
      const next = new Set(previous);
      for (const item of available) {
        if (allSelected) next.delete(item.content.id);
        else next.add(item.content.id);
      }
      return next;
    });
  }
  async function submit() {
    if (batchInFlight.current || !selectedItems.length) return;
    batchInFlight.current = true;
    setSubmitting(true);
    setBatchSummary("");
    setBatchFailures([]);
    setError("");
    const controller = new AbortController();
    batchController.current = controller;
    const snapshot = [...selectedItems];
    setBatchProgress({ done: 0, total: snapshot.length });
    let added = 0,
      existing = 0,
      processed = 0;
    const failures: BatchResult[] = [];
    try {
      for (let offset = 0; offset < snapshot.length; offset += 100) {
        if (controller.signal.aborted) break;
        const group = snapshot.slice(offset, offset + 100);
        // Finish an in-flight request before stopping: its results tell us which
        // tasks already exist, so cancellation never reports an unknown outcome.
        const response = await api<{ results: BatchResult[] }>(
          "/tasks/batch",
          "POST",
          { preview_ids: group.map((item) => item.preview_id) },
        );
        const successful = new Set<string>();
        const accepted: Record<string, string> = {};
        const byPreview = new Map(group.map((item) => [item.preview_id, item]));
        for (const result of response.results) {
          const item = byPreview.get(result.preview_id);
          if (result.state === "failed") failures.push(result);
          else {
            result.state === "added" ? added++ : existing++;
            if (item) {
              successful.add(item.content.id);
              accepted[item.content.id] = result.task_id || "queued";
            }
          }
        }
        setSubmitted((previous) => ({ ...previous, ...accepted }));
        setSelected(
          (previous) =>
            new Set([...previous].filter((id) => !successful.has(id))),
        );
        processed += group.length;
        setBatchProgress({ done: processed, total: snapshot.length });
      }
    } catch (e) {
      if (!aborted(e))
        setError(
          e instanceof Error
            ? `${e.message}；未确认的作品仍保留选中，重试时会自动去重。`
            : "批量提交中断，可重试；服务会自动去重",
        );
    } finally {
      setBatchFailures(failures);
      setBatchSummary(
        `新增 ${added} 个任务，已有 ${existing} 个，失败 ${failures.length} 个${processed < snapshot.length ? `，未提交 ${snapshot.length - processed} 个` : ""}。`,
      );
      batchInFlight.current = false;
      setSubmitting(false);
      await onQueued();
    }
  }
  function submitSearch(event: FormEvent) {
    event.preventDefault();
    void resolve(input);
  }
  const disabled = !!busy || submitting;
  return (
    <section
      className="author-page"
      hidden={!active}
      aria-label={
        personal
          ? personal.feed === "likes"
            ? "Cookie 账号的喜欢作品"
            : "Cookie 账号的收藏作品"
          : "作者全部作品"
      }
    >
      {!personal && (
        <>
          <div className="page-heading">
            <div>
              <div className="eyebrow">MORE FROM YOUR FAVORITE CREATORS</div>
              <h1>作者作品</h1>
              <p>找到喜欢的作者，把值得留下的作品一起收藏。</p>
            </div>
          </div>
          <form className="author-search" onSubmit={submitSearch}>
            <label htmlFor="author-link">作者主页或作品链接</label>
            <div>
              <input
                id="author-link"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="https://www.douyin.com/user/… 或一条作品分享链接"
                disabled={disabled}
              />
              <Button disabled={!input.trim() || disabled} type="submit">
                {busy === "resolve" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Search />
                )}
                查看作品
              </Button>
            </div>
            <p>可直接粘贴完整分享文案；需要在偏好设置中配置抖音 Cookie。</p>
          </form>
        </>
      )}
      {personal && (
        <div className="personal-list-heading">
          <h2>{personal.feed === "likes" ? "我的喜欢" : "我的收藏"}</h2>
          <span aria-live="polite">
            已加载 {items.length} 个作品
            {!hasMore && busy !== "resolve" ? " · 已到末页" : ""}
          </span>
        </div>
      )}
      {notice && (
        <p className="personal-notice" role="status">
          {notice}
        </p>
      )}
      {error && (
        <div role="alert" className="inline-error">
          <CircleAlert size={17} />
          <span>{error}</span>
        </div>
      )}
      {busy === "resolve" && (
        <div role="status" className="author-loading">
          <LoaderCircle className="animate-spin" />
          {personal ? "正在读取当前账号的作品…" : "正在读取作者与作品…"}
          <Button
            variant="ghost"
            onClick={() => workController.current?.abort()}
          >
            取消
          </Button>
        </div>
      )}
      {author && (
        <>
          {!personal && (
            <header className="author-profile">
              <div className="author-avatar">
                {author.avatar ? (
                  <img
                    src={author.avatar}
                    alt=""
                    onError={(e) => {
                      e.currentTarget.style.display = "none";
                    }}
                  />
                ) : (
                  <UserRound />
                )}
              </div>
              <div>
                <h2>{author.nickname || "抖音作者"}</h2>
                <p>{author.signature || "公开作品，妥善收藏。"}</p>
                <span>
                  {author.works_count !== null
                    ? `主页标记 ${author.works_count} 个作品 · `
                    : ""}
                  已加载 {items.length} 个
                  {!hasMore && busy !== "resolve" ? " · 已加载到末页" : ""}
                </span>
              </div>
              <Button variant="outline" size="sm" asChild>
                <a href={author.web_url} target="_blank" rel="noreferrer">
                  <ExternalLink />
                  抖音主页
                </a>
              </Button>
            </header>
          )}
          <div className="author-toolbar">
            <div className="author-filters" role="group" aria-label="作品类型">
              {[
                ["all", "全部作品"],
                ["video", "视频"],
                ["image_album", "图集"],
              ].map(([value, label]) => (
                <Button
                  key={value}
                  variant={filter === value ? "secondary" : "ghost"}
                  size="sm"
                  aria-pressed={filter === value}
                  onClick={() => setFilter(value)}
                >
                  {label}
                </Button>
              ))}
            </div>
            <label className="author-filter-search">
              <Search size={15} />
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索已加载的作品"
                aria-label="搜索已加载的作品"
              />
            </label>
          </div>
          <div className="author-selection-tools">
            <label>
              <input
                type="checkbox"
                checked={allSelected}
                disabled={!available.length || submitting}
                onChange={toggleAll}
              />
              全选当前结果 <span>（可选 {available.length} 个）</span>
            </label>
            <Button
              variant="ghost"
              size="sm"
              disabled={!selected.size || submitting}
              onClick={() => setSelected(new Set())}
            >
              清空选择
            </Button>
          </div>
          {batchSummary && (
            <div className="batch-summary" role="status">
              <Check size={17} />
              <span>{batchSummary}</span>
              <Button variant="ghost" size="sm" onClick={onTasks}>
                查看下载任务
                <ArrowRight />
              </Button>
            </div>
          )}
          {batchFailures.length > 0 && (
            <details className="stored-files">
              <summary>
                {batchFailures.length} 个作品未提交成功，仍保留选中，可重试
              </summary>
              <ul>
                {batchFailures.map((f) => (
                  <li key={f.preview_id}>
                    <span>
                      {f.title || "作品"}
                      <small>{f.error}</small>
                    </span>
                  </li>
                ))}
              </ul>
            </details>
          )}
          {!filtered.length && busy !== "resolve" && (
            <div className="author-empty">
              <Search size={26} />
              <h3>
                {items.length ? "没有匹配的作品" : "当前没有可展示的作品"}
              </h3>
              <p>
                {items.length
                  ? "试试其他关键词或作品类型。"
                  : "平台未返回公开作品。作品数量可能包含私密、删除或当前不可见的内容。"}
              </p>
            </div>
          )}
          <div className="author-grid" aria-busy={busy === "resolve"}>
            {filtered.slice(0, visibleCount).map((item) => {
              const c = item.content;
              const task = taskOf(item);
              const added = exists(item);
              const checked = selected.has(c.id) && !added;
              const canSelect = selectable(item);
              return (
                <article
                  key={c.id}
                  className={cn("author-work-card", checked && "is-selected")}
                >
                  <div className="author-work-cover">
                    <button
                      aria-label={`预览 ${c.title || "未命名作品"}`}
                      onClick={() =>
                        task
                          ? onTask(task)
                          : onPreview({
                              preview_id: item.preview_id,
                              video: c,
                              account_scope: personal?.session.scope,
                            })
                      }
                    >
                      <img src={c.cover} alt="" loading="lazy" />
                      <span className="video-duration">
                        {c.kind === "image_album" ? (
                          <>
                            <Images size={12} />
                            {c.image_count} 张
                          </>
                        ) : (
                          duration(c.duration)
                        )}
                      </span>
                    </button>
                    <label
                      className="author-work-select"
                      title={
                        !canSelect
                          ? added
                            ? "已有下载任务，可到下载任务中查看或重试"
                            : item.reason
                          : "选择作品"
                      }
                    >
                      <input
                        type="checkbox"
                        aria-label={`选择 ${c.title || "未命名作品"}`}
                        checked={checked}
                        disabled={!canSelect || submitting}
                        onChange={() => toggle(c.id)}
                      />
                    </label>
                    {added && (
                      <span className="author-work-state">
                        <Check size={12} />
                        {task ? stateLabels[task.status] : "已在队列"}
                      </span>
                    )}
                  </div>
                  <div className="author-work-info">
                    <h3 title={c.title}>{c.title || "未命名作品"}</h3>
                    <div>
                      <span>
                        {c.kind === "image_album" ? "图片图集" : "视频"}
                      </span>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          task
                            ? onTask(task)
                            : onPreview({
                                preview_id: item.preview_id,
                                video: c,
                                account_scope: personal?.session.scope,
                              })
                        }
                      >
                        {task ? "查看任务" : "预览"}
                        <ArrowRight size={12} />
                      </Button>
                    </div>
                    {!item.downloadable && (
                      <p className="task-error">{item.reason}</p>
                    )}
                  </div>
                </article>
              );
            })}
          </div>
          {filtered.length > visibleCount && (
            <div className="author-visible-more">
              <p>
                当前显示 {visibleCount}/{filtered.length}{" "}
                个结果；全选包含全部已加载的匹配作品。
              </p>
              <Button
                variant="outline"
                onClick={() => setVisibleCount((count) => count + 80)}
              >
                显示更多已加载作品
              </Button>
            </div>
          )}
          <div className="author-pagination">
            <p aria-live="polite">
              已加载 {items.length} 个作品
              {hasMore ? "，还有更多作品" : "，已到平台返回的末页"}。已选择{" "}
              {selectedItems.length} 个。
            </p>
            <div>
              {busy === "all" ? (
                <Button
                  variant="outline"
                  onClick={() => workController.current?.abort()}
                >
                  <X />
                  停止加载，保留已加载作品
                </Button>
              ) : (
                <>
                  <Button
                    variant="outline"
                    disabled={!hasMore || disabled}
                    onClick={() => void loadMore()}
                  >
                    {busy === "more" ? (
                      <LoaderCircle className="animate-spin" />
                    ) : (
                      <RefreshCw />
                    )}
                    加载更多
                  </Button>
                  <Button
                    variant="outline"
                    disabled={!hasMore || disabled}
                    onClick={() => void loadMore(true)}
                  >
                    加载全部作品
                  </Button>
                </>
              )}
            </div>
            <small>
              “全部”指平台当前返回的可见作品；平台标记数量可能包含当前不可见的作品。
            </small>
          </div>
          <div className="author-batch-bar">
            <div aria-live="polite">
              <strong>{selectedItems.length}</strong>
              <span>个作品已选择</span>
              {submitting && (
                <small>
                  正在提交 {batchProgress.done}/{batchProgress.total}
                </small>
              )}
            </div>
            <div>
              {submitting ? (
                <Button
                  variant="outline"
                  onClick={() => batchController.current?.abort()}
                >
                  停止后续提交
                </Button>
              ) : (
                <Button variant="ghost" onClick={onTasks}>
                  下载任务
                  <ArrowRight />
                </Button>
              )}
              <Button
                disabled={!selectedItems.length || submitting || !!busy}
                onClick={() => void submit()}
              >
                {submitting ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Download />
                )}
                {submitting
                  ? "正在加入队列…"
                  : `下载所选${selectedItems.length ? ` (${selectedItems.length})` : ""}`}
              </Button>
            </div>
          </div>
        </>
      )}
      {!author && !busy && !error && (
        <div className="author-empty">
          <UserRound size={36} />
          <h3>从喜欢的作者开始</h3>
          <p>粘贴作者主页，或在作品详情中点击“查看作者全部作品”。</p>
        </div>
      )}
    </section>
  );
}
