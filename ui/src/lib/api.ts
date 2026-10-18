export type Video = {
  id: string;
  kind: "video" | "image_album";
  description: string;
  image_count: number;
  images?: { index: number; url: string; width?: number; height?: number }[];
  title: string;
  author: string;
  author_uid: string;
  cover: string;
  duration: number;
  width: number;
  height: number;
  source_url: string;
};
export type Task = Video & {
  status:
    | "queued"
    | "downloading"
    | "completed"
    | "failed"
    | "canceled"
    | "partial";
  files: {
    name: string;
    kind: string;
    state: string;
    bytes: number;
    sha256?: string;
    retained?: boolean;
    error?: string;
  }[];
  files_completed: number;
  files_total: number;
  downloaded: number;
  total: number;
  error: string;
  favorite: boolean;
  created_at: string;
  updated_at: string;
};
export type Preview = {
  preview_id: string;
  video: Video;
  account_scope?: string;
};
export type Settings = {
  cookie_configured: boolean;
  cookie_revision: string;
  data_dir: string;
  concurrency: number;
  max_video_bytes: number;
  max_image_bytes: number;
  max_files: number;
  version: string;
};
export type Library = {
  tasks: Task[];
  stats: {
    completed: number;
    active: number;
    failed: number;
    total_bytes: number;
  };
};
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code?: string,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    headers:
      body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new ApiError(
      data.error || `请求失败 (${response.status})`,
      response.status,
      data.code,
    );
  return data as T;
}

export type AuthorProfile = {
  uid: string;
  nickname: string;
  signature: string;
  avatar: string;
  web_url: string;
  works_count: number | null;
  follower_count: number | null;
};
export type AuthorPost = {
  content: Video;
  preview_id: string;
  task_id?: string;
  task_status?: Task["status"];
  downloadable: boolean;
  reason?: string;
};
export type CachedAuthorProfile = {
  profile: AuthorProfile;
  fetched_at: string;
  stale: boolean;
  warning?: string;
  warning_code?: string;
};
export type AuthorPageData = {
  items: AuthorPost[];
  cursor: string;
  has_more: boolean;
  notice?: string;
  account_scope?: string;
};
export type BatchResult = {
  preview_id: string;
  task_id?: string;
  title?: string;
  state: "added" | "existing" | "failed";
  error?: string;
};

export type AccountSession = {
  profile: AuthorProfile;
  scope: string;
  likes_count: number | null;
  bookmarks_count: number | null;
};
