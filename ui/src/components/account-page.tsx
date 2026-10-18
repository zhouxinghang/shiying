import { useEffect, useRef, useState } from "react";
import {
  Bookmark,
  CircleAlert,
  Heart,
  LoaderCircle,
  RefreshCw,
  ShieldCheck,
  UserRound,
} from "lucide-react";
import { AuthorPage } from "@/components/author-page";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, type AccountSession, type Preview, type Task } from "@/lib/api";

export function AccountPage({
  active,
  cookieRevision,
  cookieConfigured,
  tasks,
  onPreview,
  onTask,
  onTasks,
  onQueued,
  onSettings,
}: {
  active: boolean;
  cookieRevision: string;
  cookieConfigured: boolean;
  tasks: Task[];
  onPreview: (p: Preview) => void;
  onTask: (task: Task) => void;
  onTasks: () => void;
  onQueued: () => Promise<void>;
  onSettings: () => void;
}) {
  const [session, setSession] = useState<AccountSession>();
  const [feed, setFeed] = useState<"likes" | "bookmarks">("likes");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  const attempted = useRef("");
  const current = session?.scope === cookieRevision ? session : undefined;
  async function connect() {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setError("");
    setSession(undefined);
    try {
      const result = await api<AccountSession>(
        "/account",
        "GET",
        undefined,
        request.signal,
      );
      if (!request.signal.aborted) {
        attempted.current = result.scope;
        setSession(result);
      }
    } catch (e) {
      if (!request.signal.aborted)
        setError(e instanceof Error ? e.message : "无法读取 Cookie 登录账号");
    } finally {
      if (controller.current === request) setBusy(false);
    }
  }
  useEffect(() => {
    if (
      active &&
      cookieConfigured &&
      cookieRevision &&
      attempted.current !== cookieRevision
    ) {
      attempted.current = cookieRevision;
      void connect();
    }
  }, [active, cookieRevision, cookieConfigured]);
  useEffect(
    () => () => {
      attempted.current = "";
      controller.current?.abort();
    },
    [],
  );
  function invalidate() {
    setSession(undefined);
    setError(
      "当前账号状态已变化，请重新读取 Cookie 账号；如果登录过期，请先更新 Cookie。",
    );
    void onQueued();
  }
  return (
    <section
      className="account-page"
      hidden={!active}
      aria-label="Cookie 登录账号的喜欢和收藏"
    >
      <div className="page-heading">
        <div>
          <div className="eyebrow">YOUR DOUYIN, YOUR COLLECTION</div>
          <h1>抖音账号</h1>
          <p>读取已配置 Cookie 对应账号的喜欢与收藏，选择后保存到本地。</p>
        </div>
        <Button
          variant="outline"
          disabled={busy || !cookieConfigured}
          onClick={() => void connect()}
        >
          {busy ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
          {current ? "重新读取账号" : "读取 Cookie 账号"}
        </Button>
      </div>
      {!cookieConfigured && (
        <div className="inline-error">
          <CircleAlert size={17} />
          <span>尚未配置抖音登录 Cookie，无法确定本人账号。</span>
          <Button variant="outline" size="sm" onClick={onSettings}>
            前往配置
          </Button>
        </div>
      )}
      {error && (
        <div role="alert" className="inline-error">
          <CircleAlert size={17} />
          <span>{error}</span>
          <Button variant="outline" size="sm" onClick={onSettings}>
            更新 Cookie
          </Button>
        </div>
      )}
      {busy && (
        <div className="author-loading" role="status">
          <LoaderCircle className="animate-spin" />
          正在核对 Cookie 的登录账号…
        </div>
      )}
      {current && (
        <>
          <header className="account-identity">
            <div className="author-avatar">
              {current.profile.avatar ? (
                <img src={current.profile.avatar} alt="" />
              ) : (
                <UserRound />
              )}
            </div>
            <div>
              <h2>{current.profile.nickname || "抖音账号"}</h2>
              <p>
                <ShieldCheck size={14} />
                已通过抖音本人资料接口核对
              </p>
              <small>
                {current.likes_count !== null
                  ? `平台标记喜欢 ${current.likes_count} 个`
                  : "喜欢数量未提供"}
                {current.bookmarks_count !== null
                  ? ` · 收藏 ${current.bookmarks_count} 个`
                  : ""}
              </small>
            </div>
          </header>
          <Tabs
            value={feed}
            onValueChange={(value) => setFeed(value as "likes" | "bookmarks")}
          >
            <TabsList className="account-tabs">
              <TabsTrigger value="likes">
                <Heart size={15} />
                我的喜欢
              </TabsTrigger>
              <TabsTrigger value="bookmarks">
                <Bookmark size={15} />
                我的收藏
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <p className="account-explainer">
            这里的喜欢和收藏来自抖音账号；侧栏“本地收藏”保存的是本应用中标记的作品。全部加载只读取列表，点击“下载所选”后才会保存媒体文件。
          </p>
          {(["likes", "bookmarks"] as const).map((kind) => (
            <AuthorPage
              key={`${current.scope}-${kind}`}
              active={active && feed === kind}
              tasks={tasks}
              personal={{ session: current, feed: kind }}
              onPersonalInvalid={invalidate}
              onPreview={onPreview}
              onTask={onTask}
              onTasks={onTasks}
              onQueued={onQueued}
            />
          ))}
        </>
      )}
    </section>
  );
}
