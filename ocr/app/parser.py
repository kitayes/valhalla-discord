from __future__ import annotations

from dataclasses import dataclass
import re
from statistics import median
from typing import Iterable, Optional
import unicodedata

VICTORY_KEYWORDS = ("VICTORY", "ПОБЕДА")
DEFEAT_KEYWORDS = ("DEFEAT", "ПОРАЖЕНИЕ")
HEADER_IGNORE_WORDS = {
    "VICTORY",
    "DEFEAT",
    "ПОБЕДА",
    "ПОРАЖЕНИЕ",
    "VS",
    "KDA",
    "GOLD",
    "ЗОЛОТО",
    "DAMAGE",
    "УРОН",
    "DURATION",
    "ДЛИТЕЛЬНОСТЬ",
    "MATCH RESULT",
    "ИТОГИ МАТЧА",
    "DETAILS",
    "ДЕТАЛИ",
    "DATA",
    "ДАННЫЕ",
    "ВЫЙТИ",
    "NEW",
    "ЛОББИ",
    "МОМЕНТЫ",
    "ВСЕМ",
    "ЗДЕСЬ",
    "ЗДЕСЬ.",
    '"ЛАЙК"',
}

UI_OVERLAY_PATTERNS = (
    re.compile(r"вернуться\s+в\s+лобби", re.IGNORECASE),
    re.compile(r"вернуться\s+в", re.IGNORECASE),
    re.compile(r"оставьте\s+отзыв(?:\s+об\s+этом\s+матче)?", re.IGNORECASE),
    re.compile(r"скопировано\s+в\s+буфер\s+обмена", re.IGNORECASE),
    re.compile(r"быстрый\s+чат", re.IGNORECASE),
    re.compile(r"создать\s+яркие", re.IGNORECASE),
    re.compile(r"играть\s*снова", re.IGNORECASE),
    re.compile(r'"?лайк"?\s+всем', re.IGNORECASE),
    re.compile(r"б[оo]ево[йe]?\s*[id|d]{1,2}\s*\d*", re.IGNORECASE),
    re.compile(r"дли[тt]\.?\s*\d{1,2}:\d{2}(?:\s+\d{2}\.\d{2}\.\d{4}.*)?", re.IGNORECASE),
)

_KDA_SLASH_RE = re.compile(r"(\d{1,2})\s*/\s*(\d{1,2})\s*/\s*(\d{1,2})")
_KDA_THREE_INTS_RE = re.compile(r"^\s*(\d{1,2})\s+(\d{1,2})\s+(\d{1,2})\s*$")
_HEADER_PAIR_RE = re.compile(r"^\s*(\d{1,2})\s*(?:vs|VS|:|-|\s+)\s*(\d{1,2})\s*$")
_TIME_RE = re.compile(r"^\s*\d{1,2}:\d{2}\s*$")
_GOLD_RE = re.compile(r"^\s*\d+(?:[.,]\d+)?\s*[kк]\s*$", re.IGNORECASE)
_GOLD_LONG_RE = re.compile(r"^\s*\d{1,2}[.,]\d{3}\s*$")
_RAW_GOLD_RE = re.compile(r"^\s*\d{4,6}\s*$")
_GLUED_GOLD_TRAILING_SLASH_RE = re.compile(r"^\s*\d{3,5}/\s*$")
_LEVEL_RE = re.compile(r"^(?:lv\.?\s*|ур\.?\s*)?\d{1,2}$", re.IGNORECASE)
_RATING_RE = re.compile(r"^\s*(\d{1,2})[.,'’`](\d)\s*$")
_INT_RE = re.compile(r"^\s*(\d{1,6})\s*$")
_MEDAL_MVP_RE = re.compile(r"(?<![A-Za-zА-Яа-яЁё])MVP(?![A-Za-zА-Яа-яЁё])", re.IGNORECASE)
_MEDAL_SVP_RE = re.compile(r"(?<![A-Za-zА-Яа-яЁё])SVPG?(?![A-Za-zА-Яа-яЁё])", re.IGNORECASE)
_GLUED_RATING_GOLD_RE = re.compile(r"(\d{1,2}[.,'’`]\d)(?=\d+(?:[.,]\d+)?[kк])", re.IGNORECASE)
_GLUED_BADGE_RE = re.compile(r"(?<=\d)(SVPG?|MVP)|(SVPG?|MVP)(?=\d)", re.IGNORECASE)
_STAT_HINT_TOKEN_RE = re.compile(
    r"^(?:\d{1,6}|\d{3,5}/|\d{1,2}[.,'’`]\d|\d+(?:[.,]\d+)?[kк]|\d{1,2}[.,]\d{3}|\d{1,2}/\d{1,2}/\d{1,2}|MVP|SVPG?)$",
    re.IGNORECASE,
)
_GLUED_LEFT_NAME_KDA_GOLD_RE = re.compile(
    r"^(.*?)\s+(\d{1,2})\s+(\d{1,2})\s+(\d{1,2})\s+(\d{4,6})\s*$"
)
_GLUED_LEFT_NAME_PARTIAL_GOLD_RE = re.compile(
    r"^(.*?)[\s|]+(?:\d{1,4}\s+){1,3}(?:\d{4,6}|\d{3,5}/)\s*$"
)
_GLUED_LEFT_NAME_STUCK_GOLD_RE = re.compile(
    r"^(.+?[A-Za-zА-Яа-яЁё._-])(\d{3,4})\s+(\d{4,6})\s*$"
)
_GENDER_SYMBOL_RE = re.compile(r"[♂♀⚤]+")
_SUBHEADER_NOISE_TOKENS = {"$", "S", "X", "Х", "×", "+"}
_CENTER_SYMBOL_NOISE_RE = re.compile(r"^[\+\-\*\$&©%#@~<>\|/\\.,:;!?()]+$|^\+\d$")


@dataclass
class Box:
    x1: float
    y1: float
    x2: float
    y2: float
    text: str
    score: float = 1.0

    @property
    def cx(self) -> float:
        return (self.x1 + self.x2) / 2.0

    @property
    def cy(self) -> float:
        return (self.y1 + self.y2) / 2.0


def _strip_ui_overlay(text: str) -> str:
    cleaned = text
    for pat in UI_OVERLAY_PATTERNS:
        cleaned = pat.sub(" ", cleaned)
    return " ".join(cleaned.split()).strip()


def _levenshtein(a: str, b: str) -> int:
    if a == b:
        return 0
    if not a:
        return len(b)
    if not b:
        return len(a)
    prev = list(range(len(b) + 1))
    for i, ca in enumerate(a, start=1):
        cur = [i]
        for j, cb in enumerate(b, start=1):
            cost = 0 if ca == cb else 1
            cur.append(min(cur[-1] + 1, prev[j] + 1, prev[j - 1] + cost))
        prev = cur
    return prev[-1]


def parse_scoreboard(
    boxes: Iterable[Box],
    width: int,
    height: int,
    expected_players: Optional[list[str]] = None,
    header_side_hint: Optional[str] = None,
) -> dict:
    """Parse RapidOCR boxes from an MLBB scoreboard into structured match data."""
    box_list: list[Box] = []
    for b in boxes:
        norm_text = unicodedata.normalize("NFKC", b.text or "").strip()
        if norm_text:
            box_list.append(Box(b.x1, b.y1, b.x2, b.y2, norm_text, b.score))

    if width <= 0 or height <= 0:
        return {
            "players": [],
            "mvp": None,
            "svp": None,
            "header_win_kills": None,
            "header_lose_kills": None,
            "warnings": ["некорректные размеры изображения"],
            "_row_meta": [],
            "_gold_bounds": {"left_x1": None, "right_x2": None},
        }

    left_won, header_found = _detect_left_won(box_list, width, height, header_side_hint)
    left_header_kills, right_header_kills = _extract_header_kills(box_list, width, height)
    if left_header_kills is not None and right_header_kills is not None:
        header_win_kills = left_header_kills if left_won else right_header_kills
        header_lose_kills = right_header_kills if left_won else left_header_kills
    else:
        header_win_kills = None
        header_lose_kills = None

    body_boxes = _filter_body_boxes(box_list, width, height)
    gold_bounds = _extract_gold_bounds(body_boxes, width)
    left_rows, right_rows, anchor_ys, row_step = _assign_five_row_bands(body_boxes, width, height)

    parsed_rows: list[dict] = []
    row_meta: list[dict] = []

    def process_side(rows: list[list[Box]], is_left: bool) -> None:
        side_result = "WIN" if (is_left == left_won) else "LOSE"
        for idx, row_boxes in enumerate(rows):
            ay = anchor_ys[idx] if idx < len(anchor_ys) else 0.0
            row_data, meta = _parse_row(
                row_boxes, is_left, width, height, side_result, anchor_y=ay, row_step=row_step
            )
            parsed_rows.append(row_data)
            row_meta.append(meta)

    process_side(left_rows, True)
    process_side(right_rows, False)

    out = {
        "players": parsed_rows,
        "mvp": None,
        "svp": None,
        "header_win_kills": header_win_kills,
        "header_lose_kills": header_lose_kills,
        "warnings": [],
        "_header_found": header_found,
        "_row_meta": row_meta,
        "_gold_bounds": gold_bounds,
    }
    return recompute_scoreboard_summary(out)


def _strip_kda_edge_tokens(name: str, is_left: bool, row_data: dict) -> str:
    """Trim stray K (left edge) or A (right edge) digits that leaked into player_name."""
    clean = (name or "").strip().rstrip("©").strip()
    if not clean:
        return ""
    if is_left:
        k_str = str(row_data.get("kills", -1))
        parts = clean.split()
        if len(parts) >= 2 and parts[-1] == k_str:
            cand = " ".join(parts[:-1]).strip()
            if any(ch.isalpha() for ch in cand):
                return cand
        if len(k_str) == 2 and clean.endswith(k_str) and len(clean) >= 6 and clean[-3].isalpha():
            return clean[:-2].strip()
    else:
        a_str = str(row_data.get("assists", -1))
        parts = clean.split()
        if len(parts) >= 2 and parts[0] == a_str:
            cand = " ".join(parts[1:]).strip()
            if any(ch.isalpha() for ch in cand):
                return cand
    return clean


def recompute_scoreboard_summary(parsed: dict) -> dict:
    """Recompute MVP/SVP medals, validation warnings, and side-verified confidences."""
    rows: list[dict] = parsed.get("players", [])
    row_meta: list[dict] = parsed.get("_row_meta", [])
    header_found: bool = parsed.get("_header_found", True)
    header_win_kills: Optional[int] = parsed.get("header_win_kills")
    header_lose_kills: Optional[int] = parsed.get("header_lose_kills")

    for idx, row_data in enumerate(rows):
        meta = row_meta[idx] if idx < len(row_meta) else {}
        row_data["player_name"] = _strip_kda_edge_tokens(
            row_data.get("player_name", ""), meta.get("is_left", True), row_data
        )

    mvp_candidates: list[tuple[str, float]] = []
    svp_candidates: list[tuple[str, float]] = []
    for idx, row_data in enumerate(rows):
        meta = row_meta[idx] if idx < len(row_meta) else {}
        side_result = row_data.get("result")
        pname = row_data.get("player_name")
        if not pname:
            continue
        if meta.get("has_mvp") and side_result == "WIN":
            mvp_candidates.append((pname, row_data.get("rating", 0.0)))
        if (meta.get("has_svp") or meta.get("has_mvp")) and side_result == "LOSE":
            svp_candidates.append((pname, row_data.get("rating", 0.0)))

    win_sum = sum(r["kills"] for r in rows if r.get("result") == "WIN")
    lose_sum = sum(r["kills"] for r in rows if r.get("result") == "LOSE")

    def side_assists_valid(side_result: str, total_kills: Optional[int]) -> bool:
        if total_kills is None:
            return False
        for r in rows:
            if r.get("result") != side_result:
                continue
            if r.get("assists", 0) > max(total_kills - r.get("kills", 0), 0):
                return False
        return True

    win_verified = (
        header_win_kills is not None
        and win_sum == header_win_kills
        and side_assists_valid("WIN", header_win_kills)
    )
    lose_verified = (
        header_lose_kills is not None
        and lose_sum == header_lose_kills
        and side_assists_valid("LOSE", header_lose_kills)
    )
    for idx, r in enumerate(rows):
        meta = row_meta[idx] if idx < len(row_meta) else {}
        if meta and not meta.get("kda_found", True):
            continue
        if (r.get("result") == "WIN" and win_verified) or (r.get("result") == "LOSE" and lose_verified):
            if r.get("confidence", 0.0) < 0.85:
                r["confidence"] = 0.85

    parsed["mvp"] = _pick_medal_winner(rows, "WIN", mvp_candidates)
    parsed["svp"] = _pick_medal_winner(rows, "LOSE", svp_candidates)

    warnings: list[str] = []
    if not header_found:
        warnings.append("не удалось уверенно определить VICTORY/DEFEAT по шапке")
    if len(rows) != 10:
        warnings.append(f"распознано {len(rows)} строк игроков вместо 10")
    if header_win_kills is not None and win_sum != header_win_kills:
        warnings.append(
            f"сумма киллов победителей ({win_sum}) не сходится со счётом в шапке ({header_win_kills})"
        )
    if header_lose_kills is not None and lose_sum != header_lose_kills:
        warnings.append(
            f"сумма киллов проигравших ({lose_sum}) не сходится со счётом в шапке ({header_lose_kills})"
        )
    parsed["warnings"] = warnings
    return parsed


def kda_crop_box(is_left: bool, y1: float, y2: float, width: int, height: int) -> tuple[int, int, int, int]:
    """Compute a padded crop box around the inner stats area of one scoreboard row."""
    pad_y = max(6.0, 0.012 * height)
    cy1 = max(0, int(y1 - pad_y))
    cy2 = min(height, int(y2 + pad_y))
    if cy2 <= cy1:
        cy2 = min(height, cy1 + max(16, int(0.06 * height)))
    if is_left:
        cx1 = int(0.13 * width)
        cx2 = int(0.495 * width)
    else:
        cx1 = int(0.505 * width)
        cx2 = int(0.87 * width)
    return (cx1, cy1, cx2, cy2)


def _match_title_keyword(token: str) -> Optional[str]:
    for kw in ("VICTORY", "VICTOR", "ICTORY", "ПОБЕДА", "ПОБЕД"):
        if kw in token:
            return "VICTORY"
    for kw in ("DEFEAT", "DEFEA", "EFEAT", "DFEAT", "ПОРАЖЕНИЕ", "ПОРАЖ"):
        if kw in token:
            return "DEFEAT"
    if len(token) >= 5:
        if _levenshtein(token, "VICTORY") <= 2 or _levenshtein(token, "ПОБЕДА") <= 2:
            return "VICTORY"
        if _levenshtein(token, "DEFEAT") <= 2 or _levenshtein(token, "ПОРАЖЕНИЕ") <= 2:
            return "DEFEAT"
    # MLBB's bevelled title font often loses the first letters ('CIORY', 'TORY', 'FEAI').
    if len(token) >= 4:
        if token.endswith(("ORY", "TOR")):
            return "VICTORY"
        if token.endswith(("EAT", "EAI", "FEA")):
            return "DEFEAT"
    return None


def _detect_left_won(
    boxes: list[Box], width: int, height: int, hint: Optional[str]
) -> tuple[bool, bool]:
    top_boxes = [
        b
        for b in boxes
        if b.cy <= 0.22 * height and 0.28 * width <= b.cx <= 0.72 * width
    ]
    vic_xs: list[float] = []
    def_xs: list[float] = []

    for b in top_boxes:
        clean = _strip_ui_overlay(b.text)
        if not clean:
            continue
        upper = clean.upper()
        matched_kind = _match_title_keyword(upper)
        if matched_kind is None:
            for tok in re.findall(r"[A-ZА-ЯЁ]+", upper):
                matched_kind = _match_title_keyword(tok)
                if matched_kind is not None:
                    break
        if matched_kind == "VICTORY":
            vic_xs.append(b.cx)
        elif matched_kind == "DEFEAT":
            def_xs.append(b.cx)

    if vic_xs and not def_xs:
        return True, True
    if def_xs and not vic_xs:
        return False, True
    if vic_xs and def_xs:
        return (min(vic_xs) < min(def_xs)), True

    if hint == "left":
        return True, True
    if hint == "right":
        return False, True
    return True, False


def _extract_header_kills(
    boxes: list[Box], width: int, height: int
) -> tuple[Optional[int], Optional[int]]:
    # Tier 1: Tall top-banner score digits well above Row 0.
    left_banner: list[tuple[float, float, int]] = []
    right_banner: list[tuple[float, float, int]] = []
    for b in boxes:
        if not (0.015 * height <= b.cy <= 0.155 * height):
            continue
        box_h = b.y2 - b.y1
        if box_h < 0.045 * height:
            continue
        txt = b.text.strip()
        if _TIME_RE.match(txt) or "длит" in txt.lower():
            continue
        m = re.match(r"^\d{1,2}$", txt)
        if not m:
            continue
        val = int(txt)
        if not (0 <= val <= 99):
            continue
        if 0.24 * width <= b.cx <= 0.44 * width:
            left_banner.append((-box_h, abs(b.cx - 0.34 * width), val))
        elif 0.56 * width <= b.cx <= 0.76 * width:
            right_banner.append((-box_h, abs(b.cx - 0.66 * width), val))

    if left_banner and right_banner:
        left_banner.sort()
        right_banner.sort()
        return left_banner[0][2], right_banner[0][2]

    # Tier 2: Compact header fallback (strictly above Row 0 so Row 0 KDA never leaks in).
    header_boxes = [
        b
        for b in boxes
        if 0.015 * height <= b.cy <= 0.185 * height and 0.34 * width <= b.cx <= 0.66 * width
    ]
    for b in header_boxes:
        txt = b.text.strip()
        if _TIME_RE.match(txt) or "длит" in txt.lower():
            continue
        m = _HEADER_PAIR_RE.match(txt)
        if m:
            return int(m.group(1)), int(m.group(2))

    left_candidates: list[tuple[float, int]] = []
    right_candidates: list[tuple[float, int]] = []
    for b in header_boxes:
        txt = b.text.strip()
        if _TIME_RE.match(txt) or "длит" in txt.lower():
            continue
        if not re.match(r"^\d{1,2}$", txt):
            continue
        val = int(txt)
        if not (0 <= val <= 99):
            continue
        if b.cx < 0.5 * width:
            left_candidates.append((abs(b.cx - 0.43 * width), val))
        else:
            right_candidates.append((abs(b.cx - 0.57 * width), val))

    if left_candidates and right_candidates:
        left_candidates.sort(key=lambda t: t[0])
        right_candidates.sort(key=lambda t: t[0])
        return left_candidates[0][1], right_candidates[0][1]

    return None, None


def _filter_body_boxes(boxes: list[Box], width: int, height: int) -> list[Box]:
    out: list[Box] = []
    for b in boxes:
        if b.cy < 0.18 * height or b.cy > 0.865 * height:
            continue
        clean = _strip_ui_overlay(b.text)
        if not clean:
            continue
        upper = clean.upper()
        low = clean.lower()
        if upper in HEADER_IGNORE_WORDS or low.startswith(("длит", "боево")):
            continue
        if b.cy <= 0.24 * height and 0.30 * width <= b.cx <= 0.70 * width:
            # Header score/time digits live strictly above Row 0 (real MLBB Row 0 top line sits at ~0.238*H).
            if b.cy <= 0.185 * height and (
                _TIME_RE.match(clean) or _HEADER_PAIR_RE.match(clean) or _INT_RE.match(clean)
            ):
                continue
            if _TIME_RE.match(clean):
                continue
            if _match_title_keyword(upper) is not None or any(kw in upper for kw in VICTORY_KEYWORDS + DEFEAT_KEYWORDS):
                continue
        alpha_cnt = sum(1 for ch in clean if ch.isalpha())
        if b.cy <= 0.255 * height:
            if upper in _SUBHEADER_NOISE_TOKENS:
                continue
            if (b.cx < 0.15 * width or b.cx > 0.85 * width) and alpha_cnt < 2:
                continue
        if (b.cx < 0.175 * width or b.cx > 0.825 * width) and alpha_cnt < 2:
            continue
        if 0.482 * width <= b.cx <= 0.532 * width:
            if not (_MEDAL_MVP_RE.search(clean) or _MEDAL_SVP_RE.search(clean) or _RATING_RE.match(clean)):
                continue
        out.append(Box(b.x1, b.y1, b.x2, b.y2, clean, b.score))
    return out


def _extract_gold_bounds(body_boxes: list[Box], width: int) -> dict:
    left_x1s = [
        b.x1
        for b in body_boxes
        if 0.35 * width <= b.cx <= 0.445 * width and _RAW_GOLD_RE.match(b.text)
    ]
    right_x2s = [
        b.x2
        for b in body_boxes
        if 0.555 * width <= b.cx <= 0.65 * width and _RAW_GOLD_RE.match(b.text)
    ]
    return {
        "left_x1": float(median(left_x1s)) if left_x1s else None,
        "right_x2": float(median(right_x2s)) if right_x2s else None,
    }


def _assign_five_row_bands(
    body_boxes: list[Box], width: int, height: int
) -> tuple[list[list[Box]], list[list[Box]], list[float], float]:
    anchors: list[float] = []
    for b in body_boxes:
        in_left = 0.175 * width <= b.cx <= 0.435 * width
        in_right = 0.565 * width <= b.cx <= 0.825 * width
        if not (in_left or in_right):
            continue
        txt = b.text
        alpha_cnt = sum(1 for ch in txt if ch.isalpha())
        has_gold = bool(re.search(r"(?<!\d)\d{4,6}(?!\d)", txt))
        has_slash = bool(_KDA_SLASH_RE.search(txt))
        if alpha_cnt >= 2 or has_gold or has_slash:
            anchors.append(b.cy)

    anchors.sort()
    tol = max(0.042 * height, 12.0)
    clusters: list[list[float]] = []
    for cy in anchors:
        if not clusters or abs(cy - median(clusters[-1])) > tol:
            clusters.append([cy])
        else:
            clusters[-1].append(cy)

    ys = [float(median(cl)) for cl in clusters]
    if len(ys) == 4:
        gaps = [ys[i + 1] - ys[i] for i in range(3)]
        max_idx = max(range(3), key=lambda i: gaps[i])
        other_gaps = sorted(gaps[i] for i in range(3) if i != max_idx)
        g_med = other_gaps[0] if len(other_gaps) == 1 else (other_gaps[0] + other_gaps[1]) / 2.0
        if g_med > 0 and 1.75 <= (gaps[max_idx] / g_med) <= 2.35:
            mid = (ys[max_idx] + ys[max_idx + 1]) / 2.0
            ys.insert(max_idx + 1, mid)

    if len(ys) == 5 and (ys[4] - ys[0]) / 4.0 >= 0.065 * height:
        step = (ys[4] - ys[0]) / 4.0
        bands: list[tuple[float, float]] = []
        for r in range(5):
            top_r = ys[0] - 0.35 * step if r == 0 else ys[r - 1] + 0.75 * (ys[r] - ys[r - 1])
            bot_r = ys[4] + 0.85 * step if r == 4 else ys[r] + 0.75 * (ys[r + 1] - ys[r])
            bands.append((top_r, bot_r))

        left_rows: list[list[Box]] = [[] for _ in range(5)]
        right_rows: list[list[Box]] = [[] for _ in range(5)]
        for b in body_boxes:
            target = left_rows if b.cx < 0.5 * width else right_rows
            for r, (top_r, bot_r) in enumerate(bands):
                in_band = (top_r <= b.cy <= bot_r) if r == 4 else (top_r <= b.cy < bot_r)
                if in_band:
                    target[r].append(b)
                    break
        for cl in left_rows:
            cl.sort(key=lambda b: b.x1)
        for cl in right_rows:
            cl.sort(key=lambda b: b.x1)
        return left_rows, right_rows, ys, step

    left_boxes = [b for b in body_boxes if b.cx < 0.5 * width]
    right_boxes = [b for b in body_boxes if b.cx >= 0.5 * width]
    return _cluster_five_rows(left_boxes, height), _cluster_five_rows(right_boxes, height), [], 0.0


def _cluster_five_rows(boxes: list[Box], height: int) -> list[list[Box]]:
    if not boxes:
        return []

    sorted_boxes = sorted(boxes, key=lambda b: b.cy)
    tol = max(0.045 * height, 14.0)

    clusters: list[list[Box]] = []
    for b in sorted_boxes:
        if not clusters:
            clusters.append([b])
            continue
        cur_center = median(item.cy for item in clusters[-1])
        if abs(b.cy - cur_center) <= tol:
            clusters[-1].append(b)
        else:
            clusters.append([b])

    while len(clusters) > 5:
        centers = [median(item.cy for item in cl) for cl in clusters]
        min_gap = float("inf")
        merge_idx = 0
        for idx in range(len(centers) - 1):
            gap = centers[idx + 1] - centers[idx]
            if gap < min_gap:
                min_gap = gap
                merge_idx = idx
        if min_gap > 0.08 * height and len(clusters) > 5:
            drop_idx = min(
                range(len(clusters)),
                key=lambda i: (len(clusters[i]), sum(x.score for x in clusters[i])),
            )
            clusters.pop(drop_idx)
        else:
            clusters[merge_idx].extend(clusters[merge_idx + 1])
            clusters.pop(merge_idx + 1)

    if len(clusters) < 5:
        y_min = sorted_boxes[0].cy
        y_max = sorted_boxes[-1].cy
        span = y_max - y_min
        if span >= 0.25 * height:
            step = span / 4.0
            bands: list[list[Box]] = [[] for _ in range(5)]
            for b in sorted_boxes:
                band_idx = min(4, max(0, int(round((b.cy - y_min) / step))))
                bands[band_idx].append(b)
            non_empty = [b for b in bands if b]
            if len(non_empty) > len(clusters):
                clusters = non_empty

    for cl in clusters:
        cl.sort(key=lambda b: b.x1)
    return clusters[:5]


def _expand_row_boxes(row_boxes: list[Box]) -> list[Box]:
    """Split boxes where OCR glued stats/gold/badges together, and rejoin split decimals."""
    rejoined: list[Box] = []
    i = 0
    while i < len(row_boxes):
        cur = row_boxes[i]
        if (
            i + 1 < len(row_boxes)
            and re.match(r"^\d{1,2}\.$", cur.text)
            and re.match(r"^\.?\d$", row_boxes[i + 1].text)
        ):
            nxt = row_boxes[i + 1]
            combined = cur.text + nxt.text.lstrip(".")
            rejoined.append(
                Box(cur.x1, min(cur.y1, nxt.y1), nxt.x2, max(cur.y2, nxt.y2), combined, min(cur.score, nxt.score))
            )
            i += 2
            continue
        rejoined.append(cur)
        i += 1

    expanded: list[Box] = []
    for b in rejoined:
        txt = _GLUED_BADGE_RE.sub(lambda m: f" {m.group(0)} ", b.text)
        txt = _GLUED_RATING_GOLD_RE.sub(r"\1 ", txt)
        parts = [p for p in txt.split() if p]
        if len(parts) <= 1:
            expanded.append(Box(b.x1, b.y1, b.x2, b.y2, txt.strip() or b.text, b.score))
            continue

        if not any(_STAT_HINT_TOKEN_RE.match(p) for p in parts):
            expanded.append(b)
            continue

        span = max(b.x2 - b.x1, float(len(parts)))
        total_chars = max(sum(len(p) for p in parts), 1)
        cursor = b.x1
        for p in parts:
            w = span * (len(p) / total_chars)
            expanded.append(Box(cursor, b.y1, cursor + w, b.y2, p, b.score))
            cursor += w

    return expanded


def _preprocess_glued_left_boxes(
    raw_row_boxes: list[Box], is_left: bool, width: int
) -> tuple[list[Box], bool]:
    """Split left-row boxes that bridge nickname, K/D/A, and raw 4-5 digit gold."""
    if not is_left or width <= 0:
        return raw_row_boxes, False
    out: list[Box] = []
    had_glued_kda = False
    for b in raw_row_boxes:
        if b.x1 < 0.28 * width and b.x2 > 0.37 * width:
            m_full = _GLUED_LEFT_NAME_KDA_GOLD_RE.match(b.text)
            if m_full:
                k, d, a = int(m_full.group(2)), int(m_full.group(3)), int(m_full.group(4))
                if k <= 60 and d <= 60 and a <= 60:
                    name_txt = m_full.group(1).strip()
                    if name_txt:
                        out.append(Box(b.x1, b.y1, min(b.x2, 0.25 * width), b.y2, name_txt, b.score))
                    out.append(Box(0.31 * width, b.y1, 0.37 * width, b.y2, f"{k} {d} {a}", b.score))
                    continue
            m_part = _GLUED_LEFT_NAME_PARTIAL_GOLD_RE.match(b.text) or _GLUED_LEFT_NAME_STUCK_GOLD_RE.match(b.text)
            if m_part:
                name_txt = m_part.group(1).strip()
                if name_txt:
                    out.append(Box(b.x1, b.y1, min(b.x2, 0.25 * width), b.y2, name_txt, b.score))
                had_glued_kda = True
                continue
        out.append(b)
    return out, had_glued_kda


def _parse_row(
    raw_row_boxes: list[Box],
    is_left: bool,
    width: int,
    height: int,
    side_result: str,
    anchor_y: float = 0.0,
    row_step: float = 0.0,
) -> tuple[dict, dict]:
    prep_boxes, had_glued_kda = _preprocess_glued_left_boxes(raw_row_boxes, is_left, width)
    row_boxes = _expand_row_boxes(prep_boxes)

    has_mvp = False
    has_svp = False
    rating = 0.0
    kills = 0
    deaths = 0
    assists = 0
    kda_found = False
    kda_scores: list[float] = []

    consumed_indices: set[int] = set()
    cleaned_texts: dict[int, str] = {}

    for idx, b in enumerate(row_boxes):
        txt = b.text.strip()
        if _MEDAL_MVP_RE.search(txt):
            has_mvp = True
            txt = _MEDAL_MVP_RE.sub("", txt).strip()
        if _MEDAL_SVP_RE.search(txt):
            has_svp = True
            txt = _MEDAL_SVP_RE.sub("", txt).strip()
        cleaned_texts[idx] = txt

    # Pass A: slash-formatted or 3-space-separated KDA inside a single box.
    for idx, b in enumerate(row_boxes):
        txt = cleaned_texts[idx]
        if not txt:
            continue
        m_slash = _KDA_SLASH_RE.search(txt)
        if m_slash:
            kills, deaths, assists = (
                int(m_slash.group(1)),
                int(m_slash.group(2)),
                int(m_slash.group(3)),
            )
            kda_found = True
            kda_scores.append(b.score)
            rem = _KDA_SLASH_RE.sub("", txt).strip()
            if rem:
                cleaned_texts[idx] = rem
            else:
                consumed_indices.add(idx)
            break

        m_three = _KDA_THREE_INTS_RE.match(txt)
        if m_three:
            k, d, a = int(m_three.group(1)), int(m_three.group(2)), int(m_three.group(3))
            if k <= 60 and d <= 60 and a <= 60:
                kills, deaths, assists = k, d, a
                kda_found = True
                kda_scores.append(b.score)
                consumed_indices.add(idx)
                break

    int_candidates: list[tuple[int, int, Box]] = []
    for idx, b in enumerate(row_boxes):
        if idx in consumed_indices:
            continue
        txt = cleaned_texts[idx]
        if not txt:
            consumed_indices.add(idx)
            continue
        if (
            _GOLD_RE.match(txt)
            or _GOLD_LONG_RE.match(txt)
            or _RAW_GOLD_RE.match(txt)
            or _GLUED_GOLD_TRAILING_SLASH_RE.match(txt)
        ):
            consumed_indices.add(idx)
            continue

        in_rating_band = (b.cx >= 0.40 * width) if is_left else (b.cx <= 0.60 * width)
        m_rat = _RATING_RE.match(txt)
        if m_rat and in_rating_band and rating == 0.0:
            val = float(f"{m_rat.group(1)}.{m_rat.group(2)}")
            if 1.0 <= val <= 20.0:
                rating = val
                consumed_indices.add(idx)
                continue

        if _LEVEL_RE.match(txt) and not _INT_RE.match(txt):
            consumed_indices.add(idx)
            continue

        m_int = _INT_RE.match(txt)
        if m_int:
            val = int(m_int.group(1))
            in_gold_band = (
                (0.375 * width <= b.cx <= 0.432 * width)
                if is_left
                else (0.568 * width <= b.cx <= 0.628 * width)
            )
            if val >= 1000 or (in_gold_band and val >= 100):
                consumed_indices.add(idx)
                continue
            in_kda_band = (
                (0.245 * width <= b.cx <= 0.425 * width)
                if is_left
                else (0.575 * width <= b.cx <= 0.755 * width)
            )
            if in_kda_band and 100 <= val <= 999:
                consumed_indices.add(idx)
                had_glued_kda = True
                continue
            if not in_kda_band:
                # Keep small integers inside nickname column (e.g., clan tags like '009', '33'),
                # while dropping portrait numbers in outer zone.
                in_portrait = (b.cx < 0.175 * width) if is_left else (b.cx > 0.825 * width)
                if in_portrait:
                    consumed_indices.add(idx)
                continue
            if 0 <= val <= 60:
                int_candidates.append((idx, val, b))

    if not kda_found and len(int_candidates) >= 3:
        best_triplet = int_candidates[:3]
        if len(int_candidates) > 3:
            best_span = float("inf")
            for start in range(len(int_candidates) - 2):
                trip = int_candidates[start : start + 3]
                span = trip[2][2].cx - trip[0][2].cx
                if span < best_span:
                    best_span = span
                    best_triplet = trip
        kills, deaths, assists = best_triplet[0][1], best_triplet[1][1], best_triplet[2][1]
        kda_found = True
        for idx, _, b in best_triplet:
            consumed_indices.add(idx)
            kda_scores.append(b.score)

    name_parts: list[str] = []
    kept_scores: list[float] = []
    for idx, b in enumerate(row_boxes):
        if idx in consumed_indices:
            continue
        in_nick_band = (
            (0.145 * width <= b.cx <= 0.33 * width)
            if is_left
            else (0.67 * width <= b.cx <= 0.855 * width)
        )
        if not in_nick_band:
            continue
        txt = _GENDER_SYMBOL_RE.sub("", cleaned_texts[idx]).strip()
        if not txt or _CENTER_SYMBOL_NOISE_RE.match(txt):
            continue
        if b.cx > 0.305 * width and b.cx < 0.695 * width and txt.upper() in _SUBHEADER_NOISE_TOKENS:
            continue
        name_parts.append(txt)
        kept_scores.append(b.score)

    player_name = " ".join(name_parts).strip()
    stat_and_name_scores = kept_scores + kda_scores
    if stat_and_name_scores:
        row_conf = sum(stat_and_name_scores) / len(stat_and_name_scores)
    elif raw_row_boxes:
        row_conf = sum(b.score for b in raw_row_boxes) / len(raw_row_boxes)
    else:
        row_conf = 0.0

    if not kda_found:
        row_conf = min(row_conf, 0.4)
    elif kda_scores:
        row_conf = min(row_conf, sum(kda_scores) / len(kda_scores))

    y1 = min((b.y1 for b in raw_row_boxes), default=0.0)
    y2 = max((b.y2 for b in raw_row_boxes), default=float(height))

    row_dict = {
        "player_name": player_name,
        "result": side_result,
        "kills": kills,
        "deaths": deaths,
        "assists": assists,
        "rating": round(rating, 1),
        "confidence": round(row_conf, 3),
    }
    meta = {
        "is_left": is_left,
        "y1": y1,
        "y2": y2,
        "anchor_y": anchor_y,
        "row_step": row_step,
        "kda_found": kda_found,
        "had_glued_kda": had_glued_kda,
        "kda_min_score": min(kda_scores) if kda_scores else 0.0,
        "has_mvp": has_mvp,
        "has_svp": has_svp,
    }
    return row_dict, meta


def _pick_medal_winner(
    rows: list[dict], side_result: str, candidates: list[tuple[str, float]]
) -> Optional[str]:
    if candidates:
        candidates_sorted = sorted(candidates, key=lambda c: c[1], reverse=True)
        return candidates_sorted[0][0]
    side_rows = [r for r in rows if r["result"] == side_result and r["player_name"] and r["rating"] > 0]
    if not side_rows:
        return None
    best = max(side_rows, key=lambda r: r["rating"])
    return best["player_name"]
