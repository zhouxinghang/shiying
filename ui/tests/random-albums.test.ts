import assert from "node:assert/strict";
import { test } from "node:test";
import {
  browsableAlbums,
  firstAlbumImage,
  nextAlbumSession,
  syncAlbumSession,
} from "../src/lib/random-albums.ts";
import type { Task } from "../src/lib/api.ts";

test("random rounds include every album once, avoid immediate repeats, and preserve history", () => {
  const ids = ["a", "b", "c", "d"];
  let session = syncAlbumSession({ order: [], position: 0 }, ids);
  assert.deepEqual([...session.order].sort(), ids);
  const firstRound = [...session.order];
  for (let i = 1; i < ids.length; i++) session = nextAlbumSession(session, ids);
  assert.equal(session.position, 3);
  session = nextAlbumSession(session, ids);
  assert.equal(session.position, 4);
  assert.notEqual(session.order[3], session.order[4]);
  assert.deepEqual(session.order.slice(0, 4), firstRound);
  assert.deepEqual(session.order.slice(4).sort(), ids);
  // Returning to a previous work and moving forward follows the same history.
  const revisited = { ...session, position: 1 };
  assert.deepEqual(nextAlbumSession(revisited, ids), {
    ...session,
    position: 2,
  });
});

test("polling and additions preserve the current work and unvisited order", () => {
  const session = { order: ["c", "a", "b"], position: 1 };
  assert.deepEqual(syncAlbumSession(session, ["a", "b", "c"]), session);
  assert.deepEqual(syncAlbumSession(session, ["a", "b", "c", "d"]), {
    order: ["c", "a", "b", "d"],
    position: 1,
  });
});

test("deleted works are removed from every round without losing the current position", () => {
  const session = { order: ["a", "b", "c", "b", "a", "c"], position: 4 };
  assert.deepEqual(syncAlbumSession(session, ["a", "c"]), {
    order: ["a", "c", "a", "c"],
    position: 2,
  });
  assert.deepEqual(syncAlbumSession(session, ["b", "c"]), {
    order: ["b", "c", "b", "c"],
    position: 3,
  });
  assert.deepEqual(syncAlbumSession(session, []), { order: [], position: 0 });
});

test("empty and single-album libraries do not produce invalid navigation", () => {
  const empty = { order: [], position: 0 };
  assert.deepEqual(nextAlbumSession(empty, []), empty);
  const single = syncAlbumSession(empty, ["a"]);
  assert.deepEqual(nextAlbumSession(single, ["a"]), single);
  assert.deepEqual(syncAlbumSession(single, []), empty);
});

test("only saved image albums are browsable and their first image is used rather than the cover", () => {
  const makeTask = (
    id: string,
    kind: Task["kind"],
    status: Task["status"],
    image_count = 3,
  ) => ({ id, kind, status, image_count }) as Task;
  const tasks = [
    makeTask("video", "video", "completed"),
    makeTask("saved", "image_album", "completed"),
    makeTask("partial", "image_album", "partial"),
    makeTask("queued", "image_album", "queued"),
    makeTask("downloading", "image_album", "downloading"),
    makeTask("failed", "image_album", "failed"),
    makeTask("canceled", "image_album", "canceled"),
    makeTask("empty", "image_album", "completed", 0),
  ];
  assert.deepEqual(
    browsableAlbums(tasks).map((task) => task.id),
    ["saved", "partial"],
  );
  assert.equal(firstAlbumImage(tasks[1]), "/api/tasks/saved/images/1");
  assert.equal(
    firstAlbumImage({
      ...tasks[1],
      cover: "/cover",
      images: [
        { index: 2, url: "/second" },
        { index: 1, url: "/first" },
      ],
    }),
    "/first",
  );
});
