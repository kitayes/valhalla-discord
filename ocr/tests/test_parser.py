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

    def test_real_mlbb_two_line_card_geometry_raw_gold_and_lose_mvp_badge(self):
        # Modeled on real 1024x471 screenshot layout (42 VICTORY 48 + losing "MVP" crest).
        w, h = 1024, 471
        boxes = [
            Box(328, 25, 382, 68, "42", 1.0),
            Box(412, 19, 611, 59, "VICTORY", 0.996),
            Box(643, 25, 696, 68, "48", 1.0),
            Box(787, 56, 970, 71, "Длит. 28:09 23.11.2026 20:27:36", 0.976),
            Box(316, 77, 337, 94, "X", 0.627),
            # Row 0 (top Y~113, bottom rating/level Y~145 — closer to Row 1 top than Row 0 top!)
            Box(205, 105, 287, 121, "anhedo gold.", 0.958),
            Box(315, 104, 340, 121, "27", 0.96),
            Box(340, 104, 355, 121, "7", 0.97),
            Box(360, 104, 375, 121, "1", 0.98),
            Box(380, 105, 419, 120, "23919", 0.999),
            Box(605, 104, 708, 120, "16535 3 11 30", 0.971),
            Box(775, 104, 834, 121, "OMEN", 0.845),
            Box(131, 138, 152, 153, "49", 0.99),
            Box(446, 119, 484, 134, "MVP", 0.93),
            Box(448, 134, 484, 156, "12.0", 0.828),
            Box(543, 137, 571, 156, "8.8", 0.999),
            Box(851, 138, 886, 155, "15", 0.769),
            # Row 1
            Box(207, 165, 274, 182, "Jeskoo.", 0.992),
            Box(319, 164, 420, 181, "5  8  22 17160", 0.91),
            Box(605, 165, 708, 181, "15590  7  7 22", 0.958),
            Box(732, 165, 833, 181, "Fumetsu", 0.999),
            Box(452, 198, 481, 216, "8.5", 0.998),
            Box(541, 196, 573, 216, "10.1", 0.988),
            # Row 2 (losing team's best player also has literal "MVP" text!)
            Box(195, 227, 301, 241, "WI Antitoxicité.", 0.936),
            Box(319, 227, 419, 241, "2 14 21 16220", 0.96),
            Box(604, 225, 708, 241, "23262 18 10 13", 0.945),
            Box(765, 225, 833, 242, "Kozurnu", 0.99),
            Box(540, 240, 575, 255, "MVP", 0.989),
            Box(453, 257, 478, 276, "6.1", 0.996),
            Box(540, 255, 573, 276, "10.7", 0.953),
            Box(876, 262, 884, 272, "5", 0.999),
            # Row 3
            Box(211, 286, 289, 303, "twentyblqck", 0.997),
            Box(318, 285, 419, 302, "2  9  17 16679", 0.981),
            Box(605, 287, 707, 301, "20018 12 7 14", 0.962),
            Box(777, 285, 833, 304, "язура", 0.744),
            Box(452, 318, 479, 337, "6.1", 0.997),
            Box(546, 319, 570, 337, "9.5", 0.991),
            Box(858, 320, 892, 335, "48", 0.992),
            # Row 4 + footer buttons
            Box(195, 347, 300, 362, "ceylon ft.zoro-R", 0.952),
            Box(319, 346, 419, 362, "6 11 27 18117", 0.957),
            Box(605, 346, 708, 362, "15740  8  7 23", 0.938),
            Box(784, 345, 833, 362, "addzen", 0.999),
            Box(450, 378, 484, 398, "10.2", 0.997),
            Box(544, 378, 572, 398, "9.7", 0.997),
            Box(96, 426, 165, 445, "Данные", 0.958),
            Box(220, 429, 308, 445, "Быстрый чат", 0.959),
            Box(357, 426, 462, 446, '"Лайк" всем', 0.968),
            Box(879, 426, 935, 443, "Выйти", 0.662),
            Box(54, 458, 209, 470, "Боевой ID 498198225103377251", 0.962),
        ]

        out = parse_scoreboard(boxes, w, h)
        self.assertEqual(len(out["players"]), 10)
        self.assertEqual(out["header_win_kills"], 42)
        self.assertEqual(out["header_lose_kills"], 48)
        self.assertEqual(out["mvp"], "anhedo gold.")
        self.assertEqual(out["svp"], "Kozurnu")
        self.assertEqual(out["warnings"], [])
        self.assertEqual(out["players"][0]["player_name"], "anhedo gold.")
        self.assertEqual((out["players"][0]["kills"], out["players"][0]["deaths"], out["players"][0]["assists"]), (27, 7, 1))
        self.assertEqual(out["players"][0]["rating"], 12.0)
        self.assertEqual(out["players"][4]["player_name"], "ceylon ft.zoro-R")
        self.assertEqual(out["players"][4]["rating"], 10.2)
        self.assertEqual(out["players"][5]["player_name"], "OMEN")
        self.assertEqual(out["players"][7]["rating"], 10.7)

    def test_real_mlbb_fuzzy_defeai_ictory_and_banner_kills_over_row0(self):
        # Verifies 'DEFEAI' + 'Вернуться в лобби' + Row 0 '2 4' does NOT pollute header kills 16 vs 22.
        w, h = 1024, 471
        boxes = [
            Box(456, 8, 566, 22, "Вернуться в лобби", 0.994),
            Box(328, 25, 382, 68, "16", 0.999),
            Box(420, 19, 604, 59, "DEFEAI", 0.999),
            Box(641, 24, 696, 67, "22", 1.0),
            # Left LOSE rows (sum K = 16)
            Box(194, 105, 260, 121, "StarsPlayer", 0.95),
            Box(319, 105, 376, 120, "2 4 4", 0.95),
            Box(381, 105, 417, 120, "7054", 1.0),
            Box(454, 137, 480, 156, "5.6", 0.999),
            Box(193, 165, 272, 182, "PHT Yato.", 0.981),
            Box(319, 165, 417, 182, "5  7  2  9873", 0.94),
            Box(454, 197, 481, 217, "5.2", 0.998),
            Box(194, 226, 259, 243, "Acrolev", 0.996),
            Box(319, 225, 376, 242, "0 4 7", 0.95),
            Box(381, 226, 417, 241, "8615", 0.999),
            Box(453, 258, 480, 277, "5.8", 0.999),
            Box(193, 285, 250, 303, "KalaM", 0.959),
            Box(319, 285, 376, 302, "0 5 9", 0.96),
            Box(381, 287, 415, 301, "8181", 0.999),
            Box(454, 318, 481, 338, "6.2", 0.999),
            Box(194, 346, 273, 363, "ONI naoki.", 0.95),
            Box(319, 346, 376, 364, "9 2 2", 0.96),
            Box(379, 347, 420, 362, "10603", 0.999),
            Box(451, 362, 482, 374, "MVP", 0.97),
            Box(451, 379, 484, 397, "10.3", 0.999),
            # Right WIN rows (sum K = 22)
            Box(606, 104, 642, 122, "7616", 1.0),
            Box(648, 104, 703, 120, "0 6 8", 0.98),
            Box(771, 104, 835, 121, "K-i-ll-e-r", 0.931),
            Box(544, 137, 572, 156, "4.8", 0.999),
            Box(605, 165, 705, 182, "14317 13 0 3", 0.958),
            Box(766, 165, 833, 182, "HikKAcent.", 0.93),
            Box(541, 180, 572, 193, "MVP", 0.933),
            Box(541, 198, 575, 216, "12.6", 0.999),
            Box(606, 225, 708, 242, "9118  3  4 12", 0.938),
            Box(744, 227, 834, 243, "ася добрая", 0.963),
            Box(544, 259, 571, 277, "8.9", 0.999),
            Box(607, 287, 646, 301, "8893", 0.999),
            Box(648, 285, 705, 302, "4 3 5", 0.98),
            Box(762, 287, 832, 303, "EXXTAZY*", 0.987),
            Box(545, 319, 572, 338, "8.5", 0.997),
            Box(605, 347, 645, 362, "10480", 0.999),
            Box(647, 346, 705, 363, "2 3 6", 0.95),
            Box(732, 346, 832, 363, "Криво-Traher", 0.912),
            Box(545, 379, 572, 398, "7.5", 0.998),
        ]

        out = parse_scoreboard(boxes, w, h, header_side_hint="left")
        self.assertEqual(out["header_win_kills"], 22)
        self.assertEqual(out["header_lose_kills"], 16)
        self.assertEqual(out["players"][0]["result"], "LOSE")
        self.assertEqual(out["players"][5]["result"], "WIN")
        self.assertEqual(out["mvp"], "HikKAcent.")
        self.assertEqual(out["svp"], "ONI naoki.")
        self.assertEqual(out["warnings"], [])

        # Also verify fuzzy 'ICTORY' and bevelled 'CIORY' match VICTORY even without hint.
        for title_txt in ("ICTORY", "CIORY"):
            vict_boxes = [
                Box(324, 24, 377, 67, "21", 1.0),
                Box(448, 24, 609, 59, title_txt, 0.912),
                Box(653, 25, 684, 65, "2", 1.0),
            ] + boxes[4:]
            out_vict = parse_scoreboard(vict_boxes, w, h)
            self.assertEqual(out_vict["header_win_kills"], 21)
            self.assertEqual(out_vict["header_lose_kills"], 2)
            self.assertEqual(out_vict["players"][0]["result"], "WIN")
            self.assertEqual(out_vict["players"][5]["result"], "LOSE")
            self.assertFalse(any("VICTORY/DEFEAT" in w_msg for w_msg in out_vict["warnings"]))

    def test_real_mlbb_glued_name_stats_clan_numbers_and_ui_overlays(self):
        w, h = 1024, 472
        boxes = [
            Box(326, 25, 381, 68, "28", 1.0),
            Box(413, 19, 611, 59, "VICTORY", 0.994),
            Box(644, 26, 695, 67, "18", 0.999),
            Box(92, 47, 248, 60, "Оставьте отзыв об этом матче", 0.977),
            # Row 0
            Box(193, 105, 275, 121, "♂ NURMO", 0.95),
            Box(320, 105, 375, 121, "8 3 9", 0.99),
            Box(381, 105, 420, 121, "12919", 0.999),
            Box(450, 138, 484, 156, "10.9", 0.997),
            Box(605, 106, 644, 121, "11550", 0.999),
            Box(648, 105, 703, 120, "4 6 9", 0.99),
            Box(787, 106, 821, 121, "ness♂", 0.999),
            Box(543, 121, 572, 133, "MVP", 0.97),
            Box(541, 138, 575, 156, "10.5", 0.998),
            # Row 1
            Box(193, 165, 276, 182, "SVR ayumi", 0.934),
            Box(320, 165, 375, 182, "5 5 9", 0.99),
            Box(380, 166, 419, 181, "10879", 0.999),
            Box(454, 198, 481, 217, "7.6", 0.998),
            Box(493, 178, 503, 195, "+", 0.799),
            Box(605, 165, 710, 183, "10731  1  7 12", 0.932),
            Box(792, 165, 835, 183, "minau", 0.998),
            Box(544, 198, 572, 217, "7.0", 0.996),
            # Toast overlay in the middle of scoreboard
            Box(415, 213, 609, 232, "Скопировано в буфер обмена", 0.976),
            # Row 2
            Box(192, 226, 284, 244, "sehnsuchtw", 0.993),
            Box(320, 225, 419, 242, "1 2 20 11314", 0.952),
            Box(450, 241, 480, 254, "MVP", 0.91),
            Box(451, 259, 484, 278, "11.2", 0.998),
            Box(605, 226, 709, 242, "13542  3  4 10", 0.915),
            Box(768, 225, 835, 244, "Пучина", 0.91),
            Box(544, 259, 572, 278, "8.8", 0.998),
            # Row 3: numeric clan tag '009 Nuuur' must keep '009' inside player_name!
            Box(192, 287, 275, 303, "009 Nuuur", 0.95),
            Box(320, 287, 375, 303, "1 4 6", 0.99),
            Box(380, 287, 419, 302, "10606", 0.999),
            Box(453, 319, 481, 338, "6.6", 0.998),
            Box(521, 301, 532, 317, "3", 0.997),
            Box(605, 287, 706, 303, "12395 6 7 5", 0.95),
            Box(775, 285, 835, 305, "Чернуар", 0.92),
            Box(545, 319, 571, 339, "6.9", 0.999),
            # Row 4: glued 'VORA ClYDeee. 33. 13 4 6 13613' + 'натсуw игратьснова' + apostrophe rating "9'6"
            Box(193, 346, 420, 364, "VORA ClYDeee. 33. 13 4 6 13613", 0.95),
            Box(451, 380, 484, 398, "9'6", 0.90),
            Box(607, 347, 705, 364, "9936 4 4 9", 0.98),
            Box(764, 350, 874, 364, "натсуw игратьснова", 0.806),
            Box(545, 380, 572, 399, "8.2", 0.998),
            Box(836, 394, 849, 405, "©", 0.573),
        ]

        out = parse_scoreboard(boxes, w, h)
        self.assertEqual(out["warnings"], [])
        self.assertEqual(out["players"][0]["player_name"], "NURMO")
        self.assertEqual(out["players"][3]["player_name"], "009 Nuuur")
        self.assertEqual((out["players"][3]["kills"], out["players"][3]["deaths"], out["players"][3]["assists"]), (1, 4, 6))
        self.assertEqual(out["players"][4]["player_name"], "VORA ClYDeee. 33.")
        self.assertEqual((out["players"][4]["kills"], out["players"][4]["deaths"], out["players"][4]["assists"]), (13, 4, 6))
        self.assertEqual(out["players"][4]["rating"], 9.6)
        self.assertEqual(out["players"][5]["player_name"], "ness")
        self.assertEqual(out["players"][9]["player_name"], "натсуw")


if __name__ == "__main__":
    unittest.main()
