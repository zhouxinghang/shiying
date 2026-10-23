import type { Task } from "./api";

export type AlbumSession = { order: string[]; position: number };

export function browsableAlbums(tasks: Task[]): Task[] {
  return tasks.filter(
    (task) =>
      task.kind === "image_album" &&
      (task.status === "completed" || task.status === "partial") &&
      task.image_count > 0,
  );
}

export function firstAlbumImage(task: Task): string {
  return (
    task.images?.find((image) => image.index === 1)?.url ||
    `/api/tasks/${task.id}/images/1`
  );
}

function shuffle(ids: string[]): string[] {
  const result = [...ids];
  for (let i = result.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [result[i], result[j]] = [result[j], result[i]];
  }
  return result;
}

// Polling may add or delete works. Keep the current work and browsing history
// stable, and place new albums after the remaining works in this round.
export function syncAlbumSession(
  session: AlbumSession,
  available: string[],
): AlbumSession {
  const allowed = new Set(available);
  const known = new Set(session.order);
  const order = session.order.filter((id) => allowed.has(id));
  const position = session.order
    .slice(0, session.position)
    .filter((id) => allowed.has(id)).length;
  order.push(...shuffle(available.filter((id) => !known.has(id))));
  return { order, position: Math.min(position, Math.max(0, order.length - 1)) };
}

export function nextAlbumSession(
  session: AlbumSession,
  available: string[],
): AlbumSession {
  if (session.position < session.order.length - 1)
    return { ...session, position: session.position + 1 };
  if (available.length < 2) return session;

  const round = shuffle(available);
  // A new round must not immediately repeat the photo just displayed.
  if (round[0] === session.order[session.position])
    [round[0], round[1]] = [round[1], round[0]];
  return {
    order: [...session.order, ...round],
    position: session.order.length,
  };
}
