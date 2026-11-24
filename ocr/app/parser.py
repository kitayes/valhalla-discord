from __future__ import annotations

from dataclasses import dataclass
import re
from statistics import median
from typing import Iterable, Optional

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
}

_KDA_SLASH_RE = re.compile(r"(\d{1,2})\s*/\s*(\d{1,2})\s*/\s*(\d{1,2})")
_KDA_THREE_INTS_RE = re.compile(r"^\s*(\d{1,2})\s+(\d{1,2})\s+(\d{1,2})\s*$")
_HEADER_PAIR_RE = re.compile(r"^\s*(\d{1,2})\s*(?:vs|VS|:|-|\s+)\s*(\d{1,2})\s*$")
_TIME_RE = re.compile(r"^\s*\d{1,2}:\d{2}\s*$")
_GOLD_RE = re.compile(r"^\s*\d+(?:[.,]\d+)?\s*[kк]\s*$", re.IGNORECASE)
_GOLD_LONG_RE = re.compile(r"^\s*\d{1,2}[.,]\d{3}\s*$")
_LEVEL_RE = re.compile(r"^(?:lv\.?\s*|ур\.?\s*)?\d{1,2}$", re.IGNORECASE)
_RATING_RE = re.compile(r"^\s*(\d{1,2})[.,](\d)\s*$")
_INT_RE = re.compile(r"^\s*(\d{1,4})\s*$")
_MEDAL_MVP_RE = re.compile(r"\bMVP\b", re.IGNORECASE)
_MEDAL_SVP_RE = re.compile(r"\bSVPG?\b", re.IGNORECASE)


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


def parse_scoreboard(
    boxes: Iterable[Box],
    width: int,
    height: int,
    expected_players: Optional[list[str]] = None,
    header_side_hint: Optional[str] = None,
) -> dict:
    """Parse RapidOCR boxes from an MLBB scoreboard into structured match data."""
    box_list = [b for b in boxes if b.text and b.text.strip()]
    warnings: list[str] = []

    if width <= 0 or height <= 0:
        return {
            "players": [],
            "mvp": None,
            "svp": None,
            "header_win_kills": None,
            "header_lose_kills": None,
            "warnings": ["некорректные размеры изображения"],
            "_row_meta": [],
        }

    left_won, header_found = _detect_left_won(box_list, width, height, header_side_hint)
    if not header_found:
        warnings.append("не удалось уверенно определить VICTORY/DEFEAT по шапке")

    left_header_kills, right_header_kills = _extract_header_kills(box_list, width, height)
    if left_header_kills is not None and right_header_kills is not None:
        header_win_kills = left_header_kills if left_won else right_header_kills
        header_lose_kills = right_header_kills if left_won else left_header_kills
    else:
        header_win_kills = None
        header_lose_kills = None

    body_boxes = _filter_body_boxes(box_list, width, height)
    left_boxes = [b for b in body_boxes if b.cx < 0.5 * width]
    right_boxes = [b for b in body_boxes if b.cx >= 0.5 * width]

    left_rows = _cluster_five_rows(left_boxes, height)
    right_rows = _cluster_five_rows(right_boxes, height)

    parsed_rows: list[dict] = []
    row_meta: list[dict] = []
    mvp_candidates: list[tuple[str, float]] = []
    svp_candidates: list[tuple[str, float]] = []

    def process_side(rows: list[list[Box]], is_left: bool) -> None:
        side_result = "WIN" if (is_left == left_won) else "LOSE"
        for idx, row_boxes in enumerate(rows):
            row_data, meta = _parse_row(row_boxes, is_left, width, height, side_result)
            row_data["_row_idx"] = len(parsed_rows)
            meta["row_idx"] = len(parsed_rows)
            parsed_rows.append(row_data)
            row_meta.append(meta)

            if meta["has_mvp"] and side_result == "WIN" and row_data["player_name"]:
                mvp_candidates.append((row_data["player_name"], row_data["rating"]))
            if meta["has_svp"] and side_result == "LOSE" and row_data["player_name"]:
                svp_candidates.append((row_data["player_name"], row_data["rating"]))

    process_side(left_rows, True)
    process_side(right_rows, False)

    mvp_name = _pick_medal_winner(parsed_rows, "WIN", mvp_candidates)
    svp_name = _pick_medal_winner(parsed_rows, "LOSE", svp_candidates)

    for r in parsed_rows:
        r.pop("_row_idx", None)

    if len(parsed_rows) != 10:
        warnings.append(f"распознано {len(parsed_rows)} строк игроков вместо 10")

    win_sum = sum(r["kills"] for r in parsed_rows if r["result"] == "WIN")
    lose_sum = sum(r["kills"] for r in parsed_rows if r["result"] == "LOSE")
    if header_win_kills is not None and win_sum != header_win_kills:
        warnings.append(
            f"сумма киллов победителей ({win_sum}) не сходится со счётом в шапке ({header_win_kills})"
        )
    if header_lose_kills is not None and lose_sum != header_lose_kills:
        warnings.append(
            f"сумма киллов проигравших ({lose_sum}) не сходится со счётом в шапке ({header_lose_kills})"
        )

    return {
        "players": parsed_rows,
        "mvp": mvp_name,
        "svp": svp_name,
        "header_win_kills": header_win_kills,
        "header_lose_kills": header_lose_kills,
        "warnings": warnings,
        "_row_meta": row_meta,
    }


def kda_crop_box(is_left: bool, y1: float, y2: float, width: int, height: int) -> tuple[int, int, int, int]:
    """Compute a padded crop box around the inner stats area of one scoreboard row."""
    pad_y = max(6.0, 0.012 * height)
    cy1 = max(0, int(y1 - pad_y))
    cy2 = min(height, int(y2 + pad_y))
    if cy2 <= cy1:
        cy2 = min(height, cy1 + max(16, int(0.06 * height)))
    if is_left:
        cx1 = int(0.14 * width)
        cx2 = int(0.49 * width)
    else:
        cx1 = int(0.51 * width)
        cx2 = int(0.86 * width)
    return (cx1, cy1, cx2, cy2)


def _detect_left_won(
    boxes: list[Box], width: int, height: int, hint: Optional[str]
) -> tuple[bool, bool]:
    top_boxes = [b for b in boxes if b.cy <= 0.25 * height]
    vic_xs: list[float] = []
    def_xs: list[float] = []

    for b in top_boxes:
        upper = b.text.strip().upper()
        if any(kw in upper for kw in VICTORY_KEYWORDS):
            vic_xs.append(b.cx)
        if any(kw in upper for kw in DEFEAT_KEYWORDS):
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
    header_boxes = [
        b
        for b in boxes
        if 0.01 * height <= b.cy <= 0.25 * height and 0.25 * width <= b.cx <= 0.75 * width
    ]
    for b in header_boxes:
        txt = b.text.strip()
        if _TIME_RE.match(txt):
            continue
        m = _HEADER_PAIR_RE.match(txt)
        if m:
            return int(m.group(1)), int(m.group(2))

    left_candidates: list[tuple[float, int]] = []
    right_candidates: list[tuple[float, int]] = []
    for b in header_boxes:
        txt = b.text.strip()
        if _TIME_RE.match(txt):
            continue
        m = _INT_RE.match(txt)
        if not m:
            continue
        val = int(m.group(1))
        if not (0 <= val <= 99):
            continue
        if b.cx < 0.5 * width:
            left_candidates.append((abs(b.cx - 0.44 * width), val))
        else:
            right_candidates.append((abs(b.cx - 0.56 * width), val))

    if left_candidates and right_candidates:
        left_candidates.sort(key=lambda t: t[0])
        right_candidates.sort(key=lambda t: t[0])
        return left_candidates[0][1], right_candidates[0][1]

    return None, None


def _filter_body_boxes(boxes: list[Box], width: int, height: int) -> list[Box]:
    out: list[Box] = []
    for b in boxes:
        if b.cy < 0.17 * height or b.cy > 0.95 * height:
            continue
        clean = b.text.strip()
        upper = clean.upper()
        if upper in HEADER_IGNORE_WORDS:
            continue
        if b.cy <= 0.24 * height and 0.30 * width <= b.cx <= 0.70 * width:
            if _TIME_RE.match(clean) or _HEADER_PAIR_RE.match(clean) or _INT_RE.match(clean):
                continue
            if any(kw in upper for kw in VICTORY_KEYWORDS + DEFEAT_KEYWORDS):
                continue
        out.append(b)
    return out


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

    # Merge overly close clusters when more than 5 exist.
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
            # Drop the sparsest outlier cluster (e.g. footer noise)
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


def _parse_row(
    row_boxes: list[Box], is_left: bool, width: int, height: int, side_result: str
) -> tuple[dict, dict]:
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

    # Filter gold and hero-level portrait badges before looking for split integer K/D/A boxes.
    int_candidates: list[tuple[int, int, Box]] = []
    for idx, b in enumerate(row_boxes):
        if idx in consumed_indices:
            continue
        txt = cleaned_texts[idx]
        if not txt:
            consumed_indices.add(idx)
            continue
        if _GOLD_RE.match(txt) or _GOLD_LONG_RE.match(txt):
            consumed_indices.add(idx)
            continue

        # Rating float like "10.4" or "8,7"
        m_rat = _RATING_RE.match(txt)
        if m_rat and rating == 0.0:
            val = float(f"{m_rat.group(1)}.{m_rat.group(2)}")
            if 1.0 <= val <= 20.0:
                rating = val
                consumed_indices.add(idx)
                continue

        # Explicit Lv.15 / Ур.15
        if _LEVEL_RE.match(txt) and not _INT_RE.match(txt):
            consumed_indices.add(idx)
            continue

        m_int = _INT_RE.match(txt)
        if m_int:
            val = int(m_int.group(1))
            if val >= 100:
                consumed_indices.add(idx)  # Gold without 'k'
                continue
            # Hero level inside outer portrait edge (<=15)
            in_portrait = (b.cx < 0.14 * width) if is_left else (b.cx > 0.86 * width)
            if in_portrait and val <= 15:
                consumed_indices.add(idx)
                continue
            if 0 <= val <= 60:
                int_candidates.append((idx, val, b))

    if not kda_found and len(int_candidates) >= 3:
        # Take the 3 closest-grouped integers in x-order.
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
    name_scores: list[float] = []
    for idx, b in enumerate(row_boxes):
        if idx in consumed_indices:
            continue
        txt = cleaned_texts[idx].strip()
        if not txt:
            continue
        name_parts.append(txt)
        name_scores.append(b.score)

    player_name = " ".join(name_parts).strip()
    all_scores = [b.score for b in row_boxes] if row_boxes else [0.0]
    row_conf = sum(all_scores) / len(all_scores)
    if not kda_found:
        row_conf = min(row_conf, 0.4)
    elif kda_scores:
        row_conf = min(row_conf, min(kda_scores))

    y1 = min((b.y1 for b in row_boxes), default=0.0)
    y2 = max((b.y2 for b in row_boxes), default=float(height))

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
        "kda_found": kda_found,
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
