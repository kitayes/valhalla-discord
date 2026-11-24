FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1 \
    libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Pre-download ONNX detection and East-Slavic+Latin recognition models into the
# image with retry so the container starts fast and runs 100% offline.
RUN for i in 1 2 3; do \
        python3 -c "import cv2, numpy as np; from rapidocr import RapidOCR, LangRec, OCRVersion, ModelType; img = np.zeros((120, 400, 3), dtype=np.uint8); cv2.putText(img, 'VICTORY 123', (20, 75), cv2.FONT_HERSHEY_SIMPLEX, 1.5, (255, 255, 255), 3); ocr = RapidOCR(params={'Global.use_cls': False, 'Rec.ocr_version': OCRVersion.PPOCRV5, 'Rec.lang_type': LangRec.ESLAV, 'Rec.model_type': ModelType.MOBILE, 'Det.unclip_ratio': 1.15}); res = ocr(img); assert res.txts, 'warm-up failed to exercise rec model'" \
        && break; \
        echo "RapidOCR warm-up attempt $i failed, retrying..." >&2; \
        [ "$i" -eq 3 ] && exit 1; \
        sleep 3; \
    done

COPY app ./app

EXPOSE 8000

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
