import type { Task } from "./api";

export type AuthorGroup = {
  key: string;
  name: string;
  uid: string;
  tasks: Task[];
};

// Preserve the incoming sort order for both authors and their works. A nickname
// is only a fallback for old downloads without an author ID.
export function groupWorksByAuthor(tasks: Task[]): AuthorGroup[] {
  const groups = new Map<string, AuthorGroup>();
  for (const task of tasks) {
    const uid = task.author_uid?.trim();
    const name = task.author?.trim() || "未知作者";
    const key = uid ? `uid:${uid}` : `name:${name}`;
    let group = groups.get(key);
    if (!group) {
      group = { key, name, uid: uid || "", tasks: [] };
      groups.set(key, group);
    }
    group.tasks.push(task);
  }
  return [...groups.values()];
}
