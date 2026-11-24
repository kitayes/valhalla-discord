from __future__ import annotations

import argparse
from io import BytesIO
import json
import os
import sys
import urllib.request
from PIL import Image, ImageDraw, ImageFont

FONT_REGULAR = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
FONT_BOLD = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"


def _load_font(path: str, size: int) -> ImageFont.FreeTypeFont:
    if os.path.exists(path):
        return ImageFont.truetype(path, size=size)
    return ImageFont.load_default()


def render_en_victory_16_9() -> bytes:
    w, h = 1920, 1080
    img = Image.new("RGB", (w, h), (16, 23, 38))
    draw = ImageDraw.Draw(img)

    # Subtle blue header banner so color fallback also sees left=VICTORY
    draw.rectangle([420, 12, 1500, 175], fill=(24, 52, 108))
    draw.rectangle([60, 210, 940, 910], fill=(22, 34, 56))
    draw.rectangle([980, 210, 1860, 910], fill=(45, 24, 34))

    f_title = _load_font(FONT_BOLD, 54)
    f_score = _load_font(FONT_BOLD, 42)
    f_time = _load_font(FONT_REGULAR, 28)
    f_name = _load_font(FONT_BOLD, 28)
    f_stat = _load_font(FONT_BOLD, 30)
    f_small = _load_font(FONT_BOLD, 22)

    draw.text((w // 2, 58), "VICTORY", fill=(240, 210, 95), font=f_title, anchor="mm")
    draw.text((int(0.435 * w), 132), "20", fill=(115, 195, 255), font=f_score, anchor="mm")
    draw.text((w // 2, 132), "12:34", fill=(210, 220, 235), font=f_time, anchor="mm")
    draw.text((int(0.565 * w), 132), "12", fill=(255, 120, 120), font=f_score, anchor="mm")

    left_rows = [
        ("Bjorn", 8, 2, 6, "11.2k", "11.4", "MVP"),
        ("Thorin", 5, 2, 7, "9.8k", "9.2", ""),
        ("Freya", 3, 3, 9, "8.9k", "8.5", ""),
        ("Arthas", 2, 2, 10, "8.1k", "8.0", ""),
        ("Kael", 2, 3, 8, "7.9k", "7.6", ""),
    ]
    right_rows = [
        ("Gunnar", 6, 3, 4, "10.1k", "8.9", "SVPG"),
        ("Loki", 2, 4, 5, "7.8k", "6.1", ""),
        ("Astrid", 2, 5, 4, "7.4k", "5.8", ""),
        ("Soren", 1, 4, 6, "6.9k", "5.2", ""),
        ("Ragnar", 1, 4, 5, "6.5k", "4.9", ""),
    ]

    row_ys = [285, 415, 545, 675, 805]
    for idx, (name, k, d, a, gold, rating, badge) in enumerate(left_rows):
        y = row_ys[idx]
        draw.rectangle([75, y - 48, 925, y + 48], fill=(28, 44, 72))
        draw.ellipse([125, y - 34, 193, y + 34], fill=(64, 110, 180))
        draw.text((160, y + 20), "15", fill=(255, 255, 255), font=f_small, anchor="mm")
        draw.text((290, y), name, fill=(245, 248, 255), font=f_name, anchor="lm")
        draw.text((515, y), str(k), fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((585, y), str(d), fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((655, y), str(a), fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((745, y), gold, fill=(245, 210, 110), font=f_small, anchor="mm")
        draw.text((835, y), rating, fill=(130, 225, 255), font=f_stat, anchor="mm")
        if badge:
            draw.rectangle([865, y - 16, 922, y + 16], fill=(230, 175, 40))
            draw.text((893, y), badge, fill=(15, 20, 30), font=f_small, anchor="mm")

    for idx, (name, k, d, a, gold, rating, badge) in enumerate(right_rows):
        y = row_ys[idx]
        draw.rectangle([995, y - 48, 1845, y + 48], fill=(64, 32, 44))
        if badge:
            draw.rectangle([998, y - 16, 1066, y + 16], fill=(170, 140, 220))
            draw.text((1032, y), badge, fill=(15, 20, 30), font=f_small, anchor="mm")
        draw.text((1102, y), rating, fill=(255, 195, 165), font=f_stat, anchor="mm")
        draw.text((1185, y), gold, fill=(245, 210, 110), font=f_small, anchor="mm")
        draw.text((1265, y), str(k), fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((1335, y), str(d), fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((1405, y), str(a), fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((1630, y), name, fill=(245, 248, 255), font=f_name, anchor="rm")
        draw.ellipse([1725, y - 34, 1793, y + 34], fill=(165, 70, 85))
        draw.text((1760, y + 20), "14", fill=(255, 255, 255), font=f_small, anchor="mm")

    buf = BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


def render_ru_defeat_20_9() -> bytes:
    w, h = 2400, 1080
    img = Image.new("RGB", (w, h), (17, 22, 35))
    draw = ImageDraw.Draw(img)

    # Red defeat top banner
    draw.rectangle([600, 12, 1800, 175], fill=(118, 28, 34))
    draw.rectangle([120, 210, 1170, 910], fill=(56, 26, 36))
    draw.rectangle([1230, 210, 2280, 910], fill=(24, 40, 68))

    f_title = _load_font(FONT_BOLD, 54)
    f_score = _load_font(FONT_BOLD, 42)
    f_time = _load_font(FONT_REGULAR, 28)
    f_name = _load_font(FONT_BOLD, 28)
    f_stat = _load_font(FONT_BOLD, 30)
    f_small = _load_font(FONT_BOLD, 22)

    draw.text((w // 2, 58), "ПОРАЖЕНИЕ", fill=(255, 165, 165), font=f_title, anchor="mm")
    draw.text((int(0.435 * w), 132), "10", fill=(255, 130, 130), font=f_score, anchor="mm")
    draw.text((w // 2, 132), "14:20", fill=(220, 225, 235), font=f_time, anchor="mm")
    draw.text((int(0.565 * w), 132), "18", fill=(120, 200, 255), font=f_score, anchor="mm")

    left_rows = [
        ("Бьорн", 5, 3, 2, "9,850", "9.5"),
        ("Тор", 2, 4, 3, "8,120", "6.2"),
        ("Фрейя", 1, 4, 4, "7,400", "5.8"),
        ("Олаф", 1, 4, 5, "7,100", "5.5"),
        ("Сигурд", 1, 3, 3, "6,800", "5.1"),
    ]
    right_rows = [
        ("Егор", 10, 1, 5, "12.4k", "12.1"),
        ("Алиса", 3, 2, 7, "10.2k", "8.8"),
        ("Виктор", 2, 2, 8, "9.5k", "8.2"),
        ("Глеб", 2, 2, 6, "8.9k", "7.4"),
        ("Дима", 1, 3, 9, "8.4k", "7.1"),
    ]

    row_ys = [285, 415, 545, 675, 805]
    for idx, (name, k, d, a, gold, rating) in enumerate(left_rows):
        y = row_ys[idx]
        draw.rectangle([140, y - 48, 1150, y + 48], fill=(68, 34, 46))
        draw.ellipse([190, y - 34, 258, y + 34], fill=(165, 70, 85))
        draw.text((225, y + 20), "14", fill=(255, 255, 255), font=f_small, anchor="mm")
        draw.text((370, y), name, fill=(245, 248, 255), font=f_name, anchor="lm")
        draw.text((710, y), f"{k}/{d}/{a}", fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((905, y), gold, fill=(245, 210, 110), font=f_small, anchor="mm")
        draw.text((1045, y), rating, fill=(255, 195, 165), font=f_stat, anchor="mm")

    for idx, (name, k, d, a, gold, rating) in enumerate(right_rows):
        y = row_ys[idx]
        draw.rectangle([1250, y - 48, 2260, y + 48], fill=(30, 48, 80))
        draw.text((1355, y), rating, fill=(130, 225, 255), font=f_stat, anchor="mm")
        draw.text((1495, y), gold, fill=(245, 210, 110), font=f_small, anchor="mm")
        draw.text((1690, y), f"{k}/{d}/{a}", fill=(255, 255, 255), font=f_stat, anchor="mm")
        draw.text((2030, y), name, fill=(245, 248, 255), font=f_name, anchor="rm")
        draw.ellipse([2140, y - 34, 2208, y + 34], fill=(64, 110, 180))
        draw.text((2175, y + 20), "15", fill=(255, 255, 255), font=f_small, anchor="mm")

    buf = BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


def post_multipart(url: str, image_bytes: bytes, expected: list[str]) -> dict:
    boundary = "----ValhallaOCRBoundary7MA4YWxkTrZu0gW"
    body = bytearray()
    body.extend(f"--{boundary}\r\n".encode())
    body.extend(b'Content-Disposition: form-data; name="image"; filename="scoreboard.png"\r\n')
    body.extend(b"Content-Type: image/png\r\n\r\n")
    body.extend(image_bytes)
    body.extend(b"\r\n")
    body.extend(f"--{boundary}\r\n".encode())
    body.extend(b'Content-Disposition: form-data; name="expected_players"\r\n\r\n')
    body.extend(json.dumps(expected).encode())
    body.extend(f"\r\n--{boundary}--\r\n".encode())

    req = urllib.request.Request(
        url,
        data=bytes(body),
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://127.0.0.1:18000/parse?debug=true")
    ap.add_argument("--save-en", default="", help="Write EN VICTORY PNG to this path and exit")
    args = ap.parse_args()

    en_png = render_en_victory_16_9()
    if args.save_en:
        with open(args.save_en, "wb") as f:
            f.write(en_png)
        return 0

    ru_png = render_ru_defeat_20_9()

    en_expected = ["Bjorn", "Thorin", "Freya", "Arthas", "Kael", "Gunnar", "Loki", "Astrid", "Soren", "Ragnar"]
    ru_expected = ["Бьорн", "Тор", "Фрейя", "Олаф", "Сигурд", "Егор", "Алиса", "Виктор", "Глеб", "Дима"]

    cases = [
        (
            "EN 16:9 VICTORY",
            en_png,
            en_expected,
            {
                "mvp": "Bjorn",
                "svp": "Gunnar",
                "header_win_kills": 20,
                "header_lose_kills": 12,
                "first_kda": (8, 2, 6),
                "first_result": "WIN",
            },
        ),
        (
            "RU 20:9 ПОРАЖЕНИЕ",
            ru_png,
            ru_expected,
            {
                "mvp": "Erop",
                "svp": "Бьорн",
                "header_win_kills": 18,
                "header_lose_kills": 10,
                "first_kda": (5, 3, 2),
                "first_result": "LOSE",
            },
        ),
    ]

    for label, png, exp, want in cases:
        res = post_multipart(args.url, png, exp)
        print(f"=== {label} ===")
        print(json.dumps({k: v for k, v in res.items() if k != "raw_boxes"}, ensure_ascii=False, indent=2))
        players = res.get("players", [])
        if len(players) != 10:
            print("RAW BOXES:", json.dumps(res.get("raw_boxes", []), ensure_ascii=False))
            return 1
        if res.get("warnings"):
            print(f"UNEXPECTED WARNINGS ({label}):", res["warnings"])
            return 1
        if res.get("header_win_kills") != want["header_win_kills"] or res.get("header_lose_kills") != want["header_lose_kills"]:
            print(f"HEADER KILLS MISMATCH ({label}):", res.get("header_win_kills"), res.get("header_lose_kills"))
            return 1
        if res.get("mvp") != want["mvp"]:
            print(f"MVP MISMATCH ({label}): got {res.get('mvp')!r}, want {want['mvp']!r}")
            return 1
        if res.get("svp") != want["svp"]:
            print(f"SVP MISMATCH ({label}): got {res.get('svp')!r}, want {want['svp']!r}")
            return 1
        p0 = players[0]
        if (p0.get("kills"), p0.get("deaths"), p0.get("assists")) != want["first_kda"] or p0.get("result") != want["first_result"]:
            print(f"FIRST PLAYER MISMATCH ({label}):", p0)
            return 1

    return 0


if __name__ == "__main__":
    sys.exit(main())
