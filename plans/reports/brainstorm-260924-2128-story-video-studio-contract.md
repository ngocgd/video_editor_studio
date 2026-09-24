# Brainstorm: Story Video Studio — web app sinh video kể truyện cho YouTube

Ngày: 2026-09-24 · Trạng thái: CHỜ USER DUYỆT

Nguồn research:
- [Thị trường, bản quyền, chính sách](researcher-260924-2128-market-channels-copyright-policy.md)
- [Model AI local trên RTX 5060 Ti](researcher-260924-2128-local-ai-models-rtx5060ti.md)
- [Kiến trúc backend + goclaw](researcher-260924-2128-backend-architecture-goclaw.md)

## 1. Quyết định user đã chốt

| # | Quyết định |
|---|---|
| 1 | Render cả EN và VI (chọn theo từng project). Kênh đầu tiên nhắm thị trường nước ngoài (EN). |
| 2 | Giai đoạn 1 dùng nội bộ (1 người, chạy trên máy local). Giai đoạn 2 lên SaaS. |
| 3 | Nguồn truyện: truyện AI tự sáng tác + import truyện tu tiên Trung Quốc có sẵn (user tự chịu quyết định nội dung sau khi đã biết rủi ro). |
| 4 | Định dạng chính: video dài kiểu audio story (ảnh + chuyển động nhẹ + giọng đọc + phụ đề). |
| 5 | LLM mặc định chạy local; Claude/Gemini là provider tùy chọn; muốn gọi Claude CLI theo pattern goclaw. |
| 6 | Có bước kiểm duyệt; có auto-upload để thử beta. |
| 7 | Affiliate không nằm trong phạm vi web này. |

## 2. Contract

**Outcome.** Web app nội bộ "Story Video Studio" cho phép: nhập setting (thể loại, độ dài, ngôn ngữ, số tập) hoặc import truyện có sẵn → sinh/biên tập cốt truyện theo tập → tạo nhân vật nhất quán (ảnh tham chiếu + LoRA) → tách cảnh, sinh ảnh cảnh → lồng tiếng đa giọng → phụ đề → render video dài → duyệt → upload YouTube (private/scheduled) kèm thumbnail, tiêu đề, mô tả → xem analytics kênh và gợi ý tối ưu. Kiến trúc sẵn sàng lên SaaS mà không phải viết lại.

**Constraints.**
- Phần cứng: Windows 11, 32GB RAM, RTX 5060 Ti 16GB (Blackwell → PyTorch ≥ 2.7, CUDA 12.8, driver ≥ 570). Không model nào được chạy song song trên GPU; mọi job GPU phải xếp hàng 1 slot.
- Backend Go, triển khai bằng Docker (WSL2 + NVIDIA Container Toolkit). Worker AI bằng Python vì hệ sinh thái AI chỉ có ở Python.
- Chỉ dùng model có license cho phép thương mại (kênh có kiếm tiền).
- Upload chỉ qua YouTube Data API chính thức với OAuth. Không dùng kỹ thuật vượt captcha hay giả lập trình duyệt để né cơ chế chống tự động.
- Code phải SaaS-ready: `tenant_id` trên mọi bảng, có auth kể cả bản local, storage qua S3 API.

**Non-goals.**
- Affiliate, landing page marketing SaaS, billing/thanh toán (giai đoạn SaaS mới làm).
- Gen video AI full-motion cho toàn bộ video (chỉ vài clip "hero" tùy chọn).
- App mobile.
- Tool vượt captcha, né phát hiện bản quyền.

**Acceptance criteria.**
1. Từ 1 setting EN, app sinh được 1 tập truyện ≥ 30 phút và render ra MP4 1080p có giọng đọc, phụ đề khớp, ≥ 2 nhân vật nhận ra được là cùng một người qua các cảnh.
2. Làm được như (1) với tiếng Việt.
3. Import 1 chương truyện có sẵn (text) → ra video như (1).
4. Mỗi bước pipeline chạy lại được riêng lẻ (sửa 1 cảnh chỉ render lại cảnh đó); app tắt giữa chừng thì chạy tiếp từ bước dở.
5. UI hiển thị tiến độ realtime của từng job và hàng đợi GPU.
6. Upload lên YouTube ở chế độ private/scheduled kèm thumbnail + metadata, có bật cờ khai báo nội dung AI khi cần.
7. Trang analytics hiển thị view, watch time, CTR, retention theo video (YouTube Analytics API).
8. Chuyển provider LLM Ollama ↔ Claude ↔ Gemini bằng cấu hình, không sửa code.

## 3. Mục tiêu kinh doanh (để đo, không phải để code)

- Mục tiêu kênh: đạt YPP (1.000 sub + 4.000 giờ xem trong 12 tháng) càng sớm càng tốt.
- Năng suất: 1 máy làm được khoảng 5–8 giờ video thành phẩm mỗi ngày (ước tính 2–3,5 giờ máy chạy cho 1 giờ video; cần benchmark thật).
- Tránh "inauthentic content": mỗi video có biên tập người thật, không dùng một template lặp lại.

## 4. Cấu trúc web (sơ đồ trang)

```
Dashboard ─ tổng quan job, hàng đợi GPU, video chờ duyệt, số liệu kênh
Projects (Series)
 └─ Project detail
     ├─ Story Bible   — thế giới, cảnh giới tu luyện, tóm tắt, style
     ├─ Characters    — hồ sơ, ảnh tham chiếu, LoRA, giọng đọc gán cho nhân vật
     ├─ Episodes      — outline → bản thảo → biên tập (editor văn bản)
     │   └─ Storyboard — danh sách cảnh: đoạn văn, prompt ảnh, ảnh, chuyển động, giọng
     ├─ Render        — cấu hình (độ dài, tỉ lệ, ngôn ngữ, nhạc nền) + tiến độ từng bước
     └─ Publish       — thumbnail, tiêu đề, mô tả, tag, lịch đăng, trạng thái upload
Import            — nhập truyện (text/file), tách chương, dịch
Review Queue      — video chờ duyệt trước khi đăng
Library           — video, ảnh, audio đã tạo; dọn dung lượng
Analytics         — số liệu kênh, theo video, gợi ý tối ưu
Settings          — kênh YouTube (OAuth), provider LLM, model manager, preset giọng/style
```

Pipeline dữ liệu: `Series → Episode → Scene → (Image, Narration, Subtitle) → Render → Review → Publish → Analytics`.

## 5. Phương án backend

| | A. Go + River + Python workers (**khuyến nghị**) | B. Go + Temporal + Python workers | C. Go monolith gọi Python subprocess |
|---|---|---|---|
| Điều phối | River (queue trên Postgres), mỗi bước pipeline là 1 job, state lưu DB | Temporal workflow, replay tự động | Goroutine + bảng trạng thái tự viết |
| Hạ tầng thêm | Không (dùng lại Postgres) | Temporal server + DB riêng | Không |
| Resume theo bước | Tự thiết kế state machine đơn giản | Có sẵn, mạnh nhất | Tự viết toàn bộ |
| Giả định chính | Pipeline là chuỗi bước tuyến tính, ít rẽ nhánh | Sẽ có nhiều workflow phức tạp, nhiều tenant | Mãi chạy 1 máy |
| Hỏng đầu tiên khi | Workflow có nhiều nhánh song song + chờ người duyệt dài ngày | Máy local yếu, vận hành nặng cho 1 người | Lên SaaS, cần tách GPU worker ra máy khác |
| Chi phí bỏ hướng | Thấp: logic từng bước tái dùng, chỉ thay lớp điều phối | Trung bình | Cao |

Stack của phương án A:
- **API:** Go (chi router + net/http), Postgres + sqlc + pgx, goose migration, River queue (queue `gpu` concurrency=1, queue `cpu` song song), SSE cho tiến độ.
- **Worker AI:** Python service độc lập (TTS, align, LLM helper). Giao tiếp gRPC có streaming tiến độ. ComfyUI gọi bằng HTTP + WebSocket sẵn có.
- **Render:** FFmpeg NVENC, do Go điều phối.
- **Storage:** MinIO (S3 API), sau chuyển sang R2/S3 chỉ bằng đổi cấu hình.
- **Frontend:** React + Vite + TanStack Router/Query + shadcn/ui (SPA dạng tool, không cần SSR).
- **Deploy:** `docker-compose.yml` (core) + `docker-compose.gpu.yml` (ComfyUI, worker Python).

## 6. Stack model AI (license cho phép thương mại — cần xác minh lại khi cài)

| Bước | Mặc định | Dự phòng | Ghi chú |
|---|---|---|---|
| LLM | Gemma 4 12B (Q6_K, ~13GB, ~27 tok/s) hoặc Qwen3.5-9B (Q8, ~11GB, ~25 tok/s); bản mạnh hơn Qwen3.6-27B chỉ vừa ở Q3 (mức "marginal") — số liệu từ llmfit chạy trên máy, chưa đo thực | Claude qua `claude` CLI (local), Claude/Gemini API key (SaaS) | Chất lượng truyện dài VI của model local cần thử thực tế |
| Ảnh cảnh (số lượng lớn) | Z-Image Turbo (Apache 2.0, ~2–3s/ảnh) | Illustrious-XL/NoobAI cho style anime thuần | License từng checkpoint Civitai phải kiểm tra riêng |
| Ảnh có chữ, thumbnail | Qwen-Image gốc (Apache 2.0) | API trả phí: Nano Banana Pro / Grok Imagine / GPT Image | Qwen-Image-2.1 là repo riêng, license non-commercial → loại |
| Nhân vật | LoRA (ai-toolkit) + Qwen-Image-Edit 2509/2511 (Apache 2.0) | PuLID; character sheet gốc có thể làm bằng API trả phí | |
| TTS EN | Chatterbox (MIT) | Orpheus 3B (Apache 2.0) cho đoạn cần emotion tag; premium: ElevenLabs v3 / Gemini TTS | VibeVoice loại (Microsoft khuyến cáo không dùng thương mại) |
| TTS VI | VieNeu-TTS v3 (Apache 2.0) | — | F5-TTS, viXTTS license không cho thương mại → loại |
| Phụ đề | WhisperX (EN), faster-whisper (VI) | | |
| Video | FFmpeg NVENC Ken Burns/parallax | LTX-Video / Wan 2.2 cho clip ngắn | |

## 7. Trade-offs và rủi ro

- **Truyện Trung Quốc có sẵn:** research không tìm thấy vụ đánh gậy cụ thể nhắm vào kênh audiobook tu tiên, và cũng không có chương trình license cho creator. Nghĩa là rủi ro enforcement hiện thấp, nhưng về pháp lý vẫn là vi phạm, và China Literature từng dùng DMCA. App chỉ cung cấp tính năng import; quyết định nội dung thuộc về user. Khuyến nghị kênh chính dùng truyện tự sáng tác.
- **Claude qua CLI (pattern goclaw):** goclaw spawn `claude -p --output-format stream-json` và dùng phiên đăng nhập subscription. Tự chạy trên máy cá nhân được xem là "ordinary use" của Claude Code, nhưng dùng cho pipeline sinh nội dung tự động số lượng lớn là vùng xám về ToS. Khi lên SaaS thì **bắt buộc** chuyển sang API key. Đề xuất: làm cả 2 adapter (`claude-cli` cho local, `anthropic-api` cho SaaS) sau cùng một interface.
- **YouTube API:** project chưa audit thì video upload bị khóa private. Beta sẽ upload private rồi user tự publish, song song nộp form audit.
- **Thông tin chưa xác minh (không dùng làm căn cứ cho tới khi kiểm tra lại):** quota upload đổi còn 1 unit/lần từ 06/2026; ngưỡng YPP tăng gấp đôi từ 02/2027; Anthropic chặn server-side token subscription từ đầu 2026; Qwen-Image 2.1 đổi sang license phi thương mại.

## 8. Better approaches

- Frontend dùng Vite SPA thay cho Next.js, vì app là tool nội bộ, không cần SEO/SSR.
- Điều phối bằng River thay cho Redis/Asynq, vì bỏ được một thành phần hạ tầng mà vẫn resume được từng bước.
- Video dựng từ ảnh tĩnh + chuyển động thay cho gen video AI toàn bộ: nhanh hơn hàng chục lần trên 16GB, và đúng định dạng các kênh audio story đang làm.

## 9. Bước tiếp theo

Sau khi user duyệt contract này → `/ak:bootstrap` (mode full) với contract làm đầu vào: chốt design UI/UX (tránh AI-slop), rồi lập plan theo phase trong `plans/`.

## 10. Quyết định bổ sung (21:57)

- Backend: **phương án A (River)**.
- LLM provider Claude: gọi qua `claude` CLI theo pattern của goclaw (đã có tại `C:/Users/ADMIN/goclaw`, file `internal/providers/claude_cli*.go`). Pattern: `exec.CommandContext` chạy `claude -p --output-format stream-json --verbose --model <m> --permission-mode <mode> [--resume|--session-id <uuid>]`, đọc NDJSON (`assistant` → text delta, `result` → nội dung cuối + usage), khóa theo session, lọc biến môi trường `CLAUDE*`. Vì story generation chỉ cần text, tắt toàn bộ tool của CLI. goclaw dùng license CC BY-NC 4.0 nên **viết lại theo pattern, không copy code** sang sản phẩm sẽ thương mại hóa.
- Tên sản phẩm: **Loomtale Studio** (tên làm việc, đổi được bằng cấu hình).
- Nhạc nền: **bỏ khỏi phạm vi** (user quyết định 22:09).
- Kiểm tra máy thật (22:09): GPU RTX 5060 Ti 16GB, compute 12.0, driver 617.14; Docker 29.2 chạy GPU trong container OK (`nvidia/cuda:12.8` thấy GPU); RAM 31.8GB nhưng lúc đo chỉ trống ~10.8GB; ổ C trống 137GB; Python 3.12, Node 20, Claude Code 2.1.281 có sẵn; **chưa cài** Go, Ollama, PyTorch trên host.
- Research bổ sung: [so sánh model ảnh](researcher-260924-2145-image-model-comparison-local-vs-paid.md), [giọng đọc và TTS](researcher-260924-2145-narration-voice-tts-channels.md).

## 11. Yêu cầu phi chức năng (user yêu cầu 22:5x — ưu tiên hàng đầu)

Design đã duyệt: dark pro-tool, màu nhấn jade, wireframe tại `docs/wireframe/`.

**Hiệu năng / mượt:**
- Budget: JS khởi tạo ≤ 200KB gzip; route code-splitting; tương tác chính < 100ms; lưới 300+ cảnh và timeline 3 giờ vẫn 60fps.
- Virtualize mọi danh sách/lưới dài (TanStack Virtual); ảnh cảnh phục vụ bản thumbnail WebP/AVIF sinh sẵn nhiều kích thước, lazy-load; waveform tính sẵn ở backend (peaks JSON), không decode audio trên trình duyệt.
- Cập nhật tiến độ qua SSE, gộp (throttle) sự kiện, cập nhật cache TanStack Query theo từng scene thay vì refetch cả trang.
- Backend: truy vấn có index, phân trang cursor, không N+1; file lớn upload/download thẳng MinIO bằng presigned URL, API không proxy bytes.

**Tái sử dụng / code tối ưu:**
- Một thư viện UI chung (shadcn + component riêng: SceneCard, StatusChips, JobProgress, Inspector panel…) dùng lại ở mọi màn.
- Hợp đồng API một nguồn: OpenAPI sinh TypeScript client + Zod types; proto sinh Go/Python cho gRPC; sqlc sinh Go cho DB. Không viết tay kiểu dữ liệu hai lần.
- Provider interface chung cho LLM/Image/TTS để thêm engine không sửa pipeline.

**Scale (hiểu "sale" là scale — cần user xác nhận):**
- API stateless, scale ngang; River worker scale theo queue; GPU worker tách rời, trỏ được sang máy/cloud GPU khác.
- `tenant_id` mọi bảng + quota; storage S3; không lưu state trong bộ nhớ process.

**Security:**
- Auth session cookie HttpOnly/SameSite + CSRF; mật khẩu argon2id; RBAC (owner/editor/viewer) chuẩn bị cho SaaS; rate limit.
- OAuth refresh token YouTube và API key mã hoá at-rest (envelope encryption, khoá ngoài DB); secret chỉ qua env/secret store, không log.
- `claude` CLI chạy với mọi tool tắt, thư mục làm việc cô lập, timeout, giới hạn đồng thời; nội dung truyện import coi là dữ liệu không tin cậy (chống prompt injection, không bao giờ cấp tool).
- Validate mọi input (Zod ở FE, validator ở Go); kiểm tra MIME/kích thước file upload; chống SSRF khi cấu hình URL ComfyUI/Ollama; presigned URL hết hạn ngắn và gắn tenant.
- Security headers (CSP chặt, HSTS khi có HTTPS), audit log hành động nhạy cảm (publish, đổi kênh, xoá).
- CI: govulncheck, npm audit, pip-audit, gitleaks; container chạy non-root.

## Câu hỏi chưa giải quyết

1. Giả thuyết "kênh EN thành công không dùng nhạc nền" chưa được xác nhận qua search; cần nghe trực tiếp 5–10 kênh top.
2. Tốc độ và chất lượng thực của các model trên RTX 5060 Ti chưa benchmark.
3. Tên kênh YouTube chưa kiểm tra trùng.
