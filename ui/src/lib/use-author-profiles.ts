import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError, type CachedAuthorProfile } from "./api";

export type AuthorProfileState = {
  data?: CachedAuthorProfile;
  loading?: boolean;
  error?: string;
};
const limits = new Set([
  "COOKIE_REQUIRED",
  "UPSTREAM_RISK_CONTROL",
  "ACCOUNT_UNVERIFIED",
]);

export function useAuthorProfiles() {
  const [entries, setEntries] = useState<Record<string, AuthorProfileState>>(
    {},
  );
  const [notice, setNotice] = useState("");
  const records = useRef<Record<string, AuthorProfileState>>({});
  const queue = useRef<{ uid: string; force: boolean }[]>([]);
  const attempted = useRef(new Set<string>());
  const requested = useRef(new Set<string>());
  const drain = useRef<() => void>(() => {});
  const paused = useRef(false);
  const update = useCallback((uid: string, value: AuthorProfileState) => {
    records.current = {
      ...records.current,
      [uid]: { ...records.current[uid], ...value },
    };
    setEntries(records.current);
  }, []);
  const request = useCallback(
    (uid: string, force = false) => {
      if (!uid.startsWith("MS4")) return;
      requested.current.add(uid);
      const current = records.current[uid];
      if (paused.current && !force && !(current?.data && !current.data.stale)) {
        update(uid, { loading: false, error: "资料读取已暂停，可稍后重试" });
        return;
      }
      if (
        current?.loading ||
        (!force &&
          (attempted.current.has(uid) ||
            (current?.data && !current.data.stale)))
      )
        return;
      attempted.current.add(uid);
      if (force) {
        paused.current = false;
        setNotice("");
      }
      if (force) queue.current.unshift({ uid, force });
      else queue.current.push({ uid, force });
      update(uid, { loading: true, error: undefined });
      drain.current();
    },
    [update],
  );
  useEffect(() => {
    const controller = new AbortController();
    let ready = false,
      running = false;
    function pause(code?: string) {
      paused.current = true;
      for (const item of queue.current.splice(0))
        update(item.uid, {
          loading: false,
          error: "资料读取已暂停，可稍后重试",
        });
      setNotice(
        code === "COOKIE_REQUIRED"
          ? "请先配置抖音 Cookie 以补全作者资料，已保存的作品仍可离线查看。"
          : "平台暂时限制了资料读取，已保留缓存和本地作品。更新 Cookie 或稍后重试。",
      );
    }
    async function pump() {
      if (!ready || running || paused.current || controller.signal.aborted)
        return;
      running = true;
      try {
        while (
          queue.current.length &&
          !paused.current &&
          !controller.signal.aborted
        ) {
          const item = queue.current.shift()!;
          const cached = records.current[item.uid]?.data;
          if (cached && !cached.stale && !item.force) {
            update(item.uid, { loading: false });
            continue;
          }
          try {
            const result = await api<CachedAuthorProfile>(
              `/authors/${encodeURIComponent(item.uid)}/profile${item.force ? "?refresh=1" : ""}`,
              "GET",
              undefined,
              controller.signal,
            );
            if (controller.signal.aborted) break;
            update(item.uid, {
              data: result,
              loading: false,
              error: result.warning,
            });
            if (limits.has(result.warning_code || "")) {
              pause(result.warning_code);
            }
          } catch (error) {
            if (controller.signal.aborted) break;
            update(item.uid, {
              loading: false,
              error:
                error instanceof Error ? error.message : "作者资料暂时无法读取",
            });
            if (error instanceof ApiError && limits.has(error.code || "")) {
              pause(error.code);
            }
          }
        }
      } finally {
        running = false;
      }
    }
    drain.current = () => void pump();
    void api<{ items: CachedAuthorProfile[] }>(
      "/library/authors",
      "GET",
      undefined,
      controller.signal,
    )
      .then((result) => {
        if (!controller.signal.aborted)
          for (const item of result.items)
            update(item.profile.uid, { data: item });
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setNotice("本地资料缓存读取失败，仍可查看作品并重试资料。");
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          ready = true;
          void pump();
        }
      });
    return () => {
      controller.abort();
      drain.current = () => {};
      queue.current = [];
      attempted.current.clear();
      // StrictMode may restart the effect with the same refs.
      records.current = Object.fromEntries(
        Object.entries(records.current).map(([uid, entry]) => [
          uid,
          { ...entry, loading: false },
        ]),
      );
      for (const uid of requested.current) {
        queue.current.push({ uid, force: false });
        attempted.current.add(uid);
      }
    };
  }, [update]);
  const retry = useCallback(
    (uid: string) => {
      paused.current = false;
      setNotice("");
      request(uid, true);
      drain.current();
    },
    [request],
  );
  return { entries, notice, request, retry };
}
