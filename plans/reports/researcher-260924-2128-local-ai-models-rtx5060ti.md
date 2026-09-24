# Nghiên cứu: Chọn model/tool AI local cho pipeline story-video (RTX 5060 Ti 16GB)

Ngày: 2026-09-24. Phần cứng: Windows 11, 32GB RAM, RTX 5060 Ti 16GB (Blackwell, sm_120), triển khai dự kiến qua Docker Desktop + WSL2.

## 0. Tương thích phần cứng (Blackwell sm_120)

- PyTorch chính thức hỗ trợ Blackwell (sm_120) từ bản 2.7.0 (CUDA 12.8, wheel cu128), các bản 2.8–2.10 (bản ổn định hiện tại, 01/2026) đều hỗ trợ đầy đủ. **Bắt buộc** dùng PyTorch ≥2.7 build cu128, driver NVIDIA ≥570.xx — bản cũ hơn sẽ báo lỗi "sm_120 not compatible" (đã gặp trong nhiều repo như Fooocus, FramePack trước khi họ update). Nguồn: pytorch/pytorch#164342, discuss.pytorch.org.
- Docker Desktop trên Windows + WSL2 hỗ trợ GPU passthrough (GPU-PV) cho Blackwell, cần driver ≥570.xx + CUDA 12.8. Một số repo cần mount thêm `/usr/lib/wsl/lib` và set `LD_LIBRARY_PATH` cho đúng driver Blackwell trong container. Nguồn: docs.docker.com/desktop/features/gpu, docker.com blog WSL2 GPU.
- Khuyến nghị: dùng image Docker có sẵn PyTorch cu128 (hoặc build lại từ base nvidia/cuda:12.8), pin driver, test riêng từng model trước khi ráp pipeline — nhiều repo cộng đồng (ComfyUI, Fooocus...) vẫn còn thread riêng "50-series support" cần theo dõi.

## 1. Local LLM (story writing / outline / dịch)

| Model | VRAM (GGUF Q4-Q6) | Tốc độ ước tính (RTX 5060 Ti) | Chất lượng fiction EN/VI | License |
|---|---|---|---|---|
| **Qwen3 14B** | ~9-10GB | ~25-35 tok/s | Đa ngôn ngữ tốt, VI khá ổn (Qwen train nhiều dữ liệu Trung/Á), fiction EN tốt | Apache 2.0 |
| Mistral Small 22B | ~13-14GB (Q4) | ~15-20 tok/s | Fiction EN mạnh hơn Qwen3 14B, VI yếu hơn (ít train data VI) | Apache 2.0 |
| Qwen3-30B-A3B (MoE) | ~16-18GB (biên) | nhanh do active params thấp (~3B) nhưng sát giới hạn VRAM | Cân bằng, đa ngôn ngữ tốt | Apache 2.0 |

Đánh giá: chạy qua Ollama hoặc llama.cpp (GGUF) đơn giản nhất, vLLM chỉ đáng dùng nếu cần serving nhiều request song song (không cần cho pipeline 1-luồng này). **Khuyến nghị: Qwen3 14B** làm mặc định (đa ngôn ngữ + Apache 2.0 an toàn thương mại), dùng Mistral Small 22B làm fallback khi cần văn phong tiếng Anh giàu cảm xúc hơn cho đoạn cao trào. Độ trưởng thành cao (Ollama/llama.cpp ổn định nhiều năm). Rủi ro: chất lượng văn xuôi dài (30 phút–3 giờ nội dung) từ LLM 14-22B vẫn thua models đóng lớn (GPT-4 class) về mạch truyện dài — nên outline theo chương rồi generate từng đoạn, không generate 1 lần toàn bộ.

## 2. Sinh ảnh (character/scene)

| Model | VRAM (16GB) | Tốc độ/ảnh 1024px | Chất lượng | License |
|---|---|---|---|---|
| **Qwen-Image** (bản gốc, không phải 2.1) | GGUF Q4_K_M ~12-13GB, FP8 ~16GB | ~20-30s | Text-in-image tốt nhất, prompt adherence tốt | **Apache 2.0** |
| Flux.1 schnell | FP8 ~12GB | Nhanh nhất (~10-15s, ít bước) | Thấp hơn Flux dev, đủ dùng cho scene phụ | Apache 2.0 |
| Flux.1 dev | FP8 ~13GB | ~20s | Cao nhất trong nhóm Flux, prompt adherence tốt | **Non-Commercial** (xem cảnh báo license bên dưới) |

Qwen-Image-2.1 (bản mới) đã đổi sang "Qwen Research License" hạn chế non-commercial — **tránh dùng bản 2.1** cho kênh kiếm tiền, chỉ dùng Qwen-Image bản gốc (Apache 2.0). ComfyUI làm backend qua API (ComfyUI-API hoặc websocket) là lựa chọn trưởng thành nhất, có node quản lý GGUF/FP8 sẵn.

**Khuyến nghị: Qwen-Image (Apache 2.0)** làm mặc định để tránh rủi ro license trên kênh YouTube monetized; Flux.1 schnell làm fallback tốc độ. Flux.1 dev chỉ dùng nếu mua license thương mại từ Black Forest Labs hoặc gọi qua API trả phí của họ (outputs từ API được phép dùng thương mại theo điều khoản BFL 08/2026) — **không tự host Flux dev cho nội dung kiếm tiền nếu chưa mua license**.

## 3. Character consistency

| Phương pháp | VRAM | Chất lượng | Ghi chú |
|---|---|---|---|
| **LoRA training** (ai-toolkit cho Flux.2/Qwen-Image, Kohya cho Flux.1) | Flux LoRA fit 16GB với fused backward pass (thậm chí 8GB tối ưu); Qwen-Image LoRA tương tự | Cao nhất, ổn định nhất qua nhiều scene, cần train riêng mỗi nhân vật (15-30 ảnh, ~1500-2500 step) | Tốn thời gian one-time (~30-60 phút/nhân vật), nhưng consistency tốt nhất cho truyện nhiều nhân vật lặp lại |
| PuLID(-Flux) | Thấp nhất trong nhóm adapter | Giữ identity tốt, ít "kéo" style từ ảnh ref — phù hợp khi cần identity nhất quán nhưng đổi lighting/scene | Không cần train, nhanh setup |
| InstantID | Cao nhất (thêm ControlNet riêng) | Ổn định nhưng "dính" lighting/pose/expression ảnh gốc nhiều hơn PuLID | Cộng đồng gọi "InstantID để ổn định, PuLID để trung thực" |
| Qwen-Image-Edit (reference-edit) | Tương tự Qwen-Image base | Điểm GEdit-Bench cao hơn Flux Kontext (8.00 vs 7.16 semantic consistency) | Apache 2.0 (kế thừa Qwen-Image gốc), dùng để edit/re-pose nhân vật đã có giữa các scene |
| Flux Kontext dev | ~13GB | Giữ facial feature tốt qua nhiều lần edit liên tiếp | **Non-Commercial license** — cùng rủi ro như Flux dev |

**Khuyến nghị: kết hợp LoRA (train riêng mỗi nhân vật chính, Apache-license base) + PuLID cho nhân vật phụ không đáng train + Qwen-Image-Edit để chỉnh sửa/re-pose giữa scene.** Tránh Flux Kontext dev nếu không mua license thương mại — Qwen-Image-Edit là lựa chọn Apache 2.0 thay thế có điểm benchmark tốt hơn.

## 4. TTS (EN + VI)

| Model | Ngôn ngữ | Voice cloning | VRAM | License | Ghi chú |
|---|---|---|---|---|---|
| **Chatterbox(-Turbo)** | EN chính, đa ngôn ngữ hạn chế | Có (~5s sample) + dial cảm xúc | Thấp-vừa | **MIT** | Tốt nhất cho EN narration cần cảm xúc/nhân vật đa giọng |
| Kokoro | EN + vài ngôn ngữ | Không (54 giọng cố định) | Rất thấp, chạy được CPU | **Apache 2.0** | Ổn định nhất cho narration dài (không voice-clone), 82M params |
| **VieNeu-TTS v3 Turbo** | Tiếng Việt (chuyên) | Có, on-device | Thấp | **Apache 2.0** | Bản v4 (chất lượng cao hơn) đã đóng nguồn, chỉ có qua API vieneu.io — dùng v3 Turbo open-source cho local |
| viXTTS | Tiếng Việt (fine-tune từ XTTS) | Có | Vừa | Kế thừa Coqui XTTS **CPML** (non-commercial trừ khi mua license) | **Cảnh báo license — không dùng cho kênh monetized nếu chưa mua license Coqui** |
| F5-TTS | Đa ngôn ngữ, có cộng đồng fine-tune VI | Có | Vừa | **CC-BY-NC 4.0 — non-commercial only** | **Loại khỏi lựa chọn cho dự án kiếm tiền** |
| Higgs Audio v2 | Đa ngôn ngữ | Có | Vừa-cao | Apache 2.0 | Cạnh tranh với API trả phí, ít dữ liệu benchmark riêng cho VI |

**Khuyến nghị: Chatterbox (MIT) cho tiếng Anh, VieNeu-TTS v3 Turbo (Apache 2.0) cho tiếng Việt.** Cả hai license an toàn cho YouTube monetized. Tránh F5-TTS và viXTTS cho bản phát hành thương mại trừ khi mua license tương ứng. Độ trưởng thành: Chatterbox và Kokoro đã qua nhiều benchmark cộng đồng 2025-2026; VieNeu-TTS là dự án Việt Nam còn khá mới (theo dõi thêm về độ ổn định long-form >30 phút, nên test trước khi cam kết pipeline).

## 5. Subtitle alignment

| Công cụ | VRAM | Tốc độ | Độ chính xác | Ghi chú |
|---|---|---|---|---|
| **faster-whisper** (CTranslate2, large-v3) | Thấp (~4-5GB) | Nhanh hơn realtime nhiều lần trên GPU | Transcription tốt, timestamp cấp câu chính xác | Nền tảng ổn định, license MIT-compatible |
| WhisperX (faster-whisper + wav2vec2 forced alignment) | Thêm ít VRAM cho wav2vec2 | Vẫn nhanh | Word-level <100ms cho tiếng Anh; **kém tin cậy hơn cho tiếng Việt** (nhiều báo cáo lỗi timestamp từ khi dùng `--language vi`, do model wav2vec2 alignment không train tốt cho VI) | BSD-2 license |

**Khuyến nghị:** dùng faster-whisper + WhisperX cho tiếng Anh (word-level phù hợp karaoke-subtitle). Với tiếng Việt, dùng faster-whisper large-v3 lấy timestamp cấp segment/câu (built-in, đủ ổn định) thay vì ép forced-alignment word-level của WhisperX — tránh rủi ro lệch từ đã ghi nhận trong issue tracker của whisperX.

## 6. Video assembly

- **FFmpeg + NVENC** trên Blackwell: NVENC thế hệ 9, hỗ trợ AV1 Ultra Quality, cải thiện ~5% chất lượng so với thế hệ trước; encode nhanh (hàng trăm fps cho AV1/HEVC trên GPU dòng cao, 5060 Ti thấp hơn nhưng vẫn nhanh hơn nhiều lần realtime cho video 1080p). Ken Burns/pan-zoom/parallax làm bằng filter `zoompan`/overlay của FFmpeg (CPU-nhẹ) rồi encode NVENC — đã trưởng thành, không rủi ro license (FFmpeg LGPL/GPL tùy build).
- **Clip chuyển động AI ngắn (optional)**: so sánh LTX-Video vs Wan 2.2.
  - LTX-Video: chạy vừa 16-24GB ở 720p, tốc độ nhanh hơn hẳn (~25-40s/clip 5s trên RTX 4090) — phù hợp nhất cho 16GB do margin VRAM thoải mái hơn.
  - Wan 2.2: chất lượng chuyển động tốt hơn nhưng bản 14B cần >16GB (dùng bản 1.3B/5B để fit), chậm hơn (~90-120s/clip 5s trên RTX 4090, RTX 5060 Ti sẽ chậm hơn nữa do ít CUDA core/VRAM bandwidth hơn).
- **Khuyến nghị:** dùng ảnh tĩnh + Ken Burns/parallax làm mặc định cho toàn bộ video dài (30 phút–3 giờ) vì chi phí render AI-motion cho hàng trăm scene là không khả thi; chỉ dùng LTX-Video cho vài shot "hero" (mở đầu, cao trào) để tiết kiệm thời gian, Wan 2.2 (bản nhẹ) làm fallback khi cần chất lượng chuyển động cao hơn cho một số cảnh chọn lọc.

## 7. GPU scheduling & ước tính throughput cho video 1 giờ

16GB không đủ giữ đồng thời LLM (9-14GB) + diffusion (12-16GB) + TTS + whisper trong VRAM — bắt buộc **load/unload tuần tự theo stage**, mỗi stage là 1 service riêng (Docker container hoặc subprocess), giải phóng VRAM (`ollama stop`, ComfyUI free model, xóa whisper model) trước khi chuyển stage tiếp theo. Không chạy song song các stage nặng GPU.

Ước tính thô cho video hoàn chỉnh dài 1 giờ (audio-story, ảnh tĩnh + vài clip motion):
- LLM viết outline + script (~9.000-12.000 từ) + dịch: 15-30 phút (bao gồm vài lần generate lại đoạn).
- Sinh ảnh scene (ước ~60-120 ảnh cho 1 giờ nội dung, ~20-30s/ảnh với Qwen-Image FP8): 30-60 phút.
- Train LoRA nhân vật (one-time, không lặp lại mỗi video nếu tái sử dụng nhân vật): 30-60 phút/nhân vật.
- TTS EN+VI cho ~1 giờ audio: 15-30 phút (đa số model tạo nhanh hơn realtime trên GPU).
- Subtitle alignment (faster-whisper) cho 1 giờ audio: 5-10 phút.
- Video assembly + encode NVENC: 5-15 phút.
- Clip motion AI (nếu dùng, vài clip): +5-15 phút.

**Tổng ước tính: ~2-3.5 giờ compute local cho mỗi 1 giờ video thành phẩm**, phần lớn thời gian nằm ở sinh ảnh scene. Video 30 phút–3 giờ sẽ scale gần tuyến tính theo số ảnh/độ dài audio.

## Stack khuyến nghị

**Mặc định (an toàn license, cân bằng chất lượng/tốc độ):**
1. LLM: Qwen3 14B (Ollama, Apache 2.0)
2. Ảnh: Qwen-Image bản gốc (ComfyUI + GGUF/FP8, Apache 2.0)
3. Consistency: LoRA (ai-toolkit) cho nhân vật chính + PuLID cho phụ + Qwen-Image-Edit để chỉnh sửa
4. TTS: Chatterbox (EN, MIT) + VieNeu-TTS v3 Turbo (VI, Apache 2.0)
5. Subtitle: faster-whisper (+ WhisperX chỉ cho EN)
6. Video: FFmpeg NVENC + Ken Burns/parallax, LTX-Video cho vài clip motion chọn lọc

**Fallback (khi cần chất lượng cao hơn, chấp nhận license phức tạp/chi phí):**
- LLM: Mistral Small 22B (fiction EN mạnh hơn)
- Ảnh: Flux.1 dev/Flux Kontext dev — **chỉ khi đã mua license thương mại từ Black Forest Labs**
- Video motion: Wan 2.2 (bản 1.3B/5B) cho chất lượng chuyển động cao hơn

## Cảnh báo license (kênh YouTube monetized)

- **Flux.1 dev / Flux Kontext dev / Flux Pro-Flex**: Non-Commercial License — tự host để tạo nội dung kiếm tiền cần mua license thương mại từ Black Forest Labs. Chỉ Flux.1 schnell/klein là Apache 2.0 miễn phí thương mại.
- **Qwen-Image-2.1** (bản mới): đổi sang Qwen Research License (non-commercial) — dùng **Qwen-Image bản gốc** (Apache 2.0) thay thế.
- **F5-TTS**: CC-BY-NC 4.0, non-commercial only — không dùng cho video kiếm tiền.
- **viXTTS**: kế thừa Coqui XTTS CPML — cần license thương mại riêng, không mặc định free-commercial.
- **VieNeu-TTS v4**: chỉ có qua API trả phí (proprietary), bản open-source local hiện tại là v3 Turbo (Apache 2.0).

## Giới hạn nghiên cứu

Chưa benchmark thực tế trên chính RTX 5060 Ti (số liệu tốc độ suy ra từ RTX 4090/4080, cần điều chỉnh giảm ~30-40% do băng thông/CUDA core thấp hơn). Chưa test độ ổn định VieNeu-TTS và faster-whisper cho audio narration dài liên tục >30 phút (nguy cơ trôi giọng/lặp). Chưa kiểm tra tương thích cụ thể của node ComfyUI-Qwen-Image-Edit và PuLID-Flux với PyTorch cu128/sm_120 (một số custom node cộng đồng có thể chưa update kịp cho Blackwell).

Status: DONE_WITH_CONCERNS

## Câu hỏi chưa giải quyết

1. Cần benchmark thực nghiệm trên RTX 5060 Ti thật để xác nhận tốc độ/VRAM (số liệu hiện suy ra từ GPU khác).
2. Độ ổn định của VieNeu-TTS và Chatterbox cho audio narration dài >30 phút liên tục (nguy cơ trôi giọng) chưa được kiểm chứng — nên test pilot trước khi cam kết pipeline.
3. Cần xác minh trực tiếp điều khoản FLUX.1 dev license (v2.0) về việc output có được dùng thương mại khi tự host hay không — nguồn có mâu thuẫn giữa "non-commercial toàn bộ" và "output qua API được thương mại"; nên đọc kỹ license gốc hoặc liên hệ Black Forest Labs trước khi quyết định dùng Flux dev.
4. Custom node ComfyUI cho PuLID-Flux/Qwen-Image-Edit có tương thích đầy đủ với PyTorch cu128 (Blackwell) hay chưa cần kiểm tra trực tiếp trước khi build pipeline.
