// Package runtimeassets contains the pinned, reproducible NeMo runtime bundle.
package runtimeassets

const (
	BundleVersion = "nemo-asr-v2"
	Image         = "swarm-nemo-asr:nemo-asr-v2"
	BaseImage     = "nvcr.io/nvidia/nemo:24.12"
	ContainerName = "nemo-asr"
	ManagedLabel  = "com.swarm.voice.runtime=nemo-asr-v2"
)

var Files = map[string][]byte{
	"Dockerfile": []byte(`FROM nvcr.io/nvidia/nemo:24.12
RUN python3 -m pip install --no-cache-dir \
    fastapi==0.115.6 \
    python-multipart==0.0.20 \
    soundfile==0.12.1 \
    uvicorn==0.34.0
COPY server.py /opt/swarm/server.py
CMD ["python3", "/opt/swarm/server.py"]
`),
	"compose.yaml": []byte(`services:
  nemo-asr:
    image: swarm-nemo-asr:nemo-asr-v2
    build:
      context: .
      dockerfile: Dockerfile
    container_name: nemo-asr
    restart: unless-stopped
    labels:
      com.swarm.voice.runtime: nemo-asr-v2
    ports:
      - "127.0.0.1:${NEMO_PORT}:8001"
    environment:
      NEMO_MODEL: ${NEMO_MODEL}
    volumes:
      - ${NEMO_CACHE_DIR}:/root/.cache:rw
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
    healthcheck:
      test: ["CMD", "python3", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8001/health', timeout=3)"]
      interval: 10s
      timeout: 5s
      retries: 12
`),
	"server.py": []byte(`import asyncio
import io
import os
import tempfile

import nemo.collections.asr as nemo_asr
import soundfile as sf
import torch
import uvicorn
from fastapi import FastAPI, File, Form, HTTPException, Response, UploadFile

MODEL_ID = os.environ.get("NEMO_MODEL", "nvidia/parakeet-tdt-0.6b-v3")
MAX_UPLOAD_BYTES = 50 * 1024 * 1024
MAX_AUDIO_SECONDS = 600
inference_slot = asyncio.Semaphore(1)
app = FastAPI(title="Swarm NeMo ASR", version="1")
model = None

@app.on_event("startup")
def load_model():
    global model
    model = nemo_asr.models.ASRModel.from_pretrained(MODEL_ID)
    model.eval()
    if torch.cuda.is_available():
        model = model.cuda()

@app.get("/health")
def health():
    return {"status": "ok" if model is not None else "loading", "model": MODEL_ID}

@app.get("/models")
def models():
    return {"models": [{"id": MODEL_ID, "name": MODEL_ID, "ready": model is not None}]}

@app.options("/v1/audio/transcriptions")
def transcription_options(response: Response):
    response.headers["Allow"] = "OPTIONS, POST"
    response.headers["X-Swarm-Transcription-Contract"] = "openai-multipart-v1"
    return Response(status_code=204, headers=response.headers)

@app.post("/v1/audio/transcriptions")
async def transcribe(
    file: UploadFile = File(...),
    model_name: str = Form(MODEL_ID, alias="model"),
    response_format: str = Form("json"),
):
    if response_format not in ("json", "verbose_json"):
        raise HTTPException(status_code=400, detail="unsupported response_format")
    if model_name and model_name != MODEL_ID:
        raise HTTPException(status_code=400, detail="requested model does not match loaded model")
    if model is None:
        raise HTTPException(status_code=503, detail="model loading")
    payload = await file.read(MAX_UPLOAD_BYTES + 1)
    if len(payload) > MAX_UPLOAD_BYTES:
        raise HTTPException(status_code=413, detail="audio upload exceeds 50 MiB")
    try:
        audio, sample_rate = sf.read(io.BytesIO(payload), always_2d=False)
    except Exception as exc:
        raise HTTPException(status_code=400, detail="invalid audio") from exc
    if sample_rate <= 0 or len(audio) / sample_rate > MAX_AUDIO_SECONDS:
        raise HTTPException(status_code=413, detail="audio duration exceeds 10 minutes")
    with tempfile.NamedTemporaryFile(suffix=".wav") as tmp:
        sf.write(tmp.name, audio, sample_rate)
        async with inference_slot:
            result = await asyncio.to_thread(model.transcribe, [tmp.name])
    text = result[0].text if hasattr(result[0], "text") else str(result[0])
    return {"text": text, "model": MODEL_ID}

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8001)
`),
}
