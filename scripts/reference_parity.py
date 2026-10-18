"""Generate Go's golden data by running the repository's real Python parsers.

Requires Python 3.12+ and pydantic 2, but no running reference server. Namespace
packages below skip application startup and its unrelated runtime dependencies.
Run from any directory. No fixture or production Cookie is read.
"""
import hashlib
import json
from pathlib import Path
import sys
import types
from datetime import datetime, timezone
from dataclasses import replace
from urllib.parse import urlencode

ROOT = Path(__file__).resolve().parents[2]
for name in ("dtk", "dtk.core", "dtk.platforms", "dtk.platforms.douyin", "dtk.urls", "dtk.media"):
    package = types.ModuleType(name)
    package.__path__ = [str(ROOT / "src" / name.replace(".", "/"))]
    sys.modules[name] = package
from dtk.platforms.douyin.parser import parse_content, parse_author, parse_author_posts
from dtk.media.plan import build
from dtk.platforms.douyin.params import DEFAULT_PROFILE, content_detail_params, author_profile_params, author_posts_params, author_likes_params, author_collections_params, session_check_params

sources = ["src/dtk/platforms/douyin/parser.py", "src/dtk/platforms/common.py", "src/dtk/media/plan.py", "src/dtk/media/domains.py", "src/dtk/urls/parse.py", "src/dtk/urls/patterns.py", "src/dtk/platforms/douyin/params.py", "src/dtk/signing/native/abogus.py", "src/dtk/signing/native/sm3.py", "src/dtk/signing/native/websign.py", "docker/downloader/download.go", "docker/downloader/safety.go"]
params=content_detail_params(aweme_id="7000000000000000001",profile=replace(DEFAULT_PROFILE,browser_version="146.0.0.0",engine_version="146.0.0.0"))
params["msToken"]="fixture-token"
out = {"detail_query": urlencode(params), "sources": {p: hashlib.sha256((ROOT/p).read_bytes()).hexdigest() for p in sources}, "cases": []}
for name in ("video_normal", "video_long_desc", "video_image_album", "video_private", "video_deleted", "risk_control_empty", "risk_control_captcha"):
    payload = json.loads((ROOT / "tests/fixtures/douyin" / (name+".json")).read_text().replace("example-cdn.invalid", "douyinvod.com"))
    case = {"fixture": name}
    try:
        c = parse_content(payload, fetched_at=datetime(2026, 10, 18, tzinfo=timezone.utc))
        media = c.media.model_dump(mode="json")
        case["content"] = {"id": c.content_id, "title": c.title, "description": c.description, "kind": c.kind.value, "author": c.author.nickname, "author_uid": c.author.uid, "source_url": c.web_url, "duration": c.duration_ms or 0, "tags": c.tags, "music_id": c.music.music_id if c.music else None, "music_title": c.music.title if c.music else None, "location": c.location, "is_deleted": c.is_deleted, "is_private": c.is_private, "manifest": media}
        plan = build(media, c.platform, max_file_bytes=512*1024**2)
        case["plan"] = {"items": [i.as_payload() for i in plan.items], "skipped": list(plan.skipped)}
    except Exception as exc:
        case["error"] = type(exc).__name__
    out["cases"].append(case)
profile=replace(DEFAULT_PROFILE,browser_version="146.0.0.0",engine_version="146.0.0.0")
uid="MS4wLjABAAAA_SYNTHETIC_DOUYIN_AUTHOR_0001"
queries={}
for name,params in {"profile":author_profile_params(sec_user_id=uid,profile=profile),"posts":author_posts_params(sec_user_id=uid,profile=profile),"next":author_posts_params(sec_user_id=uid,cursor="1716300000000",profile=profile)}.items():
    params["msToken"]="fixture-token"
    queries[name]=urlencode(params)
profile_payload=json.loads((ROOT/"tests/fixtures/douyin/user_profile.json").read_text().replace("example-cdn.invalid","douyinvod.com"))
posts_payload=json.loads((ROOT/"tests/fixtures/douyin/user_posts_page1.json").read_text().replace("example-cdn.invalid","douyinvod.com"))
a=parse_author(profile_payload)
p=parse_author_posts(posts_payload,fetched_at=datetime(2026,10,18,tzinfo=timezone.utc))
out["authors"]={"queries":queries,"profile":{"uid":a.uid,"nickname":a.nickname,"signature":a.signature,"works_count":a.stats.content_count,"follower_count":a.stats.follower_count,"web_url":a.web_url},"page":{"ids":[c.content_id for c in p.items],"cursor":p.cursor,"has_more":p.has_more}}
# These builders establish ordered query parity only. The all-bookmarks endpoint
# is POST listcollection, not the current reference's GET collects/list folders.
# Both use the same cursor/count query shape. See PARITY.md for endpoint sources.
owner="MS4wLjABAAAA_SYNTHETIC_LOGIN_OWNER_0001"
personal_queries={}
for name,params in {"self":session_check_params(profile=profile),"likes":author_likes_params(sec_user_id=owner,profile=profile),"likes_next":author_likes_params(sec_user_id=owner,cursor="1716300000000",profile=profile),"bookmarks":author_collections_params(profile=profile),"bookmarks_next":author_collections_params(cursor="20",profile=profile)}.items():
    params["msToken"]="fixture-token"
    personal_queries[name]=urlencode(params)
out["personal_queries"]=personal_queries
path = ROOT / "personal/internal/app/testdata/reference_parity.json"
path.write_text(json.dumps(out, ensure_ascii=False, indent=2)+"\n")
print(f"Generated {len(out['cases'])} reference cases and {len(sources)} source hashes")
