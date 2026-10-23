import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  Heart,
  ImageOff,
  Images,
  LoaderCircle,
  Maximize2,
  Shuffle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Task } from "@/lib/api";
import {
  browsableAlbums,
  firstAlbumImage,
  nextAlbumSession,
  syncAlbumSession,
  type AlbumSession,
} from "@/lib/random-albums";

function AlbumCover({ task }: { task: Task }) {
  const [state, setState] = useState<"loading" | "ready" | "failed">("loading");
  return (
    <div className="random-album-photo" aria-busy={state === "loading"}>
      {state !== "failed" && (
        <img
          src={firstAlbumImage(task)}
          alt={`${task.title || "未命名图集"}，第 1 张图片`}
          onLoad={() => setState("ready")}
          onError={() => setState("failed")}
        />
      )}
      {state === "loading" && (
        <div className="random-album-placeholder" role="status">
          <LoaderCircle className="animate-spin" aria-hidden="true" />
          <span>正在加载图片…</span>
        </div>
      )}
      {state === "failed" && (
        <div className="random-album-placeholder" role="status">
          <ImageOff size={32} aria-hidden="true" />
          <span>首页图片暂时无法显示</span>
          <small>可继续浏览，或进入详情查看其他图片。</small>
        </div>
      )}
    </div>
  );
}

export function RandomAlbumBrowser({
  tasks,
  loading,
  keyboardEnabled,
  viewerRef,
  onFavorite,
  onOpen,
  onExit,
}: {
  tasks: Task[];
  loading: boolean;
  keyboardEnabled: boolean;
  viewerRef: RefObject<HTMLElement | null>;
  onFavorite: (task: Task) => Promise<void>;
  onOpen: (task: Task) => void;
  onExit: () => void;
}) {
  const albums = useMemo(() => browsableAlbums(tasks), [tasks]);
  // Only membership changes should reconcile the order, not background polling
  // or changes to a work's favorite state.
  const membership = JSON.stringify(albums.map((task) => task.id).sort());
  const [session, setSession] = useState<AlbumSession>(() =>
    syncAlbumSession(
      { order: [], position: 0 },
      albums.map((task) => task.id),
    ),
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const savingRef = useRef(false);
  const browser = viewerRef;
  const current = albums.find(
    (task) => task.id === session.order[session.position],
  );
  useEffect(() => {
    const ids = JSON.parse(membership) as string[];
    setSession((value) => syncAlbumSession(value, ids));
  }, [membership]);
  useEffect(() => {
    browser.current?.focus({ preventScroll: true });
  }, [browser]);
  useEffect(() => {
    setError("");
  }, [current?.id]);

  const next = useCallback(() => {
    if (savingRef.current) return;
    setSession((value) =>
      nextAlbumSession(
        value,
        albums.map((task) => task.id),
      ),
    );
  }, [albums]);
  const previous = useCallback(() => {
    if (savingRef.current) return;
    setSession((value) => ({
      ...value,
      position: Math.max(0, value.position - 1),
    }));
  }, []);
  const open = useCallback(() => {
    if (current && !savingRef.current) onOpen(current);
  }, [current, onOpen]);
  const favorite = useCallback(async () => {
    if (!current || savingRef.current) return;
    savingRef.current = true;
    setSaving(true);
    setError("");
    try {
      // M always means "keep this work"; revisiting it must not unfavorite it.
      if (!current.favorite) await onFavorite(current);
      setSession((value) =>
        nextAlbumSession(
          value,
          albums.map((task) => task.id),
        ),
      );
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "收藏失败，请重试");
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }, [current, albums, onFavorite]);

  useEffect(() => {
    if (!keyboardEnabled) return;
    const keydown = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey ||
        event.isComposing
      )
        return;
      const target = event.target;
      if (
        target instanceof HTMLElement &&
        target.closest(
          'input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"]',
        )
      )
        return;
      const key = event.key.toLowerCase();
      if (![" ", "b", "m", "enter", "escape"].includes(key)) return;
      // Tab users can still activate focused controls with Space and Enter.
      if (
        (key === " " || key === "enter") &&
        target instanceof HTMLElement &&
        target.closest('button,a,[role="button"]')
      )
        return;
      event.preventDefault();
      if (event.repeat) return;
      if (key === "escape") onExit();
      else if (key === " ") next();
      else if (key === "b") previous();
      else if (key === "m") void favorite();
      else if (key === "enter") open();
    };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [keyboardEnabled, next, previous, favorite, open, onExit]);

  // Warm only the next cover rather than downloading the entire collection.
  const nextTask = albums.find(
    (task) => task.id === session.order[session.position + 1],
  );
  const nextImage = nextTask ? firstAlbumImage(nextTask) : undefined;
  useEffect(() => {
    if (nextImage) {
      const image = new Image();
      image.src = nextImage;
    }
  }, [nextImage]);

  return (
    <section
      ref={browser}
      className="random-album-browser"
      aria-label="照片随机浏览"
      tabIndex={-1}
      onClick={(event) => {
        // Pointer actions return focus to the viewer so the next shortcut works
        // immediately; keyboard users retain the native button focus order.
        if (event.detail > 0) browser.current?.focus({ preventScroll: true });
      }}
    >
      {current && <AlbumCover key={firstAlbumImage(current)} task={current} />}
      <header className="random-album-header">
        <Button variant="ghost" onClick={onExit} aria-keyshortcuts="Escape">
          <ArrowLeft aria-hidden="true" /> 返回作品库
        </Button>
        <h1>
          <Shuffle size={16} aria-hidden="true" /> 随机看图
        </h1>
        <span>{albums.length} 部图集</span>
      </header>
      {!current && (
        <div className="random-album-placeholder" role="status">
          {loading ? (
            <>
              <LoaderCircle className="animate-spin" aria-hidden="true" />
              <span>正在读取图集…</span>
            </>
          ) : (
            <>
              <Images size={40} aria-hidden="true" />
              <h2>还没有可浏览的图集</h2>
              <p>先保存图片集作品，再来随机发现喜欢。</p>
            </>
          )}
        </div>
      )}
      {current && (
        <div className="random-album-bottom">
          <div
            className="random-album-caption"
            aria-live="polite"
            aria-atomic="true"
          >
            <h2>{current.title || "未命名图集"}</h2>
            <p>
              @{current.author || "未知作者"} · 第 1 / {current.image_count} 张
              {current.favorite && (
                <span className="random-album-kept">
                  <Heart size={13} fill="currentColor" aria-hidden="true" />{" "}
                  已收藏
                </span>
              )}
            </p>
          </div>
          {error && (
            <p className="random-album-error" role="alert">
              {error}
            </p>
          )}
          <div
            className="random-album-controls"
            role="group"
            aria-label="随机浏览操作"
          >
            <Button
              variant="ghost"
              disabled={session.position === 0 || saving}
              onClick={previous}
              aria-keyshortcuts="B"
            >
              <ChevronLeft aria-hidden="true" />
              <span>上一张</span>
              <kbd>B</kbd>
            </Button>
            <Button
              variant="ghost"
              disabled={saving}
              onClick={() => void favorite()}
              aria-keyshortcuts="M"
            >
              {saving ? (
                <LoaderCircle className="animate-spin" aria-hidden="true" />
              ) : (
                <Heart aria-hidden="true" />
              )}
              <span>{saving ? "收藏中…" : "收藏并下一张"}</span>
              <kbd>M</kbd>
            </Button>
            <Button
              variant="ghost"
              disabled={saving}
              onClick={open}
              aria-keyshortcuts="Enter"
            >
              <Maximize2 aria-hidden="true" />
              <span>作品详情</span>
              <kbd>Enter</kbd>
            </Button>
            <Button
              variant="ghost"
              disabled={albums.length < 2 || saving}
              onClick={next}
              aria-keyshortcuts="Space"
            >
              <span>下一张</span>
              <kbd>空格</kbd>
              <ChevronRight aria-hidden="true" />
            </Button>
          </div>
          <p className="random-album-hint">
            随机展示每部图集的首页 · Esc 返回
            {albums.length === 1 ? " · 当前只有一部图集" : ""}
          </p>
        </div>
      )}
    </section>
  );
}
