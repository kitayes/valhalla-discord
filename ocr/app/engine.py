from __future__ import annotations

import threading
from typing import Optional

import cv2
import numpy as np
from rapidocr_onnxruntime import RapidOCR

from app.parser import Box, kda_crop_box, parse_scoreboard

_ocr_lock = threading.Lock()
_ocr_instance: Optional[RapidOCR] = None


def get_ocr() -> RapidOCR:
    global _ocr_instance
    if _ocr_instance is None:
        with _ocr_lock:
            if _ocr_instance is None:
                _ocr_instance = RapidOCR()
    return _ocr_instance


def preprocess_kda_crop(img_bgr: np.ndarray) -> np.ndarray:
    """Upscale and enhance local contrast of a small K/D/A crop for Pass-2 OCR."""
    upscaled = cv2.resize(img_bgr, None, fx=3.0, fy=3.0, interpolation=cv2.INTER_CUBIC)
    gray = cv2.cvtColor(upscaled, cv2.COLOR_BGR2GRAY)
    clahe = cv2.createCLAHE(clipLimit=2.5, tileGridSize=(8, 8))
    enhanced = clahe.apply(gray)
    return cv2.cvtColor(enhanced, cv2.COLOR_GRAY2BGR)


def detect_header_winner_hint(img_bgr: np.ndarray) -> Optional[str]:
    """Use blue-vs-red banner hue in the top header as a fallback VICTORY/DEFEAT hint."""
    height, width = img_bgr.shape[:2]
    if height < 20 or width < 20:
        return None
    y1, y2 = int(0.02 * height), int(0.18 * height)
    x1, x2 = int(0.30 * width), int(0.70 * width)
    crop = img_bgr[y1:y2, x1:x2]
    if crop.size == 0:
        return None
    blue_mean = float(np.mean(crop[:, :, 0]))
    red_mean = float(np.mean(crop[:, :, 2]))
    if blue_mean - red_mean >= 18.0:
        return "left"
    if red_mean - blue_mean >= 18.0:
        return "right"
    return None


def _rapidocr_to_boxes(raw_result, offset_x: float = 0.0, offset_y: float = 0.0, scale: float = 1.0) -> list[Box]:
    boxes: list[Box] = []
    if not raw_result:
        return boxes
    for item in raw_result:
        if not item or len(item) < 3:
            continue
        quad, text, score = item[0], item[1], float(item[2])
        xs = [float(pt[0]) / scale + offset_x for pt in quad]
        ys = [float(pt[1]) / scale + offset_y for pt in quad]
        boxes.append(
            Box(
                x1=min(xs),
                y1=min(ys),
                x2=max(xs),
                y2=max(ys),
                text=str(text),
                score=score,
            )
        )
    return boxes


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
    ocr = get_ocr()

    pass1_raw, _ = ocr(img_bgr)
    boxes = _rapidocr_to_boxes(pass1_raw)
    hint = detect_header_winner_hint(img_bgr)
    parsed = parse_scoreboard(
        boxes,
        width,
        height,
        expected_players=expected_players,
        header_side_hint=hint,
    )

    # Pass 2: re-OCR crops for rows with missing or low-confidence K/D/A digits.
    crop_rects: list[tuple[int, int, int, int]] = []
    extra_boxes: list[Box] = []
    re_ocr_needed = [
        meta
        for meta in parsed.get("_row_meta", [])
        if not meta["kda_found"] or meta["kda_min_score"] < 0.80
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
            pass2_raw, _ = ocr(enhanced)
            crop_boxes = _rapidocr_to_boxes(
                pass2_raw, offset_x=float(cx1), offset_y=float(cy1), scale=3.0
            )
            if crop_boxes:
                crop_rects.append((cx1, cy1, cx2, cy2))
                extra_boxes.extend(crop_boxes)

        if extra_boxes:
            def in_reocr_crop(b: Box) -> bool:
                txt = b.text.strip()
                looks_numeric = any(ch.isdigit() for ch in txt) and len(txt) <= 10
                if not looks_numeric:
                    return False
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
            if len(reparsed.get("warnings", [])) <= len(parsed.get("warnings", [])):
                parsed = reparsed

    parsed.pop("_row_meta", None)
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
