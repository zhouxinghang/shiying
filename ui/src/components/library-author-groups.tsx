import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  ArrowLeft,
  ArrowRight,
  ChevronDown,
  LayoutGrid,
  List,
  LoaderCircle,
  RefreshCw,
  Search,
  UserRound,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Task } from "@/lib/api";
import type { AuthorGroup } from "@/lib/library";
import {
  useAuthorProfiles,
  type AuthorProfileState,
} from "@/lib/use-author-profiles";

const selectedAuthor = () =>
  new URLSearchParams(location.hash.split("?")[1]).get("author") || "";
function Avatar({ src, name }: { src?: string; name: string }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [src]);
  return (
    <span className="library-profile-avatar">
      {src && !failed ? (
        <img
          src={src}
          alt={`${name}的头像`}
          loading="lazy"
          onError={() => setFailed(true)}
        />
      ) : (
        <UserRound size={28} aria-hidden="true" />
      )}
    </span>
  );
}
function Biography({
  state,
  missing = false,
}: {
  state?: AuthorProfileState;
  missing?: boolean;
}) {
  const profile = state?.data?.profile;
  return (
    <>
      <p className="library-profile-signature">
        {profile
          ? profile.signature || "作者还没有填写签名"
          : missing
            ? "旧记录缺少作者标识，暂无法读取详细资料"
            : state?.error
              ? "签名暂未获取"
              : "正在读取作者签名…"}
      </p>
      <span className="library-profile-followers">
        {profile?.follower_count == null
          ? "粉丝数未获取"
          : `${new Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 1 }).format(profile.follower_count)} 粉丝`}
      </span>
    </>
  );
}
function AuthorCard({
  group,
  state,
  href,
  request,
  retry,
  onEnter,
}: {
  group: AuthorGroup;
  state?: AuthorProfileState;
  href: string;
  request: (uid: string) => void;
  retry: (uid: string) => void;
  onEnter: () => void;
}) {
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    if (!group.uid || !ref.current) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          request(group.uid);
          observer.disconnect();
        }
      },
      { rootMargin: "200px" },
    );
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, [group.uid, request]);
  const name = state?.data?.profile.nickname || group.name;
  return (
    <article ref={ref} className="library-profile-card">
      <a href={href} onClick={onEnter} className="library-profile-link">
        <Avatar src={state?.data?.profile.avatar} name={name} />
        <h2>{name}</h2>
        {group.uid.startsWith("MS4") ? (
          <Biography state={state} />
        ) : (
          <>
            <p className="library-profile-signature">
              旧记录缺少作者标识，暂无法读取详细资料
            </p>
            <span className="library-profile-followers">粉丝数未获取</span>
          </>
        )}
        <span className="library-profile-open">
          <span>{group.tasks.length} 个本地作品</span>
          <span>
            查看作品 <ArrowRight size={14} aria-hidden="true" />
          </span>
        </span>
      </a>
      {state?.loading && (
        <p className="library-profile-status">
          <LoaderCircle size={13} className="animate-spin" aria-hidden="true" />{" "}
          正在更新资料
        </p>
      )}
      {state?.error && !state.loading && (
        <div className="library-profile-error">
          <span>{state.data ? "正在显示缓存资料" : "资料暂不可用"}</span>
          <button
            onClick={() => retry(group.uid)}
            aria-label={`重试 ${name} 的作者资料`}
          >
            重试
          </button>
        </div>
      )}
    </article>
  );
}

export function LibraryAuthorGroups({
  groups,
  page,
  layout,
  setLayout,
  order,
  setOrder,
  children,
}: {
  groups: AuthorGroup[];
  page: "library" | "favorites";
  layout: "grid" | "list";
  setLayout: (value: "grid" | "list") => void;
  order: string;
  setOrder: (value: string) => void;
  children: (tasks: Task[]) => ReactNode;
}) {
  const [key, setKey] = useState(selectedAuthor);
  const [search, setSearch] = useState("");
  const [workSearch, setWorkSearch] = useState("");
  const scroll = useRef(0);
  const heading = useRef<HTMLHeadingElement>(null);
  const previousKey = useRef(key);
  const { entries, notice, request, retry } = useAuthorProfiles();
  useEffect(() => {
    const update = () => setKey(selectedAuthor());
    window.addEventListener("hashchange", update);
    return () => window.removeEventListener("hashchange", update);
  }, []);
  const selected = groups.find((group) => group.key === key);
  useEffect(() => {
    if (key !== previousKey.current) {
      setWorkSearch("");
      window.scrollTo({ top: key ? 0 : scroll.current, behavior: "instant" });
      if (key) heading.current?.focus({ preventScroll: true });
      previousKey.current = key;
    }
    if (selected?.uid) request(selected.uid);
  }, [key, selected?.uid, request]);
  const profileState = selected ? entries[selected.uid] : undefined;
  const profile = profileState?.data?.profile;
  const filtered = groups.filter((group) => {
    const p = entries[group.uid]?.data?.profile;
    return `${group.name} ${p?.nickname || ""} ${p?.signature || ""} ${group.tasks.map((task) => task.title).join(" ")}`
      .toLocaleLowerCase()
      .includes(search.trim().toLocaleLowerCase());
  });
  const works =
    selected?.tasks.filter((task) =>
      `${task.title} ${task.author}`
        .toLocaleLowerCase()
        .includes(workSearch.toLocaleLowerCase()),
    ) || [];
  const name = profile?.nickname || selected?.name || "作者";
  return (
    <div className="library-author-directory">
      {key && (
        <Button variant="ghost" className="library-directory-back" asChild>
          <a href={`#${page}`}>
            <ArrowLeft aria-hidden="true" />
            返回作者列表
          </a>
        </Button>
      )}
      {key && !selected ? (
        <div className="empty-state">
          <h3>该作者暂无本地作品</h3>
          <p>作品可能已被删除或已移出本地收藏。</p>
        </div>
      ) : (
        <>
          {selected && (
            <header className="library-profile-detail">
              <Avatar src={profile?.avatar} name={name} />
              <div>
                <h2 ref={heading} tabIndex={-1}>
                  {name}
                </h2>
                <Biography
                  state={profileState}
                  missing={!selected.uid.startsWith("MS4")}
                />
                <small>
                  {selected.tasks.length} 个已保存作品
                  {profileState?.data
                    ? ` · 资料更新于 ${new Date(profileState.data.fetched_at).toLocaleDateString("zh-CN")}`
                    : ""}
                </small>
              </div>
              {selected.uid.startsWith("MS4") && (
                <Button
                  variant="outline"
                  disabled={profileState?.loading}
                  onClick={() => retry(selected.uid)}
                >
                  {profileState?.loading ? (
                    <LoaderCircle className="animate-spin" />
                  ) : (
                    <RefreshCw />
                  )}
                  更新作者资料
                </Button>
              )}
            </header>
          )}
          {notice && (
            <p className="library-directory-notice" role="status">
              {notice} <a href="#settings">偏好设置</a>
            </p>
          )}
          {selected && profileState?.error && (
            <p className="library-directory-notice" role="status">
              {profileState.error}；本地作品仍可查看。
            </p>
          )}
          <div className="library-filters">
            <label className="search-box">
              <Search size={16} aria-hidden="true" />
              <input
                aria-label={
                  selected ? "搜索该作者的本地作品" : "搜索作者或已保存作品"
                }
                placeholder={
                  selected ? "搜索该作者的作品…" : "搜索作者或已保存作品…"
                }
                value={selected ? workSearch : search}
                onChange={(e) =>
                  selected
                    ? setWorkSearch(e.target.value)
                    : setSearch(e.target.value)
                }
              />
              {(selected ? workSearch : search) && (
                <button
                  aria-label="清空搜索"
                  onClick={() => (selected ? setWorkSearch("") : setSearch(""))}
                >
                  <X size={14} />
                </button>
              )}
            </label>
            <div className="filter-actions">
              <label className="sort-select">
                <select
                  aria-label="排序方式"
                  value={order}
                  onChange={(e) => setOrder(e.target.value)}
                >
                  <option value="new">最新添加</option>
                  <option value="old">最早添加</option>
                </select>
                <ChevronDown size={14} aria-hidden="true" />
              </label>
              {selected && (
                <div className="layout-switch">
                  <button
                    aria-label="网格视图"
                    aria-pressed={layout === "grid"}
                    className={layout === "grid" ? "selected" : ""}
                    onClick={() => setLayout("grid")}
                  >
                    <LayoutGrid size={16} />
                  </button>
                  <button
                    aria-label="列表视图"
                    aria-pressed={layout === "list"}
                    className={layout === "list" ? "selected" : ""}
                    onClick={() => setLayout("list")}
                  >
                    <List size={17} />
                  </button>
                </div>
              )}
            </div>
          </div>
          <p className="library-directory-count" role="status">
            {selected
              ? `已保存 ${works.length} 个作品`
              : `共 ${filtered.length} 位作者 · 点击作者查看本地作品`}
          </p>
          {selected ? (
            works.length ? (
              children(works)
            ) : (
              <div className="empty-state">
                <h3>没有匹配的作品</h3>
                <p>试试其他关键词。</p>
              </div>
            )
          ) : filtered.length ? (
            <div className="library-profile-grid">
              {filtered.map((group) => (
                <AuthorCard
                  key={group.key}
                  group={group}
                  state={entries[group.uid]}
                  href={`#${page}?author=${encodeURIComponent(group.key)}`}
                  request={request}
                  retry={retry}
                  onEnter={() => {
                    scroll.current = window.scrollY;
                  }}
                />
              ))}
            </div>
          ) : (
            <div className="empty-state">
              <h3>没有匹配的作者</h3>
              <p>试试其他关键词，或先保存一些作品。</p>
            </div>
          )}
        </>
      )}
    </div>
  );
}
