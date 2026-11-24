import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from app.parser import Box, kda_crop_box, parse_scoreboard  # noqa: E402


def _make_row_boxes(
    y_center: float,
    width: int,
    is_left: bool,
    name: str,
    kda_mode: str,
    k: int,
    d: int,
    a: int,
    rating: float,
    gold: str = "11.2k",
    hero_lvl: int = 15,
    badge: str = "",
) -> list[Box]:
    boxes: list[Box] = []
    y1, y2 = y_center - 16, y_center + 16
    if is_left:
        boxes.append(Box(0.08 * width, y1 + 8, 0.10 * width, y2, str(hero_lvl), 0.99))
        name_text = f"{name} {badge}".strip() if badge and kda_mode == "badge_inline" else name
        boxes.append(Box(0.16 * width, y1, 0.25 * width, y2, name_text, 0.96))
        if kda_mode == "slash":
            boxes.append(Box(0.27 * width, y1, 0.34 * width, y2, f"{k}/{d}/{a}", 0.95))
        elif kda_mode == "space":
            boxes.append(Box(0.27 * width, y1, 0.34 * width, y2, f"{k} {d} {a}", 0.95))
        else:
            boxes.append(Box(0.27 * width, y1, 0.29 * width, y2, str(k), 0.95))
            boxes.append(Box(0.30 * width, y1, 0.32 * width, y2, str(d), 0.94))
            boxes.append(Box(0.33 * width, y1, 0.35 * width, y2, str(a), 0.96))
        boxes.append(Box(0.37 * width, y1, 0.41 * width, y2, gold, 0.97))
        boxes.append(Box(0.43 * width, y1, 0.46 * width, y2, f"{rating:.1f}", 0.98))
        if badge and kda_mode != "badge_inline":
            boxes.append(Box(0.46 * width, y1, 0.48 * width, y2, badge, 0.95))
    else:
        if badge:
            boxes.append(Box(0.51 * width, y1, 0.53 * width, y2, badge, 0.95))
        boxes.append(Box(0.54 * width, y1, 0.57 * width, y2, f"{rating:.1f}", 0.98))
        boxes.append(Box(0.59 * width, y1, 0.63 * width, y2, gold, 0.97))
        if kda_mode == "slash":
            boxes.append(Box(0.65 * width, y1, 0.72 * width, y2, f"{k}/{d}/{a}", 0.95))
        elif kda_mode == "space":
            boxes.append(Box(0.65 * width, y1, 0.72 * width, y2, f"{k} {d} {a}", 0.95))
        else:
            boxes.append(Box(0.65 * width, y1, 0.67 * width, y2, str(k), 0.95))
            boxes.append(Box(0.68 * width, y1, 0.70 * width, y2, str(d), 0.94))
            boxes.append(Box(0.71 * width, y1, 0.73 * width, y2, str(a), 0.96))
        boxes.append(Box(0.75 * width, y1, 0.84 * width, y2, name, 0.96))
        boxes.append(Box(0.90 * width, y1 + 8, 0.92 * width, y2, str(hero_lvl), 0.99))
    return boxes


class ParserTests(unittest.TestCase):
    def test_en_victory_16_9_separate_digit_boxes(self):
        w, h = 1920, 1080
        boxes = [
            Box(0.44 * w, 0.04 * h, 0.56 * w, 0.10 * h, "VICTORY", 0.99),
            Box(0.42 * w, 0.12 * h, 0.45 * w, 0.16 * h, "20", 0.99),
            Box(0.48 * w, 0.12 * h, 0.52 * w, 0.16 * h, "12:34", 0.99),
            Box(0.55 * w, 0.12 * h, 0.58 * w, 0.16 * h, "12", 0.99),
        ]
        left_data = [
            ("Bjorn", 8, 2, 6, 11.4, "MVP"),
            ("Thorin", 5, 2, 7, 9.2, ""),
            ("Freya", 3, 3, 9, 8.5, ""),
            ("Бьорн", 2, 2, 10, 8.0, ""),
            ("Kael", 2, 3, 8, 7.6, ""),
        ]
        right_data = [
            ("Gunnar", 6, 3, 4, 8.9, "SVPG"),
            ("Loki", 2, 4, 5, 6.1, ""),
            ("Astrid", 2, 5, 4, 5.8, ""),
            ("Soren", 1, 4, 6, 5.2, ""),
            ("Ragnar", 1, 4, 5, 4.9, ""),
        ]
        row_ys = [0.26 * h, 0.38 * h, 0.50 * h, 0.62 * h, 0.74 * h]
        for idx, (n, k, d, a, rat, badge) in enumerate(left_data):
            boxes.extend(_make_row_boxes(row_ys[idx], w, True, n, "split", k, d, a, rat, badge=badge))
        for idx, (n, k, d, a, rat, badge) in enumerate(right_data):
            boxes.extend(_make_row_boxes(row_ys[idx], w, False, n, "split", k, d, a, rat, badge=badge))

        out = parse_scoreboard(boxes, w, h)
        self.assertEqual(len(out["players"]), 10)
        self.assertEqual(out["header_win_kills"], 20)
        self.assertEqual(out["header_lose_kills"], 12)
        self.assertEqual(out["mvp"], "Bjorn")
        self.assertEqual(out["svp"], "Gunnar")
        self.assertEqual(out["warnings"], [])
        self.assertEqual(out["players"][0]["result"], "WIN")
        self.assertEqual((out["players"][0]["kills"], out["players"][0]["deaths"], out["players"][0]["assists"]), (8, 2, 6))
        self.assertEqual(out["players"][5]["result"], "LOSE")
        self.assertEqual(out["players"][3]["player_name"], "Бьорн")

    def test_ru_defeat_20_9_slash_kda_and_rating_medal_fallback(self):
        w, h = 2400, 1080
        boxes = [
            Box(0.42 * w, 0.04 * h, 0.58 * w, 0.10 * h, "ПОРАЖЕНИЕ", 0.99),
            Box(0.45 * w, 0.12 * h, 0.55 * w, 0.16 * h, "10 vs 18", 0.98),
        ]
        # Left team lost (10 kills), right team won (18 kills); no MVP/SVP text badges -> fallback to max rating!
        left_data = [
            ("Алиса", 5, 3, 2, 9.5),
            ("Борис", 2, 4, 3, 6.2),
            ("Виктор", 1, 4, 4, 5.8),
            ("Глеб", 1, 4, 5, 5.5),
            ("Дима", 1, 3, 3, 5.1),
        ]
        right_data = [
            ("Егор", 10, 1, 5, 12.1),
            ("Женя", 3, 2, 7, 8.8),
            ("Игорь", 2, 2, 8, 8.2),
            ("Коля", 2, 2, 6, 7.4),
            ("Макс", 1, 3, 9, 7.1),
        ]
        row_ys = [0.25 * h, 0.37 * h, 0.49 * h, 0.61 * h, 0.73 * h]
        for idx, (n, k, d, a, rat) in enumerate(left_data):
            boxes.extend(_make_row_boxes(row_ys[idx], w, True, n, "slash", k, d, a, rat, gold="8,450"))
        for idx, (n, k, d, a, rat) in enumerate(right_data):
            boxes.extend(_make_row_boxes(row_ys[idx], w, False, n, "space", k, d, a, rat, gold="12.4к"))

        out = parse_scoreboard(boxes, w, h)
        self.assertEqual(len(out["players"]), 10)
        self.assertEqual(out["header_win_kills"], 18)
        self.assertEqual(out["header_lose_kills"], 10)
        self.assertEqual(out["players"][0]["result"], "LOSE")
        self.assertEqual(out["players"][5]["result"], "WIN")
        self.assertEqual(out["mvp"], "Егор")
        self.assertEqual(out["svp"], "Алиса")
        self.assertEqual(out["warnings"], [])

    def test_header_kill_mismatch_adds_warning_and_crop_bounds(self):
        w, h = 1280, 720
        boxes = [
            Box(0.45 * w, 0.05 * h, 0.55 * w, 0.10 * h, "VICTORY", 0.99),
            Box(0.42 * w, 0.12 * h, 0.46 * w, 0.16 * h, "25", 0.99),
            Box(0.54 * w, 0.12 * h, 0.58 * w, 0.16 * h, "10", 0.99),
        ]
        row_ys = [0.26 * h, 0.38 * h, 0.50 * h, 0.62 * h, 0.74 * h]
        for idx in range(5):
            boxes.extend(_make_row_boxes(row_ys[idx], w, True, f"Win{idx}", "slash", 3, 2, 4, 8.0))
            boxes.extend(_make_row_boxes(row_ys[idx], w, False, f"Lose{idx}", "slash", 2, 3, 2, 6.0))

        out = parse_scoreboard(boxes, w, h)
        self.assertTrue(any("шапке" in warn for warn in out["warnings"]))

        cx1, cy1, cx2, cy2 = kda_crop_box(True, 0.25 * h, 0.31 * h, w, h)
        self.assertTrue(0 <= cx1 < cx2 <= w)
        self.assertTrue(0 <= cy1 < cy2 <= h)

    def test_glued_rapidocr_tokens_fullwidth_and_split_decimals(self):
        w, h = 1920, 1080
        boxes = [
            Box(0.44 * w, 0.04 * h, 0.56 * w, 0.10 * h, "VICTORY", 0.99),
            Box(0.42 * w, 0.12 * h, 0.45 * w, 0.16 * h, "16", 0.99),
            Box(0.55 * w, 0.12 * h, 0.58 * w, 0.16 * h, "10", 0.99),
            # Left row 0: glued gold+rating+MVP
            Box(0.16 * w, 0.24 * h, 0.23 * w, 0.28 * h, "Bjorn", 0.99),
            Box(0.27 * w, 0.24 * h, 0.29 * w, 0.28 * h, "8", 0.99),
            Box(0.31 * w, 0.24 * h, 0.33 * w, 0.28 * h, "2", 0.99),
            Box(0.35 * w, 0.24 * h, 0.37 * w, 0.28 * h, "6", 0.99),
            Box(0.38 * w, 0.24 * h, 0.48 * w, 0.28 * h, "11.2k 11.4MVP", 0.97),
            # Left row 1..4
            *_make_row_boxes(0.38 * h, w, True, "Dark Knight", "split", 2, 2, 4, 8.2),
            *_make_row_boxes(0.50 * h, w, True, "Freya", "split", 2, 3, 5, 7.9),
            *_make_row_boxes(0.62 * h, w, True, "Arthas", "split", 2, 2, 6, 7.4),
            *_make_row_boxes(0.74 * h, w, True, "Kael", "split", 2, 1, 7, 7.1),
            # Right row 0: glued SVPG + rating + gold + K + D, plus separate A
            Box(0.52 * w, 0.24 * h, 0.72 * w, 0.28 * h, "SVPG 8.9  10.1k   6  3", 0.87),
            Box(0.73 * w, 0.24 * h, 0.75 * w, 0.28 * h, "4", 0.99),
            Box(0.78 * w, 0.24 * h, 0.84 * w, 0.28 * h, "Gunnar", 0.99),
            # Right row 1: fullwidth NFKC digit '２'
            Box(0.54 * w, 0.36 * h, 0.63 * w, 0.40 * h, "5.87.4k", 0.99),
            Box(0.65 * w, 0.36 * h, 0.67 * w, 0.40 * h, "２", 0.95),
            Box(0.69 * w, 0.36 * h, 0.71 * w, 0.40 * h, "4", 0.98),
            Box(0.73 * w, 0.36 * h, 0.75 * w, 0.40 * h, "5", 0.99),
            Box(0.78 * w, 0.36 * h, 0.84 * w, 0.40 * h, "Loki", 0.99),
            # Right row 2..4 (row 4 has split decimal rating "4." + ".9")
            *_make_row_boxes(0.50 * h, w, False, "Astrid", "split", 1, 4, 3, 5.5),
            *_make_row_boxes(0.62 * h, w, False, "Soren", "split", 1, 3, 2, 5.1),
            Box(0.54 * w, 0.72 * h, 0.56 * w, 0.76 * h, "4.", 0.98),
            Box(0.56 * w, 0.72 * h, 0.57 * w, 0.76 * h, ".9", 0.87),
            Box(0.59 * w, 0.72 * h, 0.63 * w, 0.76 * h, "6.5k", 0.99),
            Box(0.65 * w, 0.72 * h, 0.74 * w, 0.76 * h, "0/2/3", 0.99),
            Box(0.78 * w, 0.72 * h, 0.84 * w, 0.76 * h, "Ragnar", 0.99),
        ]

        out = parse_scoreboard(boxes, w, h)
        self.assertEqual(len(out["players"]), 10)
        self.assertEqual(out["mvp"], "Bjorn")
        self.assertEqual(out["svp"], "Gunnar")
        self.assertEqual(out["players"][0]["player_name"], "Bjorn")
        self.assertEqual(out["players"][0]["rating"], 11.4)
        self.assertEqual(out["players"][1]["player_name"], "Dark Knight")
        self.assertEqual(out["players"][5]["player_name"], "Gunnar")
        self.assertEqual((out["players"][5]["kills"], out["players"][5]["deaths"], out["players"][5]["assists"]), (6, 3, 4))
        self.assertEqual(out["players"][5]["rating"], 8.9)
        self.assertEqual(out["players"][6]["kills"], 2)
        self.assertEqual(out["players"][6]["rating"], 5.8)
        self.assertEqual(out["players"][9]["rating"], 4.9)
        self.assertEqual(out["warnings"], [])


if __name__ == "__main__":
    unittest.main()
