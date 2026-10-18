import { useEffect, useState } from "react";
import { ChevronLeft, ChevronRight, Download, ImageOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Video } from "@/lib/api";

export function AlbumGallery({
  content,
  saved = false,
}: {
  content: Video;
  saved?: boolean;
}) {
  const [position, setPosition] = useState(0);
  const [failed, setFailed] = useState(false);
  const images = content.images || [];
  const image = images[position];
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
  if (!image)
    return <p className="text-sm text-muted-foreground">没有可预览的图片。</p>;
  return (
    <section
      className="album-gallery"
      aria-label="图集预览"
      onKeyDown={(event) => {
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
        <span className="album-counter" aria-live="polite">
          {image.index} / {content.image_count}
        </span>
      </div>
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
      {saved && (
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
