from __future__ import annotations

import json
from typing import Optional

from fastapi import FastAPI, File, Form, HTTPException, Query, UploadFile

from app.engine import analyze_image

app = FastAPI(title="Valhalla MLBB OCR Service", version="1.0.0")


@app.get("/health")
def health() -> dict:
    return {"status": "ok"}


@app.post("/parse")
async def parse(
    image: UploadFile = File(...),
    expected_players: Optional[str] = Form(default=None),
    debug: bool = Query(default=False),
) -> dict:
    data = await image.read()
    if not data:
        raise HTTPException(status_code=400, detail="empty image payload")

    expected_list: Optional[list[str]] = None
    if expected_players:
        try:
            decoded = json.loads(expected_players)
            if isinstance(decoded, list):
                expected_list = [str(item) for item in decoded]
        except json.JSONDecodeError as exc:
            raise HTTPException(status_code=400, detail=f"invalid expected_players json: {exc}") from exc

    try:
        return analyze_image(data, expected_players=expected_list, debug=debug)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"ocr processing failed: {exc}") from exc
