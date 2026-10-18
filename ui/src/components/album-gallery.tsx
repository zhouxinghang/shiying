import { useEffect, useRef, useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  Download,
  ImageOff,
  LayoutGrid,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Video } from "@/lib/api";

export function AlbumGallery({
  content,
  saved = false,
  viewer = false,
  keyboardEnabled = true,
  focusMode = false,
}: {
  content: Video;
  saved?: boolean;
  viewer?: boolean;
  keyboardEnabled?: boolean;
  focusMode?: boolean;
}) {
  const [position, setPosition] = useState(0);
  const [failed, setFailed] = useState(false);
  const [thumbnails, setThumbnails] = useState(!viewer);
  const gallery = useRef<HTMLElement>(null);
  const images = content.images || [];
  const image = images[position];
  useEffect(() => {
    if (focusMode) setThumbnails(false);
  }, [focusMode]);
  useEffect(() => {
    setPosition(0);
  }, [content.id]);
  useEffect(() => {
    setFailed(false);
  }, [image?.url]);
  const move = (delta: number) =>
    setPosition((current) =>
      Math.max(0, Math.min(images.length - 1, current + delta)),
    );
  useEffect(() => {
    if (!viewer || !keyboardEnabled) return;
    const key = (event: KeyboardEvent) => {
      const target = event.target;
      if (
        target instanceof HTMLElement &&
        (target.closest(
          "input,textarea,select,[contenteditable=true],[role=slider],.work-detail-info",
        ) ||
          event.altKey ||
          event.ctrlKey ||
          event.metaKey)
      )
        return;
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        setPosition((current) =>
          Math.max(
            0,
            Math.min(
              images.length - 1,
              current + (event.key === "ArrowLeft" ? -1 : 1),
            ),
          ),
        );
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [viewer, keyboardEnabled, images.length]);
  useEffect(() => {
    if (viewer && thumbnails)
      gallery.current
        ?.querySelector<HTMLButtonElement>(
          ".album-thumbnails button[aria-pressed=true]",
        )
        ?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [viewer, thumbnails, position]);
  if (!image)
    return <p className="text-sm text-muted-foreground">没有可预览的图片。</p>;
  return (
    <section
      ref={gallery}
      className={viewer ? "album-gallery album-viewer" : "album-gallery"}
      aria-label="图集预览"
      onKeyDown={(event) => {
        if (viewer) return;
        if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
          event.preventDefault();
          move(event.key === "ArrowLeft" ? -1 : 1);
        }
      }}
    >
      <div className="album-stage">
        {failed ? (
          <div className="album-unavailable">
            <ImageOff size={28} />
            <span>这张图片暂时无法预览</span>
            <small>可导出后使用本地看图工具打开</small>
          </div>
        ) : (
          <img
            src={image.url}
            alt={`${content.title || "图集"}，第 ${image.index} 张`}
            onError={() => setFailed(true)}
          />
        )}
        <Button
          variant="secondary"
          size="icon"
          className="album-prev"
          disabled={position === 0}
          aria-label="上一张图片"
          onClick={() => move(-1)}
        >
          <ChevronLeft />
        </Button>
        <Button
          variant="secondary"
          size="icon"
          className="album-next"
          disabled={position === images.length - 1}
          aria-label="下一张图片"
          onClick={() => move(1)}
        >
          <ChevronRight />
        </Button>
        {!viewer && (
          <span className="album-counter" aria-live="polite">
            {image.index} / {content.image_count}
          </span>
        )}
      </div>
      {viewer && (
        <div className="album-viewer-toolbar">
          <span aria-live="polite">
            第 {image.index} / {content.image_count} 张
          </span>
          <div>
            <Button
              variant="ghost"
              size="sm"
              aria-pressed={thumbnails}
              onClick={() => setThumbnails((value) => !value)}
            >
              <LayoutGrid size={15} />
              {thumbnails ? "收起缩略图" : "缩略图"}
            </Button>
            {saved && (
              <Button variant="outline" size="sm" asChild>
                <a href={`${image.url}?download=1`}>
                  <Download size={14} />
                  导出此图
                </a>
              </Button>
            )}
          </div>
        </div>
      )}
      {thumbnails && (
        <div className="album-thumbnails" aria-label="选择图片">
          {images.map((item, i) => (
            <button
              key={item.index}
              aria-label={`查看第 ${item.index} 张图片`}
              aria-pressed={position === i}
              onClick={() => setPosition(i)}
            >
              <img loading="lazy" src={item.url} alt="" />
              <span>{item.index}</span>
            </button>
          ))}
        </div>
      )}
      {saved && !viewer && (
        <Button variant="outline" size="sm" asChild>
          <a href={`${image.url}?download=1`}>
            <Download size={14} />
            导出第 {image.index} 张
          </a>
        </Button>
      )}
    </section>
  );
}
