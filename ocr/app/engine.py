from __future__ import annotations

import re
import threading
from typing import Optional

import cv2
import numpy as np
from rapidocr import LangRec, ModelType, OCRVersion, RapidOCR

from app.parser import (
    _RATING_RE,
    Box,
    kda_crop_box,
    parse_scoreboard,
    recompute_scoreboard_summary,
)

_det_ocr_lock = threading.Lock()
_det_ocr_instance: Optional[RapidOCR] = None
_rec_ocr_lock = threading.Lock()
_rec_ocr_instance: Optional[RapidOCR] = None

_CELL_DIGIT_TRANSLATION = str.maketrans(
    {"з": "3", "З": "3", "о": "0", "О": "0", "O": "0", "o": "0", "l": "1", "I": "1", "|": "1", "б": "6"}
)


def get_det_ocr() -> RapidOCR:
    """Return singleton RapidOCR instance configured for full-frame detection + recognition."""
    global _det_ocr_instance
    if _det_ocr_instance is None:
        with _det_ocr_lock:
            if _det_ocr_instance is None:
                _det_ocr_instance = RapidOCR(
                    params={
                        "Global.use_det": True,
                        "Global.use_cls": False,
                        "Global.use_rec": True,
                        "Det.limit_side_len": 1920,
                        "Det.limit_type": "max",
                        "Det.unclip_ratio": 1.15,
                        "Det.box_thresh": 0.4,
                        "Det.thresh": 0.25,
                        "Rec.ocr_version": OCRVersion.PPOCRV5,
                        "Rec.lang_type": LangRec.ESLAV,
                        "Rec.model_type": ModelType.MOBILE,
                    }
                )
    return _det_ocr_instance


def get_rec_ocr() -> RapidOCR:
    """Return separate singleton RapidOCR instance for recognition-only cell crops."""
    global _rec_ocr_instance
    if _rec_ocr_instance is None:
        with _rec_ocr_lock:
            if _rec_ocr_instance is None:
                _rec_ocr_instance = RapidOCR(
                    params={
                        "Global.use_det": False,
                        "Global.use_cls": False,
                        "Global.use_rec": True,
                        "Rec.ocr_version": OCRVersion.PPOCRV5,
                        "Rec.lang_type": LangRec.ESLAV,
                        "Rec.model_type": ModelType.MOBILE,
                    }
                )
    return _rec_ocr_instance


def preprocess_kda_crop(img_bgr: np.ndarray) -> np.ndarray:
    """Upscale and enhance local contrast of a small K/D/A crop for Pass-2 OCR."""
    upscaled = cv2.resize(img_bgr, None, fx=3.0, fy=3.0, interpolation=cv2.INTER_CUBIC)
    gray = cv2.cvtColor(upscaled, cv2.COLOR_BGR2GRAY)
    clahe = cv2.createCLAHE(clipLimit=2.5, tileGridSize=(8, 8))
    enhanced = clahe.apply(gray)
    return cv2.cvtColor(enhanced, cv2.COLOR_GRAY2BGR)


def detect_header_winner_hint(img_bgr: np.ndarray) -> Optional[str]:
    """Detect gold VICTORY ('left') vs silver/grey DEFEAT ('right') title letters in the top banner.

    MLBB keeps the blue-left / red-right banner on both outcomes, so only the
    title letter colour is reliable: VICTORY is gold, DEFEAT is metallic silver.
    """
    height, width = img_bgr.shape[:2]
    if height < 20 or width < 20:
        return None
    y1, y2 = int(0.035 * height), int(0.15 * height)
    x1, x2 = int(0.40 * width), int(0.60 * width)
    crop = img_bgr[y1:y2, x1:x2]
    if crop.size == 0:
        return None

    px = crop.astype(np.int32)
    b_chan, g_chan, r_chan = px[:, :, 0], px[:, :, 1], px[:, :, 2]
    max_chan = px.max(axis=2)
    total = float(px.shape[0] * px.shape[1])
    gold_frac = float(np.count_nonzero(((r_chan - b_chan) >= 50) & ((g_chan - b_chan) >= 25) & (max_chan >= 140))) / total
    silver_frac = float(
        np.count_nonzero((np.abs(r_chan - b_chan) <= 22) & (np.abs(g_chan - b_chan) <= 18) & (max_chan >= 150))
    ) / total

    if gold_frac >= 0.04 and gold_frac >= silver_frac:
        return "left"
    if gold_frac <= 0.005 and silver_frac >= 0.03:
        return "right"
    return None


def _rapidocr_to_boxes(
    ocr_output, offset_x: float = 0.0, offset_y: float = 0.0, scale: float = 1.0
) -> list[Box]:
    boxes: list[Box] = []
    if (
        ocr_output is None
        or ocr_output.boxes is None
        or ocr_output.txts is None
        or ocr_output.scores is None
    ):
        return boxes
    for quad, text, score in zip(ocr_output.boxes, ocr_output.txts, ocr_output.scores):
        xs = [float(pt[0]) / scale + offset_x for pt in quad]
        ys = [float(pt[1]) / scale + offset_y for pt in quad]
        boxes.append(
            Box(
                x1=min(xs),
                y1=min(ys),
                x2=max(xs),
                y2=max(ys),
                text=str(text),
                score=float(score),
            )
        )
    return boxes


def _clean_cell_int(text: str) -> Optional[int]:
    translated = (text or "").translate(_CELL_DIGIT_TRANSLATION)
    m = re.search(r"\d{1,2}", translated)
    if not m:
        return None
    val = int(m.group(0))
    return val if 0 <= val <= 60 else None


def _recognize_cell_crop(rec_ocr: RapidOCR, crop_bgr: np.ndarray) -> tuple[str, float]:
    if crop_bgr.size == 0:
        return "", 0.0
    upscaled = cv2.resize(crop_bgr, None, fx=3.0, fy=3.0, interpolation=cv2.INTER_CUBIC)
    padded = cv2.copyMakeBorder(upscaled, 8, 8, 12, 12, cv2.BORDER_REPLICATE)
    out = rec_ocr(padded)
    if out is None or not out.txts or not out.scores:
        return "", 0.0
    return str(out.txts[0]), float(out.scores[0])


def _kda_col_ranges(
    is_left: bool, width: int, height: int, gold_bounds: dict
) -> list[tuple[float, float]]:
    if height > 0 and (width / height) < 1.7:
        if is_left:
            return [(0.304 * width, 0.328 * width), (0.328 * width, 0.350 * width), (0.350 * width, 0.376 * width)]
        return [(0.625 * width, 0.650 * width), (0.650 * width, 0.672 * width), (0.672 * width, 0.698 * width)]

    col_w = 0.0202 * width
    if is_left:
        lg_x1 = gold_bounds.get("left_x1")
        if lg_x1 is None:
            lg_x1 = 0.373 * width
        la2 = lg_x1 - 0.004 * width
        return [
            (la2 - 3 * col_w - 2, la2 - 2 * col_w + 1),
            (la2 - 2 * col_w - 1, la2 - col_w + 1),
            (la2 - col_w - 1, la2 + 2),
        ]

    rg_x2 = gold_bounds.get("right_x2")
    if rg_x2 is None:
        rg_x2 = 0.626 * width
    rk1 = rg_x2 + 0.004 * width
    return [
        (rk1 - 2, rk1 + col_w + 1),
        (rk1 + col_w - 1, rk1 + 2 * col_w + 1),
        (rk1 + 2 * col_w - 1, rk1 + 3 * col_w + 3),
    ]


def _recover_side_kda_cells(
    img_bgr: np.ndarray,
    rec_ocr: RapidOCR,
    parsed: dict,
    side_indices: list[int],
    is_left: bool,
    width: int,
    height: int,
) -> None:
    rows = parsed.get("players", [])
    metas = parsed.get("_row_meta", [])
    gold_bounds = parsed.get("_gold_bounds", {})
    if any(idx >= len(rows) or idx >= len(metas) for idx in side_indices):
        return

    side_rows = [rows[idx] for idx in side_indices]
    side_meta = [metas[idx] for idx in side_indices]
    if not all(m.get("anchor_y", 0.0) > 0 for m in side_meta):
        return

    side_result = side_rows[0].get("result")
    header_kills = (
        parsed.get("header_win_kills") if side_result == "WIN" else parsed.get("header_lose_kills")
    )
    pass1_sum = sum(r["kills"] for r in side_rows)
    needs_side_rec = (
        (header_kills is not None and pass1_sum != header_kills)
        or any(
            not m.get("kda_found")
            or m.get("had_glued_kda")
            or m.get("kda_min_score", 0.0) < 0.85
            or (header_kills is not None and r["assists"] > max(header_kills - r["kills"], 0))
            for r, m in zip(side_rows, side_meta)
        )
    )
    if not needs_side_rec:
        return

    cols = _kda_col_ranges(is_left, width, height, gold_bounds)
    dy = max(8, int(0.021 * height if height < 600 else 0.017 * height))

    cell_results: list[tuple[Optional[int], Optional[int], Optional[int], list[float]]] = []
    for m in side_meta:
        yc = m["anchor_y"]
        y1 = max(0, int(yc - dy))
        y2 = min(height, int(yc + dy))
        vals: list[Optional[int]] = []
        scores: list[float] = []
        for cx1_f, cx2_f in cols:
            cx1 = max(0, int(cx1_f))
            cx2 = min(width, int(cx2_f))
            txt, sc = _recognize_cell_crop(rec_ocr, img_bgr[y1:y2, cx1:cx2])
            vals.append(_clean_cell_int(txt))
            scores.append(sc)
        cell_results.append((vals[0], vals[1], vals[2], scores))

    all_valid = all(k is not None and d is not None and a is not None for k, d, a, _ in cell_results)
    cell_sum = sum(k for k, _, _, _ in cell_results if k is not None)
    if all_valid and header_kills is not None and cell_sum == header_kills:
        for r, m, (k, d, a, scores) in zip(side_rows, side_meta, cell_results):
            assert k is not None and d is not None and a is not None
            r["kills"], r["deaths"], r["assists"] = k, d, a
            m["kda_found"] = True
            avg_sc = sum(scores) / len(scores) if scores else 0.85
            r["confidence"] = round(max(r.get("confidence", 0.0), min(avg_sc, 0.99)), 3)
        return

    for r, m, (k, d, a, scores) in zip(side_rows, side_meta, cell_results):
        if k is None or d is None or a is None:
            continue
        if not m.get("kda_found") or m.get("had_glued_kda"):
            r["kills"], r["deaths"], r["assists"] = k, d, a
            m["kda_found"] = True
            avg_sc = sum(scores) / len(scores) if scores else 0.85
            r["confidence"] = round(max(r.get("confidence", 0.0), min(avg_sc, 0.99)), 3)


def _recover_missing_ratings(
    img_bgr: np.ndarray,
    rec_ocr: RapidOCR,
    parsed: dict,
    width: int,
    height: int,
) -> None:
    rows = parsed.get("players", [])
    metas = parsed.get("_row_meta", [])
    for r, m in zip(rows, metas):
        if r.get("rating", 0.0) > 0.0:
            continue
        ay = m.get("anchor_y", 0.0)
        step = m.get("row_step", 0.0)
        if ay <= 0 or step <= 0:
            continue
        cx1 = int((0.433 if m.get("is_left") else 0.525) * width)
        cx2 = int((0.488 if m.get("is_left") else 0.578) * width)
        cy1 = max(0, int(ay + 0.30 * step))
        cy2 = min(height, int(ay + 0.76 * step))
        txt, _ = _recognize_cell_crop(rec_ocr, img_bgr[cy1:cy2, cx1:cx2])
        m_rat = _RATING_RE.match(txt.strip())
        if m_rat:
            val = float(f"{m_rat.group(1)}.{m_rat.group(2)}")
            if 1.0 <= val <= 20.0:
                r["rating"] = round(val, 1)


def analyze_image(
    image_bytes: bytes,
    expected_players: Optional[list[str]] = None,
    debug: bool = False,
) -> dict:
    arr = np.frombuffer(image_bytes, dtype=np.uint8)
    img_bgr = cv2.imdecode(arr, cv2.IMREAD_COLOR)
    if img_bgr is None:
        raise ValueError("unable to decode image bytes")

    height, width = img_bgr.shape[:2]
    det_ocr = get_det_ocr()
    rec_ocr = get_rec_ocr()

    scale = 2.0 if min(height, width) < 900 else 1.0
    det_in = (
        cv2.resize(img_bgr, None, fx=scale, fy=scale, interpolation=cv2.INTER_CUBIC)
        if scale != 1.0
        else img_bgr
    )

    pass1_raw = det_ocr(det_in)
    boxes = _rapidocr_to_boxes(pass1_raw, scale=scale)
    hint = detect_header_winner_hint(img_bgr)
    parsed = parse_scoreboard(
        boxes,
        width,
        height,
        expected_players=expected_players,
        header_side_hint=hint,
    )

    row_meta = parsed.get("_row_meta", [])
    if len(parsed.get("players", [])) == 10 and all(m.get("anchor_y", 0.0) > 0 for m in row_meta):
        _recover_side_kda_cells(img_bgr, rec_ocr, parsed, list(range(0, 5)), True, width, height)
        _recover_side_kda_cells(img_bgr, rec_ocr, parsed, list(range(5, 10)), False, width, height)
        _recover_missing_ratings(img_bgr, rec_ocr, parsed, width, height)
        recompute_scoreboard_summary(parsed)
    else:
        # Unanchored layout fallback: re-OCR wide row crops for low-confidence/missing K/D/A.
        crop_rects: list[tuple[int, int, int, int]] = []
        extra_boxes: list[Box] = []
        re_ocr_needed = [
            meta
            for meta in row_meta
            if not meta["kda_found"] or meta["kda_min_score"] < 0.78
        ]
        if re_ocr_needed:
            for meta in re_ocr_needed:
                cx1, cy1, cx2, cy2 = kda_crop_box(
                    meta["is_left"], meta["y1"], meta["y2"], width, height
                )
                crop = img_bgr[cy1:cy2, cx1:cx2]
                if crop.size == 0:
                    continue
                enhanced = preprocess_kda_crop(crop)
                pass2_raw = det_ocr(enhanced)
                crop_boxes = _rapidocr_to_boxes(
                    pass2_raw, offset_x=float(cx1), offset_y=float(cy1), scale=3.0
                )
                if crop_boxes:
                    crop_rects.append((cx1, cy1, cx2, cy2))
                    extra_boxes.extend(crop_boxes)

            if extra_boxes:
                def in_reocr_crop(b: Box) -> bool:
                    return any(
                        rx1 <= b.cx <= rx2 and ry1 <= b.cy <= ry2
                        for rx1, ry1, rx2, ry2 in crop_rects
                    )

                merged = [b for b in boxes if not in_reocr_crop(b)] + extra_boxes
                reparsed = parse_scoreboard(
                    merged,
                    width,
                    height,
                    expected_players=expected_players,
                    header_side_hint=hint,
                )
                orig_players = parsed.get("players", [])
                new_players = reparsed.get("players", [])
                if len(orig_players) == len(new_players):
                    for orig_p, new_p in zip(orig_players, new_players):
                        if not new_p.get("player_name") and orig_p.get("player_name"):
                            new_p["player_name"] = orig_p["player_name"]
                if len(reparsed.get("warnings", [])) <= len(parsed.get("warnings", [])):
                    parsed = reparsed

    parsed.pop("_row_meta", None)
    parsed.pop("_gold_bounds", None)
    parsed.pop("_header_found", None)
    if debug:
        parsed["raw_boxes"] = [
            {
                "x1": round(b.x1, 1),
                "y1": round(b.y1, 1),
                "x2": round(b.x2, 1),
                "y2": round(b.y2, 1),
                "text": b.text,
                "score": round(b.score, 3),
            }
            for b in boxes
        ]
    return parsed
